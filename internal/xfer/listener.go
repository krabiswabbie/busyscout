package xfer

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

const (
	typePush  byte = 0x01
	typePull  byte = 0x02
	typeData  byte = 0x03
	typeError byte = 0x04
	typeOK    byte = 0x05
)

// StartListener binds a TCP listener on an ephemeral port (port 0) on all interfaces.
// Binding to ":0" (all interfaces) allows connections from both localhost (127.0.0.1)
// and remote devices (LAN IP), which is required for device fileloader connections.
func StartListener() (int, net.Listener, error) {
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		return 0, nil, err
	}
	return ln.Addr().(*net.TCPAddr).Port, ln, nil
}

// AcceptAndPush accepts one connection and sends a PUSH frame with the file contents.
func AcceptAndPush(ln net.Listener, localPath string) error {
	// Set deadline for the device fileloader to connect
	if tl, ok := ln.(interface{ SetDeadline(time.Time) error }); ok {
		tl.SetDeadline(time.Now().Add(15 * time.Second))
	}

	conn, err := ln.Accept()
	if err != nil {
		return &SetupError{fmt.Errorf("fileloader did not connect back: %w", err)}
	}
	defer conn.Close()

	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	return pushConnection(conn, localPath, data, 30*time.Second, 15*time.Second)
}

// deadlineWriter bounds each inactive write, rather than imposing a total
// transfer deadline. A progressing large transfer may take arbitrarily long.
type deadlineWriter struct {
	conn    net.Conn
	timeout time.Duration
}

func (w deadlineWriter) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		if err := w.conn.SetWriteDeadline(time.Now().Add(w.timeout)); err != nil {
			return total, err
		}
		chunk := p
		if len(chunk) > 64*1024 {
			chunk = chunk[:64*1024]
		}
		n, err := w.conn.Write(chunk)
		total += n
		p = p[n:]
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

func pushConnection(conn net.Conn, name string, data []byte, inactivity, acknowledgement time.Duration) error {
	// Read concurrently: a refusal may arrive before a payload larger than TCP
	// buffers finishes writing. Waiting until after Write would lose its cause.
	response := make(chan error, 1)
	go func() { response <- readPushCompletion(conn, acknowledgement) }()
	writeDone := make(chan error, 1)
	go func() {
		w := deadlineWriter{conn, inactivity}
		var header []byte
		header = append(header, typePush)
		header = binary.BigEndian.AppendUint32(header, uint32(len(name)))
		header = append(header, name...)
		header = binary.BigEndian.AppendUint64(header, uint64(len(data)))
		if _, err := io.Copy(w, bytes.NewReader(header)); err != nil {
			writeDone <- fmt.Errorf("write header: %w", err)
			return
		}
		if _, err := io.Copy(w, bytes.NewReader(data)); err != nil {
			writeDone <- fmt.Errorf("write data: %w", err)
			return
		}
		writeDone <- nil
	}()
	select {
	case replyErr := <-response:
		// Closing also stops a writer currently blocked on a full TCP buffer. Wait
		// for it before returning so this transfer owns all its goroutines.
		conn.Close()
		writeErr := <-writeDone
		if replyErr != nil {
			return replyErr
		}
		if writeErr != nil {
			return writeErr
		}
		return nil
	case writeErr := <-writeDone:
		conn.SetReadDeadline(time.Now().Add(acknowledgement))
		replyErr := <-response
		if replyErr != nil {
			return replyErr
		}
		return writeErr
	}
}

func readRemoteError(conn net.Conn) error {
	var size uint32
	if err := binary.Read(conn, binary.BigEndian, &size); err != nil {
		return fmt.Errorf("read error length: %w", err)
	}
	if size > 4096 {
		return fmt.Errorf("remote error exceeds 4096 bytes: %d", size)
	}
	msg := make([]byte, size)
	if _, err := io.ReadFull(conn, msg); err != nil {
		return fmt.Errorf("read error message: %w", err)
	}
	return fmt.Errorf("remote error: %s", msg)
}

func readPushCompletion(conn net.Conn, acknowledgement time.Duration) error {
	var typ [1]byte
	if _, err := io.ReadFull(conn, typ[:]); err != nil {
		return fmt.Errorf("read push completion: %w", err)
	}
	switch typ[0] {
	case typeOK:
		return nil
	case typeError:
		conn.SetReadDeadline(time.Now().Add(acknowledgement))
		return readRemoteError(conn)
	default:
		return fmt.Errorf("expected push completion OK (0x05), got 0x%02x", typ[0])
	}
}

// AcceptAndPull accepts one connection and receives a file from the device.
// The fileloader on the device sends: TYPE_PULL (announcement) + TYPE_DATA (file contents).
// localPath: where to write the received file on BusyScout host.
func AcceptAndPull(ln net.Listener, localPath string) error {
	// Set deadline for the device fileloader to connect
	if tl, ok := ln.(interface{ SetDeadline(time.Time) error }); ok {
		tl.SetDeadline(time.Now().Add(15 * time.Second))
	}

	conn, err := ln.Accept()
	if err != nil {
		return &SetupError{fmt.Errorf("fileloader did not connect back: %w", err)}
	}
	defer conn.Close()

	// Read TYPE_PULL announcement (0x02)
	var typ [1]byte
	if _, err := io.ReadFull(conn, typ[:]); err != nil {
		return fmt.Errorf("read type: %w", err)
	}

	if typ[0] == typeError {
		return readRemoteError(conn)
	}

	if typ[0] != typePull {
		return fmt.Errorf("expected PULL type (0x02), got 0x%02x", typ[0])
	}

	// Read namelen + filename (consume it)
	var namelen uint32
	if err := binary.Read(conn, binary.BigEndian, &namelen); err != nil {
		return fmt.Errorf("read namelen: %w", err)
	}
	fname := make([]byte, namelen)
	if _, err := io.ReadFull(conn, fname); err != nil {
		return fmt.Errorf("read filename: %w", err)
	}

	// Read TYPE_DATA (0x03) — file contents follow
	if _, err := io.ReadFull(conn, typ[:]); err != nil {
		return fmt.Errorf("read data type: %w", err)
	}
	if typ[0] != typeData {
		return fmt.Errorf("expected DATA type (0x03), got 0x%02x", typ[0])
	}

	// Read filesize
	var filesize uint64
	if err := binary.Read(conn, binary.BigEndian, &filesize); err != nil {
		return fmt.Errorf("read filesize: %w", err)
	}

	// Read file data
	data := make([]byte, filesize)
	if _, err := io.ReadFull(conn, data); err != nil {
		return fmt.Errorf("read data: %w", err)
	}

	// Write to local file
	if err := os.WriteFile(localPath, data, 0644); err != nil {
		return fmt.Errorf("write local file: %w", err)
	}

	return nil
}
