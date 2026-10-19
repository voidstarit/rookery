package api

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/voidstarit/rookery/internal/host"
)

func (s *Server) handleDownloadPacketCapture(w http.ResponseWriter, r *http.Request) {
	if s.networks == nil {
		writeError(w, http.StatusServiceUnavailable, "VPP network runtime is not configured")
		return
	}
	name := r.PathValue("name")
	file, info, err := s.networks.OpenPacketCapture(name)
	if err != nil {
		if errors.Is(err, host.ErrCaptureNotFound) {
			writeError(w, http.StatusNotFound, "packet capture not found")
			return
		}
		s.logger.Error("open packet capture failed", "error", err)
		writeError(w, http.StatusInternalServerError, "could not open packet capture")
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/vnd.tcpdump.pcap")
	w.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(info.Name, `"`, "")+`"`)
	http.ServeContent(w, r, info.Name, info.Modified, file)
}

func webSharkFiles(files []host.PacketCaptureFile) map[string]any {
	entries := make([]map[string]any, 0, len(files))
	for _, file := range files {
		entries = append(entries, map[string]any{"name": file.Name, "size": file.Size})
	}
	return map[string]any{"files": entries, "pwd": "."}
}

func (s *Server) handleWebSharkJSON(w http.ResponseWriter, r *http.Request) {
	if s.networks == nil {
		writeError(w, http.StatusServiceUnavailable, "VPP network runtime is not configured")
		return
	}
	method := r.URL.Query().Get("method")
	switch method {
	case "files":
		files, err := s.networks.ListPacketCaptures()
		if err != nil {
			s.logger.Error("list packet captures failed", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"err": 1, "errstr": "cannot list captures"})
			return
		}
		writeJSON(w, http.StatusOK, webSharkFiles(files))
	case "download":
		name := r.URL.Query().Get("capture")
		file, info, err := s.networks.OpenPacketCapture(name)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"err": 1, "errstr": "not found"})
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", "application/vnd.tcpdump.pcap")
		w.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(info.Name, `"`, "")+`"`)
		http.ServeContent(w, r, info.Name, info.Modified, file)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"err": 1, "errstr": "method not allowed"})
	}
}

func (s *Server) handleWebSharkCapture(w http.ResponseWriter, r *http.Request) {
	if s.networks == nil {
		writeError(w, http.StatusServiceUnavailable, "VPP network runtime is not configured")
		return
	}
	name, err := url.PathUnescape(r.PathValue("name"))
	if err != nil || strings.Contains(name, "..") {
		writeError(w, http.StatusBadRequest, "invalid capture name")
		return
	}
	file, info, err := s.networks.OpenPacketCapture(name)
	if err != nil {
		if errors.Is(err, host.ErrCaptureNotFound) {
			writeError(w, http.StatusNotFound, "packet capture not found")
			return
		}
		s.logger.Error("open packet capture failed", "error", err)
		writeError(w, http.StatusInternalServerError, "could not open packet capture")
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/vnd.tcpdump.pcap")
	http.ServeContent(w, r, info.Name, info.Modified, file)
}

func (s *Server) handleDeletePacketCapture(w http.ResponseWriter, r *http.Request) {
	if s.networks == nil {
		writeError(w, http.StatusServiceUnavailable, "VPP network runtime is not configured")
		return
	}
	name := r.PathValue("name")
	if err := s.networks.DeletePacketCapture(name); err != nil {
		switch {
		case errors.Is(err, host.ErrCaptureNotFound):
			writeError(w, http.StatusNotFound, "packet capture not found")
		case errors.Is(err, host.ErrCaptureFileActive):
			writeError(w, http.StatusConflict, err.Error())
		default:
			s.logger.Error("delete packet capture failed", "error", err)
			writeError(w, http.StatusInternalServerError, "could not delete packet capture")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
