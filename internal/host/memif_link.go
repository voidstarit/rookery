package host

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"go.fd.io/govpp/extras/gomemif/memif"
)

var errMemifClosed = errors.New("memif interface closed")

type memifLink struct {
	socket *memif.Socket
	iface  *memif.Interface
	rx     *memif.Queue
	tx     *memif.Queue

	closed    chan struct{}
	closeOnce sync.Once
}

func newMemifLink(socketPath string, ifID uint32, name string, _ net.HardwareAddr) (*memifLink, error) {
	socket, err := memif.NewSocket("rookery-vnf", socketPath)
	if err != nil {
		return nil, fmt.Errorf("create memif socket: %w", err)
	}

	iface, err := socket.NewInterface(&memif.Arguments{
		Id:       ifID,
		IsMaster: false,
		Name:     name,
		ConnectedFunc: func(*memif.Interface) error {
			log.Info().Str("socket", socketPath).Uint32("interface_id", ifID).Str("name", name).Msg("memif slave connected")
			return nil
		},
		DisconnectedFunc: func(*memif.Interface) error {
			log.Warn().Str("socket", socketPath).Uint32("interface_id", ifID).Str("name", name).Msg("memif slave disconnected")
			return nil
		},
		MemoryConfig: memif.MemoryConfig{
			NumQueuePairs:    1,
			Log2RingSize:     10,
			PacketBufferSize: memifDHCPBufferSize,
		},
	})
	if err != nil {
		_ = socket.Delete()
		return nil, fmt.Errorf("create memif interface: %w", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for !iface.IsConnecting() {
		if err := iface.RequestConnection(); err != nil {
			if time.Now().After(deadline) {
				_ = socket.Delete()
				return nil, fmt.Errorf("connect to memif socket %s: %w", socketPath, err)
			}
			select {
			case <-time.After(50 * time.Millisecond):
			}
			continue
		}
	}

	// The socket must be polled for the control-channel handshake and later
	// disconnect events. Drain errors so a broken peer cannot block polling.
	errCh := make(chan error, 16)
	socket.StartPolling(errCh)
	link := &memifLink{
		socket: socket,
		iface:  iface,
		closed: make(chan struct{}),
	}
	go link.drainErrors(errCh)

	for !iface.IsConnected() {
		if time.Now().After(deadline) {
			link.close()
			return nil, fmt.Errorf("memif interface %s did not connect within 5s", socketPath)
		}
		select {
		case <-time.After(20 * time.Millisecond):
		}
	}

	rx, err := iface.GetRxQueue(0)
	if err != nil {
		link.close()
		return nil, fmt.Errorf("get memif rx queue: %w", err)
	}
	tx, err := iface.GetTxQueue(0)
	if err != nil {
		link.close()
		return nil, fmt.Errorf("get memif tx queue: %w", err)
	}
	link.rx = rx
	link.tx = tx
	return link, nil
}

func (l *memifLink) drainErrors(errCh <-chan error) {
	for {
		select {
		case <-l.closed:
			return
		case err, ok := <-errCh:
			if !ok {
				return
			}
			log.Warn().Err(err).Msg("memif control channel error")
		}
	}
}

func (l *memifLink) readFrame(ctx context.Context) ([]byte, error) {
	buf := make([]byte, memifDHCPBufferSize)
	for {
		select {
		case <-ctx.Done():
			return nil, errMemifClosed
		case <-l.closed:
			return nil, errMemifClosed
		default:
		}
		if !l.iface.IsConnected() {
			return nil, errMemifClosed
		}
		n, err := l.rx.ReadPacket(buf)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			frame := make([]byte, n)
			copy(frame, buf[:n])
			return frame, nil
		}
		// memif has no blocking read; poll at a low rate. DHCP traffic is
		// request/response, so this is not a hot path.
		select {
		case <-ctx.Done():
			return nil, errMemifClosed
		case <-l.closed:
			return nil, errMemifClosed
		case <-time.After(time.Millisecond):
		}
	}
}

func (l *memifLink) writeFrame(frame []byte) error {
	select {
	case <-l.closed:
		return errMemifClosed
	default:
	}
	if !l.iface.IsConnected() {
		return errMemifClosed
	}
	for attempts := 0; attempts < 100; attempts++ {
		if n := l.tx.WritePacket(frame); n > 0 {
			return nil
		}
		select {
		case <-l.closed:
			return errMemifClosed
		case <-time.After(100 * time.Microsecond):
		}
	}
	return errors.New("memif tx queue is full")
}

func (l *memifLink) close() {
	l.closeOnce.Do(func() {
		close(l.closed)
		if l.iface != nil {
			_ = l.iface.Delete()
		}
		if l.socket != nil {
			_ = l.socket.StopPolling()
			_ = l.socket.Delete()
		}
	})
}
