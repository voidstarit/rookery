package api

import (
	"errors"
	"net/http"
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
		s.logger.Error().Err(err).Msg("open packet capture failed")
		writeError(w, http.StatusInternalServerError, "could not open packet capture")
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/vnd.tcpdump.pcap")
	w.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(info.Name, `"`, "")+`"`)
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
			s.logger.Error().Err(err).Msg("delete packet capture failed")
			writeError(w, http.StatusInternalServerError, "could not delete packet capture")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
