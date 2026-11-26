package api

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"

	"github.com/voidstarit/rookery/internal/store"
)

const vncDialTimeout = 3 * time.Second

// consoleAccessCheckInterval controls how quickly an open console is closed
// after an administrator disables console access.
const consoleAccessCheckInterval = 1 * time.Second

// handleVNCProxy validates the VM, console access and request origin, dials the
// VM's VNC Unix socket, and upgrades the request to a WebSocket proxy. It maps
// missing VMs to 404, disabled or cross-origin access to 403, stopped VMs to
// 409, and an unreachable socket to 502.
func (s *Server) handleVNCProxy(w http.ResponseWriter, r *http.Request) {
	id, err := parseVMID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.vms == nil {
		writeError(w, http.StatusServiceUnavailable, "vm runtime is not configured")
		return
	}

	vm, err := s.store.GetVM(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, "vm not found")
		default:
			s.internalError(w, r, err)
		}
		return
	}
	if !vm.ConsoleEnabled {
		writeError(w, http.StatusForbidden, "console access is disabled for this VM")
		return
	}
	if !s.vms.Running(id) {
		writeError(w, http.StatusConflict, "vm is not running")
		return
	}
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "websocket origin not allowed")
		return
	}

	socket := s.vms.VNCSocket(id)
	if socket == "" {
		writeError(w, http.StatusInternalServerError, "vnc socket is unavailable")
		return
	}
	conn, err := net.DialTimeout("unix", socket, vncDialTimeout)
	if err != nil {
		s.logger.Error().Str("vm_id", id).Str("socket", socket).Err(err).Msg("connect vnc socket failed")
		writeError(w, http.StatusBadGateway, "vnc endpoint is unavailable")
		return
	}
	defer conn.Close()

	websocket.Server{
		Handler: func(ws *websocket.Conn) {
			ws.PayloadType = websocket.BinaryFrame
			s.proxyVNC(ws, conn, id)
		},
		Handshake: func(_ *websocket.Config, req *http.Request) error {
			if !sameOrigin(req) {
				return errors.New("origin not allowed")
			}
			return nil
		},
	}.ServeHTTP(w, r)
}

// proxyVNC copies bytes in both directions between ws and conn and polls the
// store once per second, closing both sides when the VM disappears or console
// access is disabled.
func (s *Server) proxyVNC(ws *websocket.Conn, conn net.Conn, vmID string) {
	done := make(chan error, 2)
	stop := make(chan struct{})
	var closeOnce sync.Once
	closeBoth := func() {
		closeOnce.Do(func() {
			_ = conn.Close()
			_ = ws.Close()
		})
	}
	defer close(stop)

	// Close an active console shortly after the administrator disables access.
	go func() {
		ticker := time.NewTicker(consoleAccessCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				vm, err := s.store.GetVM(ctx, vmID)
				cancel()
				if err != nil || !vm.ConsoleEnabled {
					closeBoth()
					return
				}
			}
		}
	}()

	go func() {
		_, err := io.Copy(conn, ws)
		done <- err
	}()
	go func() {
		_, err := io.Copy(ws, conn)
		done <- err
	}()

	<-done
	closeBoth()
	<-done
}

// sameOrigin reports whether the request is same-origin: an absent Origin header
// is accepted, while a malformed one or a host differing from r.Host is not.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}
