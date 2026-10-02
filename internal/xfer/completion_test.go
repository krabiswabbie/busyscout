package xfer

import (
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func consumePushHeader(conn net.Conn) (uint64, error) {
	var typ [1]byte
	if _, err := io.ReadFull(conn, typ[:]); err != nil {
		return 0, err
	}
	var n uint32
	if err := binary.Read(conn, binary.BigEndian, &n); err != nil {
		return 0, err
	}
	if _, err := io.CopyN(io.Discard, conn, int64(n)); err != nil {
		return 0, err
	}
	var size uint64
	err := binary.Read(conn, binary.BigEndian, &size)
	return size, err
}

func TestPushRequiresCompletion(t *testing.T) {
	// Removing the reply read must fail every error case, including old helpers that close without acknowledging.
	for _, tt := range []struct {
		name       string
		reply      []byte
		diagnostic string
	}{
		{"ack", []byte{5}, ""},
		{"EOF", nil, "completion"},
		{"wrong type", []byte{3}, "0x03"},
		{"remote error", append([]byte{4, 0, 0, 0, 17}, []byte("write: disk full!")...), "disk full"},
		{"oversized error", []byte{4, 0, 0, 16, 1}, "4096"},
		{"truncated length", []byte{4, 0, 0}, "error length"},
		{"truncated message", []byte{4, 0, 0, 0, 9, 'b'}, "error message"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "payload")
			if err := os.WriteFile(src, []byte("abcd"), 0600); err != nil {
				t.Fatal(err)
			}
			port, ln, err := StartListener()
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			peerDone := make(chan error, 1)
			go func() {
				conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
				if err != nil {
					peerDone <- err
					return
				}
				defer conn.Close()
				size, err := consumePushHeader(conn)
				if err == nil {
					_, err = io.CopyN(io.Discard, conn, int64(size))
				}
				if err == nil && len(tt.reply) > 0 {
					_, err = conn.Write(tt.reply)
				}
				peerDone <- err
			}()
			err = AcceptAndPush(ln, src)
			if tt.diagnostic == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.diagnostic) {
				t.Fatalf("want %q, got %v", tt.diagnostic, err)
			}
			if isSetupError(err) {
				t.Fatalf("connected transfer failure may not trigger fallback: %v", err)
			}
			if err := <-peerDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPushPreservesEarlyRemoteErrorWithPendingPayload(t *testing.T) {
	// A sequential write-then-read loses a refusal when the payload exceeds TCP buffers.
	src := filepath.Join(t.TempDir(), "large")
	if err := os.WriteFile(src, make([]byte, 16<<20), 0600); err != nil {
		t.Fatal(err)
	}
	port, ln, err := StartListener()
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		_, err = consumePushHeader(conn)
		if err == nil {
			msg := []byte("open output: Read-only file system")
			_, err = conn.Write(append([]byte{4, 0, 0, 0, byte(len(msg))}, msg...))
		}
		// Keep a pending sender blocked briefly; do not consume file data.
		time.Sleep(100 * time.Millisecond)
		done <- err
	}()
	err = AcceptAndPush(ln, src)
	if err == nil || !strings.Contains(err.Error(), "Read-only file system") || isSetupError(err) {
		t.Fatalf("lost remote refusal or enabled fallback: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPushLargeProgressAndCompletionTimeouts(t *testing.T) {
	for _, tt := range []struct {
		name                string
		size                int
		stallAck, stallData bool
	}{
		{"large progressing transfer", 256 << 10, false, false},
		{"stalled acknowledgement", 4, true, false},
		{"stalled data", 16 << 20, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sender, peer := net.Pipe()
			defer sender.Close()
			defer peer.Close()
			done := make(chan error, 1)
			go func() {
				if tt.stallData {
					return
				}
				size, err := consumePushHeader(peer)
				if err == nil {
					buf := make([]byte, 64<<10)
					for size > 0 {
						n := uint64(len(buf))
						if n > size {
							n = size
						}
						_, err = io.ReadFull(peer, buf[:n])
						if err != nil {
							break
						}
						size -= n
						time.Sleep(15 * time.Millisecond)
					}
				}
				if err == nil && !tt.stallAck {
					_, err = peer.Write([]byte{5})
				}
				done <- err
			}()
			start := time.Now()
			err := pushConnection(sender, "payload", make([]byte, tt.size), 40*time.Millisecond, 40*time.Millisecond)
			if tt.stallData || tt.stallAck {
				if err == nil || isSetupError(err) {
					t.Fatalf("stall must fail without fallback: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
			}
			if time.Since(start) > time.Second {
				t.Fatal("operation exceeded injected timeout")
			}
			if !tt.stallData {
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestPushStalledErrorMetadataIsBounded(t *testing.T) {
	// A peer that sends a type/partial message but never finishes must not hold
	// the completion reader indefinitely.
	for _, reply := range [][]byte{{4}, {4, 0, 0, 0, 8, 'x'}} {
		sender, peer := net.Pipe()
		defer sender.Close()
		defer peer.Close()
		go func() {
			size, err := consumePushHeader(peer)
			if err != nil {
				return
			}
			io.CopyN(io.Discard, peer, int64(size))
			peer.Write(reply)
		}()
		start := time.Now()
		err := pushConnection(sender, "x", []byte("abcd"), 40*time.Millisecond, 40*time.Millisecond)
		if err == nil || isSetupError(err) || time.Since(start) > time.Second {
			t.Fatalf("metadata stall not bounded: %v", err)
		}
	}
}

func TestPushEarlyIncompleteErrorDoesNotWaitForProgressingPayload(t *testing.T) {
	sender, peer := net.Pipe()
	defer sender.Close()
	defer peer.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		size, err := consumePushHeader(peer)
		if err != nil {
			return
		}
		if _, err = peer.Write([]byte{4}); err != nil {
			return
		}
		buf := make([]byte, 64<<10)
		for size > 0 {
			n := uint64(len(buf))
			if n > size {
				n = size
			}
			if _, err := io.ReadFull(peer, buf[:n]); err != nil {
				return
			}
			size -= n
			time.Sleep(30 * time.Millisecond)
		}
	}()
	start := time.Now()
	err := pushConnection(sender, "x", make([]byte, 1<<20), 100*time.Millisecond, 40*time.Millisecond)
	sender.Close()
	<-done
	if err == nil || !strings.Contains(err.Error(), "error length") || time.Since(start) > 200*time.Millisecond {
		t.Fatalf("early error metadata waited for progressing payload: %v, elapsed %v", err, time.Since(start))
	}
}
