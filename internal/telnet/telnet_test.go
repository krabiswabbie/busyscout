package telnet

import (
	"bufio"
	"net"
	"testing"
	"time"
)

// fakeShell emulates a BusyBox shell behind telnetd: it prints a prompt, echoes
// the typed line and prints the command output. With askTerminal set it also
// writes ESC[6n after every prompt, the way BusyBox built with
// FEATURE_EDITING_ASK_TERMINAL does.
type fakeShell struct {
	askTerminal bool
	// askDelay separates ESC[6n from the prompt, so it lands in a later read
	askDelay time.Duration
	// askSplit sends ESC together with the prompt and "[6n" after askDelay
	askSplit bool
	outputs  map[string]string
}

const fakePrompt = "/ # "

func (f *fakeShell) start(t *testing.T) (host, port string) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.session(conn)
		}
	}()

	host, port, _ = net.SplitHostPort(ln.Addr().String())
	return host, port
}

func (f *fakeShell) prompt(conn net.Conn) {
	switch {
	case f.askTerminal && f.askSplit:
		conn.Write([]byte(fakePrompt + "\x1b"))
		time.Sleep(f.askDelay)
		conn.Write([]byte("[6n"))
	case f.askTerminal:
		conn.Write([]byte(fakePrompt))
		time.Sleep(f.askDelay)
		conn.Write([]byte("\x1b[6n"))
	default:
		conn.Write([]byte(fakePrompt))
	}
}

func (f *fakeShell) session(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)

	f.prompt(conn)
	for {
		var line []byte
		for {
			b, err := reader.ReadByte()
			if err != nil {
				return
			}
			if b == IAC {
				// Option negotiation sent by the client: IAC <verb> <option>
				reader.Discard(2)
				continue
			}
			if b == '\n' {
				break
			}
			line = append(line, b)
		}

		// Echo of the typed line, exactly as it was sent
		conn.Write(append(line, '\n'))

		// The client sends "<name> <args>\r\n"
		cmd := string(line[:len(line)-1])
		if len(cmd) > 0 && cmd[len(cmd)-1] == ' ' {
			cmd = cmd[:len(cmd)-1]
		}
		conn.Write([]byte(f.outputs[cmd]))
		f.prompt(conn)
	}
}

func dialFakeShell(t *testing.T, f *fakeShell) *TelnetClient {
	host, port := f.start(t)
	tc := &TelnetClient{Address: host, Port: port, Timeout: 2 * time.Second}
	if err := tc.Dial(); err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(tc.Close)
	return tc
}

var fakeOutputs = map[string]string{
	"uname -m": "armv7l\r\n",
	"uname -a": "Linux (none) 7.1.0-rc5 armv7l Linux\r\n",
}

func checkUname(t *testing.T, tc *TelnetClient) {
	// Several commands on one connection: every prompt is followed by ESC[6n
	steps := []struct {
		args []string
		want string
	}{
		{[]string{"-m"}, "armv7l"},
		{[]string{"-a"}, "Linux (none) 7.1.0-rc5 armv7l Linux"},
		{[]string{"-m"}, "armv7l"},
	}
	for i, step := range steps {
		out, err := tc.Execute("uname", step.args...)
		if err != nil {
			t.Fatalf("command %d: Execute: %v", i, err)
		}
		if string(out) != step.want {
			t.Errorf("command %d: got %q, want %q", i, out, step.want)
		}
	}
}

func TestExecute_PlainShell(t *testing.T) {
	tc := dialFakeShell(t, &fakeShell{outputs: fakeOutputs})
	checkUname(t, tc)
}

func TestExecute_AskTerminalWithPrompt(t *testing.T) {
	tc := dialFakeShell(t, &fakeShell{askTerminal: true, outputs: fakeOutputs})
	checkUname(t, tc)
}

// Issue #10: ESC[6n arrives after the prompt has already been read, so it
// used to be counted as the beginning of the next command's echo.
func TestExecute_AskTerminalAfterPrompt(t *testing.T) {
	tc := dialFakeShell(t, &fakeShell{askTerminal: true, askDelay: 40 * time.Millisecond, outputs: fakeOutputs})
	checkUname(t, tc)
}

func TestExecute_AskTerminalSplitBetweenReads(t *testing.T) {
	tc := dialFakeShell(t, &fakeShell{askTerminal: true, askSplit: true, askDelay: 40 * time.Millisecond, outputs: fakeOutputs})
	checkUname(t, tc)
}
