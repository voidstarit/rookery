package api

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/websocket"

	"github.com/voidstarit/rookery/internal/store"
)

const lightVMConsoleDialTimeout = 3 * time.Second

// handleLightVMConsoleProxy streams a running LightVM's virtio console Unix
// socket over a WebSocket. LightVMs have no graphical console.
func (s *Server) handleLightVMConsoleProxy(w http.ResponseWriter, r *http.Request) {
	parsed, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid lightvm id")
		return
	}
	id := parsed.String()
	if s.lightvms == nil {
		writeError(w, http.StatusServiceUnavailable, "LightVM runtime is not configured")
		return
	}

	vm, err := s.store.GetLightVM(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrLightVMNotFound):
			writeError(w, http.StatusNotFound, "lightvm not found")
		default:
			s.internalError(w, r, err)
		}
		return
	}
	if !vm.ConsoleEnabled {
		writeError(w, http.StatusForbidden, "console access is disabled for this LightVM")
		return
	}
	if !s.lightvms.RunningLightVM(id) {
		writeError(w, http.StatusConflict, "lightvm is not running")
		return
	}
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "websocket origin not allowed")
		return
	}

	socket := s.lightvms.LightVMConsoleSocket(id)
	if socket == "" {
		writeError(w, http.StatusInternalServerError, "LightVM console socket is unavailable")
		return
	}
	conn, err := net.DialTimeout("unix", socket, lightVMConsoleDialTimeout)
	if err != nil {
		s.logger.Error().Str("lightvm_id", id).Str("socket", socket).Err(err).Msg("connect LightVM console socket failed")
		writeError(w, http.StatusBadGateway, "LightVM console endpoint is unavailable")
		return
	}
	defer conn.Close()

	websocket.Server{
		Handler: func(ws *websocket.Conn) {
			ws.PayloadType = websocket.BinaryFrame
			s.proxyLightVMConsole(ws, conn, id)
		},
		Handshake: func(_ *websocket.Config, req *http.Request) error {
			if !sameOrigin(req) {
				return errors.New("origin not allowed")
			}
			return nil
		},
	}.ServeHTTP(w, r)
}

func (s *Server) proxyLightVMConsole(ws *websocket.Conn, conn net.Conn, lightVMID string) {
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

	// Close an active console shortly after an administrator disables access
	// or the LightVM is deleted/stopped.
	go func() {
		ticker := time.NewTicker(consoleAccessCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				vm, err := s.store.GetLightVM(ctx, lightVMID)
				cancel()
				if err != nil || !vm.ConsoleEnabled || !s.lightvms.RunningLightVM(lightVMID) {
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
