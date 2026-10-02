package scout

import (
	"github.com/krabiswabbie/busyscout/internal/testutil"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func remotePathFixture(t *testing.T) (string, string, string) {
	t.Helper()
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("behavioral remote-path tests require a local POSIX shell")
	}
	dir := t.TempDir()
	source := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(source, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	host := testutil.StartShell(t, testutil.ShellOptions{Dir: dir, Env: []string{"LC_ALL=C"}})
	return dir, source, host
}

func TestNewRemoteDestinationKinds(t *testing.T) {
	// Accepting absent-looking leaves misses parent errors; following -d/-f before -L admits links.
	for _, kind := range []string{"directory", "regular file", "missing file", "symlink to file", "symlink to directory", "dangling symlink", "FIFO", "missing parent", "non-directory parent"} {
		t.Run(kind, func(t *testing.T) {
			dir, source, host := remotePathFixture(t)
			destination := filepath.Join(dir, "target")
			want := filepath.ToSlash(destination)
			wantError := ""
			var err error
			switch kind {
			case "directory":
				err = os.Mkdir(destination, 0700)
				want += "/payload.bin"
			case "regular file", "non-directory parent":
				err = os.WriteFile(destination, []byte("old content"), 0400)
				if kind == "non-directory parent" {
					destination = filepath.Join(destination, "leaf")
					wantError = "Not a directory"
				}
			case "symlink to file", "symlink to directory", "dangling symlink":
				target := filepath.Join(dir, "referent")
				if kind == "symlink to file" {
					err = os.WriteFile(target, []byte("referent"), 0600)
				} else if kind == "symlink to directory" {
					err = os.Mkdir(target, 0700)
				}
				if err == nil {
					err = os.Symlink(target, destination)
				}
				wantError = "symlink"
			case "FIFO":
				if _, err := exec.LookPath("mkfifo"); err != nil {
					t.Skip("mkfifo unavailable")
				}
				err = exec.Command("mkfifo", destination).Run()
				wantError = "not a regular file"
			case "missing parent":
				destination = filepath.Join(dir, "absent", "leaf")
				wantError = "No such file or directory"
			}
			if err != nil {
				t.Fatal(err)
			}
			s, err := New(source, host+":"+filepath.ToSlash(destination), ModePrintf, false)
			if wantError != "" {
				if err == nil || !strings.Contains(err.Error(), wantError) {
					t.Fatalf("expected %q diagnostic, got %v", wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if s.remote.Path != want {
				t.Fatalf("got destination %q, want %q", s.remote.Path, want)
			}
			if kind == "regular file" {
				content, err := os.ReadFile(destination)
				if err != nil || string(content) != "old content" {
					t.Fatalf("constructor changed existing destination: %q, %v", content, err)
				}
			} else if _, err := os.Lstat(filepath.FromSlash(want)); !os.IsNotExist(err) {
				t.Fatalf("constructor must not create destination: %v", err)
			}
		})
	}
}

func TestNewRechecksDirectoryBasename(t *testing.T) {
	// Omitting the second classification would admit a forbidden final leaf.
	for _, kind := range []string{"directory", "symlink", "dangling symlink", "FIFO", "regular file"} {
		t.Run(kind, func(t *testing.T) {
			dir, source, host := remotePathFixture(t)
			leaf := filepath.Join(dir, "payload.bin")
			wantError := "not a regular file"
			var err error
			switch kind {
			case "directory":
				err = os.Mkdir(leaf, 0700)
			case "symlink", "dangling symlink":
				target := filepath.Join(dir, "referent")
				if kind == "symlink" {
					err = os.WriteFile(target, []byte("referent"), 0600)
				}
				if err == nil {
					err = os.Symlink(target, leaf)
				}
				wantError = "symlink"
			case "FIFO":
				if _, err := exec.LookPath("mkfifo"); err != nil {
					t.Skip("mkfifo unavailable")
				}
				err = exec.Command("mkfifo", leaf).Run()
			case "regular file":
				err = os.WriteFile(leaf, []byte("old content"), 0400)
				wantError = ""
			}
			if err != nil {
				t.Fatal(err)
			}
			s, err := New(source, host+":"+filepath.ToSlash(dir), ModePrintf, false)
			if wantError != "" {
				if err == nil || !strings.Contains(err.Error(), wantError) {
					t.Fatalf("expected %q rejection of final leaf, got %v", wantError, err)
				}
			} else if err != nil || s.remote.Path != filepath.ToSlash(leaf) {
				t.Fatalf("existing final file must be accepted: %v, %v", s, err)
			}
		})
	}
}

func TestNewRemotePathErrorKeepsPermissionCause(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires enforced Unix directory permissions")
	}
	dir, source, host := remotePathFixture(t)
	private := filepath.Join(dir, "private")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(private, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(private, 0700) })
	for _, destination := range []string{private, filepath.Join(private, "leaf")} {
		_, err := New(source, host+":"+filepath.ToSlash(destination), ModePrintf, false)
		if err == nil || !strings.Contains(err.Error(), "Permission denied") {
			t.Fatalf("must retain Permission denied for %q, got %v", destination, err)
		}
	}
}

func TestNewMissingRemoteFileDoesNotRequireWritableParent(t *testing.T) {
	dir, source, host := remotePathFixture(t)
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0700) })
	destination := filepath.ToSlash(filepath.Join(dir, "missing"))
	s, err := New(source, host+":"+destination, ModePrintf, false)
	if err != nil || s.remote.Path != destination {
		t.Fatalf("classification must not require writability: %v, %v", s, err)
	}
	if _, err := os.Stat(filepath.FromSlash(destination)); !os.IsNotExist(err) {
		t.Fatalf("classification created destination: %v", err)
	}
}

func TestNewRemotePathIsLiteral(t *testing.T) {
	// Unquoted operands and converting Unix backslashes change these names.
	// Interpreting command substitution creates the sentinel.
	for _, name := range []string{"space name", "quote'name", "-leading", "semi;colon", "$(touch injected)", "star*question?", "back\\slash"} {
		t.Run(name, func(t *testing.T) {
			dir, source, host := remotePathFixture(t)
			destination := filepath.Join(dir, name)
			if err := os.Mkdir(destination, 0700); err != nil {
				t.Fatal(err)
			}
			remotePath := filepath.ToSlash(destination)
			s, err := New(source, host+":"+remotePath, ModePrintf, false)
			if err != nil || s.remote.Path != remotePath+"/payload.bin" {
				t.Fatalf("literal directory operand changed: %v, %v", s, err)
			}
			if _, err := os.Stat(filepath.Join(dir, "injected")); !os.IsNotExist(err) {
				t.Fatalf("path executed shell source: %v", err)
			}
		})
	}
}

func TestNewRemotePathRejectsLineControls(t *testing.T) {
	dir, source, host := remotePathFixture(t)
	for _, control := range []string{"\r", "\n", "\x00"} {
		_, err := New(source, host+":"+filepath.ToSlash(dir)+"/bad"+control+"path", ModePrintf, false)
		if err == nil || !strings.Contains(err.Error(), "unsupported shell path") {
			t.Fatalf("unsafe path must be rejected: %v", err)
		}
	}
}

func TestNewPreservesSourceValidationAndDefaultPath(t *testing.T) {
	_, source, host := remotePathFixture(t)
	_, err := New(source+".missing", host, ModePrintf, false)
	if err == nil || !strings.Contains(err.Error(), "source file does not exist") {
		t.Fatalf("missing local source must fail: %v", err)
	}
	s, err := New(source, host, ModePrintf, false)
	if err != nil || s.remote.Path != "/tmp/payload.bin" {
		t.Fatalf("expected default /tmp/payload.bin: %v, %v", s, err)
	}
}

func TestNewRemoteTrailingSlashDoesNotHideSymlink(t *testing.T) {
	dir, source, host := remotePathFixture(t)
	referent := filepath.Join(dir, "directory")
	if err := os.Mkdir(referent, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(referent, link); err != nil {
		t.Fatal(err)
	}
	_, err := New(source, host+":"+filepath.ToSlash(link)+"/", ModePrintf, false)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("trailing slash must not hide destination symlink: %v", err)
	}
}

func TestNewRemoteParentTraversalKeepsComponents(t *testing.T) {
	dir, source, host := remotePathFixture(t)
	for _, suffix := range []string{"/absent/../leaf", "/absent/"} {
		_, err := New(source, host+":"+filepath.ToSlash(dir)+suffix, ModePrintf, false)
		if err == nil || !strings.Contains(err.Error(), "No such file or directory") {
			t.Fatalf("parent traversal must retain missing component %q: %v", suffix, err)
		}
	}
}

func TestNewPreservesRemoteDirectoryTraversal(t *testing.T) {
	// Lexically removing link/.. redirects the upload and classifies the wrong final leaf.
	for _, kind := range []string{"regular file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir, source, host := remotePathFixture(t)
			physicalParent := filepath.Join(dir, "elsewhere")
			if err := os.MkdirAll(filepath.Join(physicalParent, "child"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(physicalParent, "child"), filepath.Join(dir, "link")); err != nil {
				t.Fatal(err)
			}
			physicalLeaf := filepath.Join(physicalParent, "payload.bin")
			if kind == "symlink" {
				if err := os.Symlink(filepath.Join(dir, "referent"), physicalLeaf); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(physicalLeaf, []byte("correct existing leaf"), 0600); err != nil {
				t.Fatal(err)
			}
			remoteDirectory := filepath.ToSlash(dir) + "/link/.."
			s, err := New(source, host+":"+remoteDirectory, ModePrintf, false)
			if kind == "symlink" {
				if err == nil || !strings.Contains(err.Error(), "symlink") {
					t.Fatalf("must reject final symlink selected by physical traversal: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if s.remote.Path != remoteDirectory+"/payload.bin" {
				t.Fatalf("remote directory spelling changed: got %q, want %q", s.remote.Path, remoteDirectory+"/payload.bin")
			}
			data, err := os.ReadFile(physicalLeaf)
			if err != nil || string(data) != "correct existing leaf" {
				t.Fatalf("constructor changed physical destination: %q, %v", data, err)
			}
			if _, err := os.Lstat(filepath.Join(dir, "payload.bin")); !os.IsNotExist(err) {
				t.Fatalf("constructor created lexical destination: %v", err)
			}
		})
	}
}

func TestNewMissingLeafWithLegacyCdAndLiteralParent(t *testing.T) {
	// Using cd -P rejects every new leaf on msh; an ls operand starting with '-'
	// without a literal relative prefix gets treated as an option instead.
	for _, name := range []string{"plain", "-parent/leaf", "link/../new leaf"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(t.TempDir(), "source")
			os.WriteFile(source, []byte("data"), 0600)
			os.Mkdir(filepath.Join(dir, "-parent"), 0700)
			physical := filepath.Join(dir, "physical")
			os.MkdirAll(filepath.Join(physical, "child"), 0700)
			os.Symlink(filepath.Join(physical, "child"), filepath.Join(dir, "link"))
			init := `cd() { if test "$1" = '-P'; then printf 'legacy cd has no -P\n' >&2; return 19; fi; command cd "$@"; };`
			host := testutil.StartShell(t, testutil.ShellOptions{Dir: dir, Env: []string{"LC_ALL=C"}, Init: init})
			h, p, _ := net.SplitHostPort(host)
			s := &Scout{remote: &RemoteFile{Host: h, Port: p, Path: name}, localFile: source, mode: ModePrintf}
			isDir, err := s.checkIsRemoteDirectory(name)
			if err != nil || isDir || s.remote.Path != name {
				t.Fatalf("literal parent must admit new leaf: %v %v", s, err)
			}
			if _, err := os.Stat(filepath.Join(physical, "new leaf")); !os.IsNotExist(err) {
				t.Fatalf("classification created destination: %v", err)
			}
		})
	}
}
