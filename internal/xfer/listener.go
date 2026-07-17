package xfer

import (
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
	if tl, ok := ln.(*net.TCPListener); ok {
		tl.SetDeadline(time.Now().Add(15 * time.Second))
	}

	conn, err := ln.Accept()
	if err != nil {
		return fmt.Errorf("accept: %w", err)
	}
	defer conn.Close()

	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	// Send type
	if _, err := conn.Write([]byte{typePush}); err != nil {
		return fmt.Errorf("write type: %w", err)
	}

	// Send namelen + filename
	name := []byte(localPath)
	if err := binary.Write(conn, binary.BigEndian, uint32(len(name))); err != nil {
		return fmt.Errorf("write namelen: %w", err)
	}
	if _, err := conn.Write(name); err != nil {
		return fmt.Errorf("write filename: %w", err)
	}

	// Send filesize
	if err := binary.Write(conn, binary.BigEndian, uint64(len(data))); err != nil {
		return fmt.Errorf("write filesize: %w", err)
	}

	// Send data
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("write data: %w", err)
	}

	return nil
}

// AcceptAndPull accepts one connection and receives a file from the device.
// The fileloader on the device sends: TYPE_PULL (announcement) + TYPE_DATA (file contents).
// localPath: where to write the received file on BusyScout host.
func AcceptAndPull(ln net.Listener, localPath string) error {
	// Set deadline for the device fileloader to connect
	if tl, ok := ln.(*net.TCPListener); ok {
		tl.SetDeadline(time.Now().Add(15 * time.Second))
	}

	conn, err := ln.Accept()
	if err != nil {
		return fmt.Errorf("accept: %w", err)
	}
	defer conn.Close()

	// Read TYPE_PULL announcement (0x02)
	var typ [1]byte
	if _, err := io.ReadFull(conn, typ[:]); err != nil {
		return fmt.Errorf("read type: %w", err)
	}

	if typ[0] == typeError {
		// Fileloader couldn't open the file — read error message
		var msglen uint32
		if err := binary.Read(conn, binary.BigEndian, &msglen); err != nil {
			return fmt.Errorf("read error msglen: %w", err)
		}
		msg := make([]byte, msglen)
		if _, err := io.ReadFull(conn, msg); err != nil {
			return fmt.Errorf("read error msg: %w", err)
		}
		return fmt.Errorf("remote error: %s", string(msg))
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
