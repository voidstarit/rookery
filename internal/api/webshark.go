package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/voidstarit/rookery/internal/host"
)

func (s *Server) handleWebSharkWatch(w http.ResponseWriter, r *http.Request) {
	if s.networks == nil {
		writeError(w, http.StatusServiceUnavailable, "VPP network runtime is not configured")
		return
	}
	name := r.URL.Query().Get("capture")
	file, info, err := s.networks.OpenPacketCapture(name)
	if err != nil {
		if errors.Is(err, host.ErrCaptureNotFound) {
			writeError(w, http.StatusNotFound, "packet capture not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not open packet capture")
		return
	}
	_ = file.Close()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	var previous int64 = -1
	send := func(kind string, size int64) {
		payload, _ := json.Marshal(map[string]any{
			"size":  size,
			"mtime": info.Modified.UnixMilli(),
			"kind":  kind,
		})
		_, _ = fmt.Fprintf(w, "event: capture-changed\ndata: %s\n\n", payload)
		flusher.Flush()
		previous = size
	}
	send("init", info.Size)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			current, currentInfo, err := s.networks.OpenPacketCapture(name)
			if err != nil {
				continue
			}
			_ = current.Close()
			info = currentInfo
			switch {
			case currentInfo.Size < previous:
				send("full", currentInfo.Size)
			case currentInfo.Size > previous:
				send("append", currentInfo.Size)
			}
		}
	}
}
