package telnet

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/krabiswabbie/busyscout/internal/testutil"
)

func realCheckedShell(t *testing.T, options testutil.ShellOptions) *TelnetClient {
	t.Helper()
	host, port, _ := net.SplitHostPort(testutil.StartShell(t, options))
	tc := &TelnetClient{Address: host, Port: port, Timeout: time.Second}
	if err := tc.Dial(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tc.Close)
	return tc
}

func TestExecuteCheckedSuccessAndOutput(t *testing.T) {
	tc := realCheckedShell(t, testutil.ShellOptions{})
	for _, command := range []string{"printf 'first # $ ordinary output'", "printf 'second'; exit 0"} {
		out, err := tc.ExecuteChecked(command)
		want := "first # $ ordinary output"
		if strings.Contains(command, "second") {
			want = "second"
		}
		if err != nil || string(out) != want {
			t.Fatalf("got output %q, error %v; want %q", out, err, want)
		}
	}
}

func TestExecuteCheckedExitStatusAndDiagnostics(t *testing.T) {
	tc := realCheckedShell(t, testutil.ShellOptions{})
	for _, diagnostic := range []string{"Permission denied", "Read-only file system"} {
		out, err := tc.ExecuteChecked("printf '" + diagnostic + "' >&2; exit 23")
		if err == nil || !strings.Contains(err.Error(), "23") || !strings.Contains(err.Error(), diagnostic) {
			t.Fatalf("want exit status and diagnostic, got output %q, error %v", out, err)
		}
		if string(out) != diagnostic {
			t.Fatalf("diagnostic output lost: %q", out)
		}
	}
}

func TestExecuteCheckedOrdinaryMarkerLikeOutput(t *testing.T) {
	tc := realCheckedShell(t, testutil.ShellOptions{})
	want := "__busyscout_00000000000000000000000000000000__:0\r\nordinary:127"
	out, err := tc.ExecuteChecked("printf '__busyscout_00000000000000000000000000000000__:0\\nordinary:127'")
	if err != nil || string(out) != want {
		t.Fatalf("ordinary output interpreted as completion: %q, %v", out, err)
	}
}

// Scripted peers are reserved for impossible/malformed completion framing.
func scriptedCheckedShell(t *testing.T, reply func(net.Conn, string)) *TelnetClient {
	return scriptedCheckedEcho(t, func(request string) string { return request }, reply)
}

func scriptedCheckedEcho(t *testing.T, echo func(string) string, reply func(net.Conn, string)) *TelnetClient {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprint(conn, "/ # ")
		reader := bufio.NewReader(conn)
		var request []byte
		for !strings.HasSuffix(string(request), "\r\n") {
			b, err := reader.ReadByte()
			if err != nil {
				return
			}
			if b == IAC {
				reader.Discard(2)
				continue
			}
			request = append(request, b)
		}
		fmt.Fprint(conn, echo(string(request)))
		marker := regexp.MustCompile(`__busyscout_[a-f0-9]{32}__`).FindString(string(request))
		fmt.Fprintf(conn, "\x1e%s:B\x1f", marker)
		reply(conn, string(request))
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	tc := &TelnetClient{Address: host, Port: port, Timeout: 200 * time.Millisecond}
	if err := tc.Dial(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tc.Close)
	return tc
}

func TestExecuteCheckedMissingCompletion(t *testing.T) {
	tc := scriptedCheckedShell(t, func(conn net.Conn, request string) {
		fmt.Fprint(conn, "Permission denied\r\n/ # ")
	})
	out, err := tc.ExecuteChecked("false")
	if err == nil || !strings.Contains(err.Error(), "completion") || !strings.Contains(err.Error(), "Permission denied") {
		t.Fatalf("missing completion accepted or diagnostic lost: %q, %v", out, err)
	}
}

func TestExecuteCheckedMalformedCompletion(t *testing.T) {
	for _, status := range []string{"garbage", "", "-1", "256", "0junk"} {
		t.Run(status, func(t *testing.T) {
			tc := scriptedCheckedShell(t, func(conn net.Conn, request string) {
				marker := regexp.MustCompile(`__busyscout_[a-f0-9]{32}__`).FindString(request)
				fmt.Fprintf(conn, "diagnostic\x1e%s:%s\x1f\r\n/ # ", marker, status)
			})
			out, err := tc.ExecuteChecked("true")
			if err == nil || !strings.Contains(err.Error(), "completion") {
				t.Fatalf("malformed status accepted: %q, %v", out, err)
			}
		})
	}
}

func TestExecuteCheckedFreshMarkers(t *testing.T) {
	var requests []string
	var mu sync.Mutex
	tc := realCheckedShell(t, testutil.ShellOptions{OnCommand: func(command string) {
		mu.Lock()
		requests = append(requests, command)
		mu.Unlock()
	}})
	for i := 0; i < 2; i++ {
		if _, err := tc.ExecuteChecked("true"); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	re := regexp.MustCompile(`__busyscout_[a-f0-9]{32}__`)
	if len(requests) != 2 || re.FindString(requests[0]) == "" || re.FindString(requests[0]) == re.FindString(requests[1]) {
		t.Fatalf("completion markers are absent or reused: %q", requests)
	}
	for _, request := range requests {
		if strings.ContainsAny(request, "\r\n") {
			t.Fatalf("wrapper would trigger interactive continuation echo: %q", request)
		}
	}
}

func TestExecutePreservesWriteError(t *testing.T) {
	want := errors.New("injected write failure")
	conn := &writeFailureConn{writeErr: want}
	tc := &TelnetClient{conn: conn, reader: bufio.NewReader(conn), writer: bufio.NewWriter(conn), Timeout: time.Second}
	if _, err := tc.Execute("true"); !errors.Is(err, want) {
		t.Fatalf("write error discarded: %v", err)
	}
}

type writeFailureConn struct {
	net.Conn
	writeErr error
}

func (c *writeFailureConn) Write([]byte) (int, error) { return 0, c.writeErr }
func (c *writeFailureConn) Read([]byte) (int, error) {
	return 0, errors.New("unexpected read after failed write")
}
func (c *writeFailureConn) SetReadDeadline(time.Time) error { return nil }

func TestExecuteCheckedRejectsControlCommand(t *testing.T) {
	tc := realCheckedShell(t, testutil.ShellOptions{})
	for _, command := range []string{"true\nfalse", "true\rfalse", "true\x00false"} {
		if _, err := tc.ExecuteChecked(command); err == nil || !strings.Contains(err.Error(), "unsupported shell command") {
			t.Fatalf("want unsupported command error before transport, got %v", err)
		}
	}
}

func TestShellQuoteLiteralOperand(t *testing.T) {
	tc := realCheckedShell(t, testutil.ShellOptions{})
	for _, operand := range []string{"", "a 'quote' \\ $HOME ; [*]", " leading and trailing "} {
		out, err := tc.ExecuteChecked("printf '%s' " + ShellQuote(operand))
		if err != nil || string(out) != operand {
			t.Fatalf("literal operand changed: %q, %v; want %q", out, err, operand)
		}
	}
}

func TestExecuteCheckedIgnoresVariableTerminalEcho(t *testing.T) {
	for _, tt := range []struct {
		name   string
		echo   func(string) string
		ending string
	}{
		{"wrapped ANSI CRCRLF", func(s string) string { return "\x1b[0m" + strings.ReplaceAll(s, " ", " \r\r\n\x1b[1G") }, "\r\n"},
		{"CR echo", func(s string) string { return strings.ReplaceAll(s, " ", " \r") }, "\r\n"},
		{"short echo", func(string) string { return "short\r\n" }, "\r\n"},
		{"no echo LF", func(string) string { return "" }, "\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tc := scriptedCheckedEcho(t, tt.echo, func(conn net.Conn, request string) {
				marker := regexp.MustCompile(`__busyscout_[a-f0-9]{32}__`).FindString(request)
				fmt.Fprintf(conn, "actual output\x1e%s:0\x1f%s/ # ", marker, tt.ending)
			})
			out, err := tc.ExecuteChecked("printf 'actual output'; : #" + strings.Repeat("padding", 80))
			if err != nil || string(out) != "actual output" {
				t.Fatalf("terminal echo corrupted actual output: %q, %v", out, err)
			}
		})
	}
}
