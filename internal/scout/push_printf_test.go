package scout

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/krabiswabbie/busyscout/internal/telnet"
	"github.com/krabiswabbie/busyscout/internal/testutil"
)

// Observations are synchronized because fragment workers use separate sessions.
type uploadObservation struct {
	mu       sync.Mutex
	dirs     map[string]bool
	modes    map[string]os.FileMode
	commands []string
}

var scratchPattern = regexp.MustCompile(`/tmp/bs-upload-[0-9a-f]+`)

func (o *uploadObservation) record(command string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.commands = append(o.commands, command)
	for _, dir := range scratchPattern.FindAllString(command, -1) {
		o.dirs[dir] = true
		if info, err := os.Stat(dir); err == nil {
			o.modes[dir] = info.Mode().Perm()
		}
	}
}
func (o *uploadObservation) snapshot() ([]string, []string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	dirs := make([]string, 0, len(o.dirs))
	for dir := range o.dirs {
		dirs = append(dirs, dir)
	}
	return dirs, append([]string(nil), o.commands...)
}
func printfFixture(t *testing.T, init string) (string, string, *uploadObservation) {
	t.Helper()
	dir := t.TempDir()
	o := &uploadObservation{dirs: make(map[string]bool), modes: make(map[string]os.FileMode)}
	// Protect unrelated global fragments if these regressions run against the old implementation.
	guard := `rm() { for arg do case "$arg" in /tmp/bs.*.part) return 0;; esac; done; command rm "$@"; }; `
	host := testutil.StartShell(t, testutil.ShellOptions{Dir: dir, Env: []string{"LC_ALL=C"}, Init: guard + init, OnCommand: o.record})
	t.Cleanup(func() {
		dirs, _ := o.snapshot()
		for _, dir := range dirs {
			os.RemoveAll(dir)
		}
	})
	return dir, host, o
}
func printfScout(t *testing.T, dir, host, target string, data []byte) *Scout {
	t.Helper()
	source := filepath.Join(t.TempDir(), "source.bin")
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(source, host+":"+target, ModePrintf, false)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func assertOwnedScratchRemoved(t *testing.T, o *uploadObservation) {
	t.Helper()
	dirs, _ := o.snapshot()
	if len(dirs) == 0 {
		t.Fatal("transfer did not create private scratch directory")
	}
	for _, dir := range dirs {
		o.mu.Lock()
		mode, seen := o.modes[dir]
		o.mu.Unlock()
		if !seen || mode != 0700 {
			t.Errorf("scratch was not private: %s mode %o, seen %v", dir, mode, seen)
		}
		if _, err := os.Lstat(dir); !os.IsNotExist(err) {
			t.Errorf("scratch survives transfer: %s: %v", dir, err)
		}
	}
}
func assertFileBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("content mismatch: got %d bytes %q, want %d bytes %q", len(got), got, len(want), want)
	}
}

func TestCheckFileSizeRequiresExactDecimal(t *testing.T) {
	// Searching ls fields accepts its link count as a size; accepting multiple fields or signed numbers also hides malformed output.
	for _, tc := range []struct {
		name, init string
		expected   int
		diagnostic string
	}{
		{"link count is not size", "", 1, "expected 1, actual 3"},
		{"extra field", `wc() { printf '3 99\n'; };`, 3, "invalid"},
		{"signed value", `wc() { printf '+3\n'; };`, 3, "invalid"},
		{"wc failure", `wc() { printf 'size refused\n' >&2; return 31; };`, 3, "size refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, host, _ := printfFixture(t, tc.init)
			path := filepath.Join(dir, "size")
			os.WriteFile(path, []byte("abc"), 0600)
			s := printfScout(t, dir, host, path, nil)
			err := s.checkFileSize(tc.expected, path)
			if err == nil || !strings.Contains(err.Error(), tc.diagnostic) {
				t.Fatalf("want %q size error, got %v", tc.diagnostic, err)
			}
		})
	}
}
func TestPushPrintfRefusedSameSizeOverwrite(t *testing.T) {
	// An unchecked destination redirection can falsely succeed because the old bytes have the expected size.
	for _, old := range [][]byte{nil, []byte("old same size")} {
		t.Run(fmt.Sprintf("size%d", len(old)), func(t *testing.T) {
			dir, host, o := printfFixture(t, "set -C")
			target := filepath.Join(dir, "target")
			os.WriteFile(target, old, 0600)
			data := bytes.Repeat([]byte("x"), len(old))
			s := printfScout(t, dir, host, target, data)
			if err := s.Push(); err == nil {
				t.Fatal("refused overwrite must fail even when old size matches")
			}
			assertFileBytes(t, target, old)
			assertOwnedScratchRemoved(t, o)
		})
	}
}
func TestPushPrintfReplacesBytesAndLiteralPaths(t *testing.T) {
	// Missing truncation retains old tails; unquoted/normalized paths execute source or select another filename.
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"empty", nil}, {"shorter", []byte("new")}, {"binary", bytes.Repeat([]byte{0, 255, 13, 10, 27, 39, 92}, 400)},
		{"space quote' $() ; * ? back\\slash", []byte("literal")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, host, o := printfFixture(t, "")
			target := filepath.Join(dir, tc.name)
			os.WriteFile(target, bytes.Repeat([]byte("old"), 1400), 0600)
			s := printfScout(t, dir, host, target, tc.data)
			if err := s.Push(); err != nil {
				t.Fatal(err)
			}
			assertFileBytes(t, target, tc.data)
			assertOwnedScratchRemoved(t, o)
			entries, _ := os.ReadDir(dir)
			if len(entries) != 1 {
				t.Fatalf("literal destination caused extra files: %v", entries)
			}
		})
	}
}
func TestPushPrintfPreservesAncestorTraversal(t *testing.T) {
	// Cleaning link/.. writes to the lexical parent rather than the filesystem-selected parent.
	dir, host, o := printfFixture(t, "")
	parent := filepath.Join(dir, "elsewhere")
	os.MkdirAll(filepath.Join(parent, "child"), 0700)
	os.Symlink(filepath.Join(parent, "child"), filepath.Join(dir, "link"))
	target := dir + "/link/../result"
	s := printfScout(t, dir, host, target, []byte("physical path"))
	if err := s.Push(); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(parent, "result"), []byte("physical path"))
	if _, err := os.Stat(filepath.Join(dir, "result")); !os.IsNotExist(err) {
		t.Fatalf("lexical target created: %v", err)
	}
	assertOwnedScratchRemoved(t, o)
}
func TestPushPrintfStaleAndConcurrentFragmentsAreIsolated(t *testing.T) {
	// Shared numbered fragments or wildcard joining/deletion corrupt concurrent transfers and unrelated files.
	dir, host, o := printfFixture(t, "")
	stale, err := os.CreateTemp("/tmp", "bs.stale-*.part")
	if err != nil {
		t.Fatal(err)
	}
	stale.WriteString("unrelated stale data")
	stale.Close()
	t.Cleanup(func() { os.Remove(stale.Name()) })
	a, b := bytes.Repeat([]byte("first"), 430), bytes.Repeat([]byte("second"), 370)
	targetA, targetB := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	sa, sb := printfScout(t, dir, host, targetA, a), printfScout(t, dir, host, targetB, b)
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for _, s := range []*Scout{sa, sb} {
		wg.Add(1)
		go func(s *Scout) { defer wg.Done(); errors <- s.Push() }(s)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertFileBytes(t, targetA, a)
	assertFileBytes(t, targetB, b)
	assertFileBytes(t, stale.Name(), []byte("unrelated stale data"))
	assertOwnedScratchRemoved(t, o)
	dirs, _ := o.snapshot()
	if len(dirs) != 2 {
		t.Fatalf("concurrent transfers shared scratch: %v", dirs)
	}
}
func TestPushPrintfSizeVerificationExhaustion(t *testing.T) {
	// Discarding a failed check after a successful upload permits joining unverified fragments.
	dir, host, o := printfFixture(t, `wc() { printf '0\n'; };`)
	target := filepath.Join(dir, "target")
	os.WriteFile(target, []byte("untouched"), 0600)
	s := printfScout(t, dir, host, target, []byte("replacement"))
	err := s.Push()
	if err == nil || !strings.Contains(err.Error(), "actual 0") {
		t.Fatalf("verification failure was lost: %v", err)
	}
	assertFileBytes(t, target, []byte("untouched"))
	assertOwnedScratchRemoved(t, o)
	_, commands := o.snapshot()
	checks := 0
	for _, command := range commands {
		if strings.Contains(command, "wc -c") {
			checks++
		}
	}
	if checks != retries {
		t.Fatalf("verification should retry to exhaustion: %d checks", checks)
	}
}
func TestPushPrintfJoinAndCleanupErrors(t *testing.T) {
	// Unchecked cat/rm errors or replacing the primary error with cleanup failure report success or lose the cause.
	for _, tc := range []struct {
		name, init string
		want       []string
	}{
		{"join", `cat() { printf 'join refused\n' >&2; return 41; };`, []string{"join refused"}},
		{"cleanup", `rm() { printf 'cleanup refused\n' >&2; return 42; };`, []string{"cleanup refused"}},
		{"both", `cat() { printf 'join refused\n' >&2; return 41; }; rm() { printf 'cleanup refused\n' >&2; return 42; };`, []string{"join refused", "cleanup refused"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, host, o := printfFixture(t, tc.init)
			target := filepath.Join(dir, "target")
			s := printfScout(t, dir, host, target, []byte("new bytes"))
			err := s.Push()
			for _, want := range tc.want {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("missing %q: %v", want, err)
				}
			}
			if tc.name == "join" {
				assertOwnedScratchRemoved(t, o)
			} else {
				dirs, _ := o.snapshot()
				if len(dirs) != 1 {
					t.Fatalf("missing owned scratch: %v", dirs)
				}
				if _, err := os.Stat(dirs[0]); err != nil {
					t.Fatalf("cleanup failure must leave owned directory: %v", err)
				}
			}
		})
	}
}
func TestPushPrintfWorkerFailureWaitsBeforeCleanup(t *testing.T) {
	// Request observation alone cannot see shell operations still in flight.
	// The failing fragment waits until every slow operation has started; each
	// slow operation records completion, and cleanup records premature removal.
	markers := t.TempDir()
	init := "__bs_worker_markers=" + telnet.ShellQuote(markers) + `; wc() { size=$(command wc -c); if test "$size" -eq 5; then while :; do set -- "$__bs_worker_markers"/started.*; if test "$#" -eq 9 && test -e "$1"; then break; fi; sleep 0.01; done; : > "$__bs_worker_markers/failure-started"; printf 'fragment check refused\n' >&2; return 43; fi; : > "$__bs_worker_markers/started.$$"; while ! test -e "$__bs_worker_markers/failure-started"; do sleep 0.01; done; sleep 0.5; : > "$__bs_worker_markers/finished.$$"; printf '%s\n' "$size"; }; rm() { ( set -- "$__bs_worker_markers"/finished.*; if test "$#" -ne 9 || ! test -e "$1"; then : > "$__bs_worker_markers/cleanup-too-early"; fi ); command rm "$@"; };`
	dir, host, o := printfFixture(t, init)
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	s := printfScout(t, dir, host, target, bytes.Repeat([]byte("x"), chunkSize*(threads-1)+5))
	err := s.Push()
	if err == nil || !strings.Contains(err.Error(), "fragment check refused") {
		t.Fatalf("worker failure missing: %v", err)
	}
	assertFileBytes(t, target, []byte("old"))
	assertOwnedScratchRemoved(t, o)
	entries, err := os.ReadDir(markers)
	if err != nil {
		t.Fatal(err)
	}
	started, finished := 0, 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "started.") {
			started++
		}
		if strings.HasPrefix(entry.Name(), "finished.") {
			finished++
		}
		if entry.Name() == "cleanup-too-early" {
			t.Error("cleanup began before all in-flight shell operations completed")
		}
	}
	if started != 9 || finished != started {
		t.Fatalf("Push returned with shell operations still running: started %d, finished %d", started, finished)
	}
}
func TestPushPrintfJoinBatchesStayBounded(t *testing.T) {
	// One unbounded exact-list command exceeds old interactive shell line limits for large uploads.
	dir, host, o := printfFixture(t, "")
	target := filepath.Join(dir, "target")
	data := bytes.Repeat([]byte{91}, chunkSize*40+1)
	s := printfScout(t, dir, host, target, data)
	if err := s.Push(); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, target, data)
	assertOwnedScratchRemoved(t, o)
	_, commands := o.snapshot()
	joins := 0
	for _, command := range commands {
		if strings.Contains(command, "cat ") {
			joins++
			if len(command) > 1024 {
				t.Fatalf("join request exceeds bounded shell line: %d bytes", len(command))
			}
		}
	}
	if joins < 2 {
		t.Fatalf("large upload did not use bounded join batches: %d", joins)
	}
}

func TestPushPrintfLaterJoinBatchFailure(t *testing.T) {
	// Losing an append error after a successful first batch can silently truncate the transfer.
	dir, host, o := printfFixture(t, `cat() { case "$*" in *000004.part*) printf 'later append refused\n' >&2; return 44;; *) command cat "$@";; esac; };`)
	target := filepath.Join(dir, "target")
	data := bytes.Repeat([]byte("z"), chunkSize*9)
	s := printfScout(t, dir, host, target, data)
	err := s.Push()
	if err == nil || !strings.Contains(err.Error(), "later append refused") {
		t.Fatalf("later join failure missing: %v", err)
	}
	assertFileBytes(t, target, data[:chunkSize*4])
	assertOwnedScratchRemoved(t, o)
}
func TestPushPrintfFinalSizeFailure(t *testing.T) {
	// Checking only fragment sizes misses a destination write that reports success but emits fewer bytes.
	dir, host, o := printfFixture(t, `cat() { command cat "$@" | head -c 3; };`)
	target := filepath.Join(dir, "target")
	s := printfScout(t, dir, host, target, []byte("replacement"))
	err := s.Push()
	if err == nil || !strings.Contains(err.Error(), "expected 11, actual 3") {
		t.Fatalf("final size failure missing: %v", err)
	}
	assertFileBytes(t, target, []byte("rep"))
	assertOwnedScratchRemoved(t, o)
}

func TestPushPrintfWorkerWriteFailureAndCleanup(t *testing.T) {
	// Discarding failed fragment writes or cleanup errors must not allow a join or hide the primary failure.
	for _, cleanupFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("cleanupFails%v", cleanupFails), func(t *testing.T) {
			init := `printf() { case "$1" in '\170'*) command printf 'worker write refused\n' >&2; return 45;; *) command printf "$@";; esac; };`
			if cleanupFails {
				init += `rm() { command printf 'cleanup refused\n' >&2; return 42; };`
			}
			dir, host, o := printfFixture(t, init)
			target := filepath.Join(dir, "target")
			os.WriteFile(target, []byte("old"), 0600)
			s := printfScout(t, dir, host, target, []byte("xxx"))
			err := s.Push()
			if err == nil || !strings.Contains(err.Error(), "worker write refused") {
				t.Fatalf("worker write error lost: %v", err)
			}
			assertFileBytes(t, target, []byte("old"))
			if cleanupFails {
				if !strings.Contains(err.Error(), "cleanup refused") {
					t.Fatalf("cleanup cause lost: %v", err)
				}
			} else {
				assertOwnedScratchRemoved(t, o)
			}
			_, commands := o.snapshot()
			writes := 0
			for _, command := range commands {
				if strings.Contains(command, `\170`) {
					writes++
				}
			}
			if writes != retries {
				t.Fatalf("write failure should retry to exhaustion: %d writes", writes)
			}
		})
	}
}
