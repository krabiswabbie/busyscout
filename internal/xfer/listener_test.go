package xfer

import (
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func itoa(n int) string { return strconv.Itoa(n) }

func TestAcceptAndPush(t *testing.T) {
	// Create temp file with known content
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "push_src.bin")
	want := []byte("hello from push test")
	if err := os.WriteFile(srcPath, want, 0644); err != nil {
		t.Fatal(err)
	}

	port, ln, err := StartListener()
	if err != nil {
		t.Fatal(err)
	}

	// Start goroutine to accept and push
	errCh := make(chan error, 1)
	go func() {
		errCh <- AcceptAndPush(ln, srcPath)
	}()

	// Connect as fileloader client and read framed data
	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Read type
	var typ [1]byte
	if _, err := io.ReadFull(conn, typ[:]); err != nil {
		t.Fatal(err)
	}
	if typ[0] != 0x01 {
		t.Fatalf("expected type 0x01 (PUSH), got 0x%02x", typ[0])
	}

	// Read namelen (4 bytes BE)
	var namelen uint32
	if err := binary.Read(conn, binary.BigEndian, &namelen); err != nil {
		t.Fatal(err)
	}

	// Read filename
	fname := make([]byte, namelen)
	if _, err := io.ReadFull(conn, fname); err != nil {
		t.Fatal(err)
	}

	// Read filesize (8 bytes BE)
	var filesize uint64
	if err := binary.Read(conn, binary.BigEndian, &filesize); err != nil {
		t.Fatal(err)
	}
	if filesize != uint64(len(want)) {
		t.Fatalf("filesize: want %d, got %d", len(want), filesize)
	}

	// Read data
	got := make([]byte, filesize)
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}

	if string(got) != string(want) {
		t.Fatalf("data mismatch: want %q, got %q", want, got)
	}

	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}

func TestAcceptAndPull(t *testing.T) {
	dir := t.TempDir()
	srcData := []byte("hello from pull test — pulled FROM device")

	port, ln, err := StartListener()
	if err != nil {
		t.Fatal(err)
	}

	dstPath := filepath.Join(dir, "pull_dst.bin")

	errCh := make(chan error, 1)
	go func() {
		errCh <- AcceptAndPull(ln, dstPath)
	}()

	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Send TYPE_PULL announcement (as fileloader would)
	if _, err := conn.Write([]byte{0x02}); err != nil { // TYPE_PULL
		t.Fatal(err)
	}
	reqName := "pull_src.bin"
	if err := binary.Write(conn, binary.BigEndian, uint32(len(reqName))); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte(reqName)); err != nil {
		t.Fatal(err)
	}

	// Send TYPE_DATA with file contents
	if _, err := conn.Write([]byte{0x03}); err != nil { // TYPE_DATA
		t.Fatal(err)
	}
	if err := binary.Write(conn, binary.BigEndian, uint64(len(srcData))); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(srcData); err != nil {
		t.Fatal(err)
	}

	if err := <-errCh; err != nil {
		t.Fatal(err)
	}

	// Verify dst file was written by AcceptAndPull
	dstData, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(dstData) != string(srcData) {
		t.Fatalf("dst file mismatch: want %q, got %q", srcData, dstData)
	}
}

func TestAcceptAndPull_ErrorResponse(t *testing.T) {
	dir := t.TempDir()
	dstPath := filepath.Join(dir, "pull_dst.bin")

	port, ln, err := StartListener()
	if err != nil {
		t.Fatal(err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- AcceptAndPull(ln, dstPath)
	}()

	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Send TYPE_ERROR — file not found on device
	if _, err := conn.Write([]byte{0x04}); err != nil { // TYPE_ERROR
		t.Fatal(err)
	}
	errMsg := "no such file /etc/shadow"
	if err := binary.Write(conn, binary.BigEndian, uint32(len(errMsg))); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte(errMsg)); err != nil {
		t.Fatal(err)
	}

	// AcceptAndPull should return an error
	if err := <-errCh; err == nil {
		t.Fatal("expected error from AcceptAndPull for missing file")
	}
}
