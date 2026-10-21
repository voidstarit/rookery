package host

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.fd.io/govpp/extras/gomemif/memif"
)

func TestMemifLinkLoopback(t *testing.T) {
	const interfaceID = 0x8000000c // shared network memif ids use the high bit
	socketPath := filepath.Join(t.TempDir(), "memif.sock")
	socket, err := memif.NewSocket("rookery-test-master", socketPath)
	if err != nil {
		t.Fatalf("create master socket: %v", err)
	}
	defer socket.Delete()

	master, err := socket.NewInterface(&memif.Arguments{
		Id:               interfaceID,
		IsMaster:         true,
		Name:             "master",
		ConnectedFunc:    func(*memif.Interface) error { return nil },
		DisconnectedFunc: func(*memif.Interface) error { return nil },
	})
	if err != nil {
		t.Fatalf("create master interface: %v", err)
	}
	errCh := make(chan error, 16)
	socket.StartPolling(errCh)
	defer socket.StopPolling()

	link, err := newMemifLink(socketPath, interfaceID, "slave", nil)
	if err != nil {
		t.Fatalf("create slave link: %v", err)
	}
	defer link.close()

	deadline := time.Now().Add(2 * time.Second)
	for !master.IsConnected() {
		if time.Now().After(deadline) {
			t.Fatal("master memif did not connect")
		}
		time.Sleep(10 * time.Millisecond)
	}

	tx, err := master.GetTxQueue(0)
	if err != nil {
		t.Fatalf("master tx queue: %v", err)
	}
	rx, err := master.GetRxQueue(0)
	if err != nil {
		t.Fatalf("master rx queue: %v", err)
	}

	payload := []byte{0x02, 0, 0, 0, 0, 1, 0x02, 0, 0, 0, 0, 2, 0x08, 0x00, 0xde, 0xad}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	readCh := make(chan []byte, 1)
	readErr := make(chan error, 1)
	go func() {
		frame, err := link.readFrame(ctx)
		if err != nil {
			readErr <- err
			return
		}
		readCh <- frame
	}()

	deadline = time.Now().Add(2 * time.Second)
	for {
		if n := tx.WritePacket(payload); n == len(payload) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("master could not write to memif")
		}
		time.Sleep(time.Millisecond)
	}

	var got []byte
	select {
	case got = <-readCh:
	case err := <-readErr:
		t.Fatalf("slave read: %v", err)
	case <-ctx.Done():
		t.Fatal("timed out waiting for slave frame")
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("slave frame = %x, want %x", got, payload)
	}

	if err := link.writeFrame(payload); err != nil {
		t.Fatalf("slave write: %v", err)
	}
	deadline = time.Now().Add(2 * time.Second)
	buf := make([]byte, 2048)
	for {
		n, err := rx.ReadPacket(buf)
		if err != nil {
			t.Fatalf("master read: %v", err)
		}
		if n > 0 {
			if !bytes.Equal(buf[:n], payload) {
				t.Fatalf("master frame = %x, want %x", buf[:n], payload)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for slave frame")
		}
		time.Sleep(time.Millisecond)
	}
}
