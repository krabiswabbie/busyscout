// Package testutil provides local shell fixtures for integration tests.
package testutil

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"testing"
)

// ShellOptions configures a persistent shell behind a local Telnet connection.
// Init is shell source run once per connection, before commands from the client.
type ShellOptions struct {
	Dir  string
	Env  []string
	Init string
	// OnCommand observes requests before the real shell executes them.
	OnCommand func(string)
}

// StartShell starts an isolated, real BusyBox shell (or /bin/sh if unavailable).
// It echoes requests and emits CRLF and prompts like a shell behind telnetd.
// All connections and child processes are closed when the test ends.
func StartShell(t *testing.T, options ShellOptions) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	connections := make(map[net.Conn]bool)
	var sessions sync.WaitGroup
	acceptDone := make(chan struct{})
	t.Cleanup(func() {
		ln.Close()
		<-acceptDone
		mu.Lock()
		for conn := range connections {
			conn.Close()
		}
		mu.Unlock()
		sessions.Wait()
	})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections[conn] = true
			mu.Unlock()
			sessions.Add(1)
			go func() {
				defer sessions.Done()
				defer conn.Close()
				defer func() {
					mu.Lock()
					delete(connections, conn)
					mu.Unlock()
				}()
				shellSession(conn, options)
			}()
		}
	}()
	return ln.Addr().String()
}

func shellSession(conn net.Conn, options ShellOptions) {
	cmd := exec.Command("/bin/sh")
	if busybox, err := exec.LookPath("busybox"); err == nil {
		cmd = exec.Command(busybox, "sh")
	}
	cmd.Dir = options.Dir
	cmd.Env = append(os.Environ(), options.Env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return
	}
	output := &crlfWriter{writer: conn}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		return
	}
	defer func() {
		stdin.Close()
		cmd.Process.Kill()
		cmd.Wait()
	}()
	io.WriteString(stdin, options.Init+"\nprintf '\\n/ # '\n")
	reader := bufio.NewReader(conn)
	for {
		var request []byte
		for !bytes.HasSuffix(request, []byte("\r\n")) {
			b, err := reader.ReadByte()
			if err != nil {
				return
			}
			if b == 255 {
				if _, err := reader.Discard(2); err != nil {
					return
				}
				continue
			}
			request = append(request, b)
		}
		if _, err := conn.Write(request); err != nil {
			return
		}
		command := request[:len(request)-2]
		if options.OnCommand != nil {
			options.OnCommand(string(command))
		}
		if _, err := stdin.Write(append(command, []byte("\nprintf '\\n/ # '\n")...)); err != nil {
			return
		}
	}
}

type crlfWriter struct {
	writer io.Writer
}

func (w *crlfWriter) Write(data []byte) (int, error) {
	_, err := w.writer.Write(bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n")))
	if err != nil {
		return 0, err
	}
	return len(data), nil
}
