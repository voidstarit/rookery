package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/websocket"

	"github.com/voidstarit/rookery/internal/lightvm"
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
	consoleID, err := lightVMConsoleID(r, vm)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "websocket origin not allowed")
		return
	}

	socket := s.lightvms.LightVMConsoleSocket(id, consoleID)
	if socket == "" {
		writeError(w, http.StatusInternalServerError, "LightVM console socket is unavailable")
		return
	}
	check := func(ctx context.Context) bool {
		current, err := s.store.GetLightVM(ctx, id)
		return err == nil && current.ConsoleEnabled && s.lightvms.RunningLightVM(id)
	}
	s.serveLightVMConsole(w, r, id, consoleID, socket, func(_ *websocket.Config, req *http.Request) error {
		if !sameOrigin(req) {
			return errors.New("origin not allowed")
		}
		return nil
	}, check)
}

// handleLightVMConsoleAccessProxy streams a LightVM console over a WebSocket
// authenticated by a time-limited console-access token. It is reachable from
// non-browser clients without an Origin header, unlike the UI console route.
func (s *Server) handleLightVMConsoleAccessProxy(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.PathValue("token"))
	if !lightvm.ValidConsoleAccessToken(token) {
		writeError(w, http.StatusNotFound, "console access not found")
		return
	}
	if s.lightvms == nil {
		writeError(w, http.StatusServiceUnavailable, "LightVM runtime is not configured")
		return
	}

	tokenHash := lightvm.HashConsoleAccessToken(token)
	access, err := s.store.GetLightVMConsoleAccessByTokenHash(r.Context(), tokenHash, time.Now())
	switch {
	case errors.Is(err, store.ErrLightVMConsoleAccessNotFound):
		writeError(w, http.StatusNotFound, "console access not found")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}

	vm, err := s.store.GetLightVM(r.Context(), access.LightVMID)
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
	if !s.lightvms.RunningLightVM(vm.ID) {
		writeError(w, http.StatusConflict, "lightvm is not running")
		return
	}
	socket := s.lightvms.LightVMConsoleSocket(vm.ID, access.ConsoleID)
	if socket == "" {
		writeError(w, http.StatusInternalServerError, "LightVM console socket is unavailable")
		return
	}
	check := func(ctx context.Context) bool {
		if _, err := s.store.GetLightVMConsoleAccessByTokenHash(ctx, tokenHash, time.Now()); err != nil {
			return false
		}
		current, err := s.store.GetLightVM(ctx, access.LightVMID)
		return err == nil && current.ConsoleEnabled && s.lightvms.RunningLightVM(access.LightVMID)
	}
	s.serveLightVMConsole(w, r, vm.ID, access.ConsoleID, socket, func(*websocket.Config, *http.Request) error {
		return nil
	}, check)
}

// serveLightVMConsole dials the console's Unix socket with the LightVM dial
// timeout and upgrades the request to a WebSocket that proxies the console,
// returning 502 when the socket is unreachable.
func (s *Server) serveLightVMConsole(w http.ResponseWriter, r *http.Request, lightVMID string, consoleID int64, socket string, handshake func(*websocket.Config, *http.Request) error, check func(context.Context) bool) {
	conn, err := net.DialTimeout("unix", socket, lightVMConsoleDialTimeout)
	if err != nil {
		s.logger.Error().Str("lightvm_id", lightVMID).Int64("console_id", consoleID).Str("socket", socket).Err(err).Msg("connect LightVM console socket failed")
		writeError(w, http.StatusBadGateway, "LightVM console endpoint is unavailable")
		return
	}
	defer conn.Close()

	websocket.Server{
		Handler: func(ws *websocket.Conn) {
			ws.PayloadType = websocket.BinaryFrame
			s.proxyLightVMConsole(ws, conn, check)
		},
		Handshake: handshake,
	}.ServeHTTP(w, r)
}

// lightVMConsoleID resolves the console query parameter against vm.Consoles,
// defaulting to the first console when the parameter is absent and rejecting
// non-positive or unknown console IDs.
func lightVMConsoleID(r *http.Request, vm store.LightVM) (int64, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("console"))
	if raw == "" {
		if len(vm.Consoles) == 0 {
			return 0, nil
		}
		return vm.Consoles[0].ID, nil
	}
	consoleID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || consoleID < 1 {
		return 0, errors.New("invalid console id")
	}
	for _, console := range vm.Consoles {
		if console.ID == consoleID {
			return consoleID, nil
		}
	}
	return 0, fmt.Errorf("console %d not found", consoleID)
}

// proxyLightVMConsole copies bytes in both directions between ws and conn and
// polls check once per second, closing both sides when console access is revoked
// or the LightVM stops.
func (s *Server) proxyLightVMConsole(ws *websocket.Conn, conn net.Conn, check func(context.Context) bool) {
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
				ok := check(ctx)
				cancel()
				if !ok {
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
