package api

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/voidstarit/rookery/internal/lightvm"
)

// handleLightVMUpload streams a multipart upload into managed LightVM storage.
// The UI uploads the file first and then creates the inventory record through
// the gateway API with the returned path.
func (s *Server) handleLightVMUpload(w http.ResponseWriter, r *http.Request) {
	if s.lightvm == nil {
		writeError(w, http.StatusServiceUnavailable, "LightVM storage is not configured")
		return
	}

	// Large kernel and initramfs uploads can take longer than the server-wide
	// read/write timeouts. Extend the deadlines for this request only.
	controller := http.NewResponseController(w)
	deadline := time.Now().Add(30 * time.Minute)
	_ = controller.SetReadDeadline(deadline)
	_ = controller.SetWriteDeadline(deadline)

	r.Body = http.MaxBytesReader(w, r.Body, s.lightvm.MaxUploadBytes()+(1<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "request must be multipart/form-data")
		return
	}

	var (
		kind   string
		stored lightvm.StoredFile
	)
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if tooLarge(err) {
				writeError(w, http.StatusRequestEntityTooLarge, "upload exceeds the maximum size")
				return
			}
			writeError(w, http.StatusBadRequest, "could not read upload")
			return
		}

		switch part.FormName() {
		case "kind":
			if kind != "" || stored.Path != "" {
				_ = part.Close()
				writeError(w, http.StatusBadRequest, "kind must be the first form field")
				return
			}
			value, err := io.ReadAll(io.LimitReader(part, 64))
			_ = part.Close()
			if err != nil {
				writeError(w, http.StatusBadRequest, "could not read upload kind")
				return
			}
			kind = strings.TrimSpace(string(value))
			if kind == "" {
				writeError(w, http.StatusBadRequest, "upload kind is required")
				return
			}
		case "file":
			if kind == "" {
				_ = part.Close()
				writeError(w, http.StatusBadRequest, "kind must be the first form field")
				return
			}
			if stored.Path != "" {
				_ = part.Close()
				writeError(w, http.StatusBadRequest, "only one file may be uploaded")
				return
			}
			stored, err = s.lightvm.Save(kind, part.FileName(), part)
			_ = part.Close()
			if err != nil {
				switch {
				case errors.Is(err, lightvm.ErrTooLarge), tooLarge(err):
					writeError(w, http.StatusRequestEntityTooLarge, "upload exceeds the maximum size")
				case errors.Is(err, lightvm.ErrUnsupportedKind):
					writeError(w, http.StatusBadRequest, err.Error())
				default:
					s.internalError(w, r, err)
				}
				return
			}
		default:
			_ = part.Close()
		}
	}

	if stored.Path == "" {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	writeJSON(w, http.StatusCreated, stored)
}

// handleDeleteLightVMUpload removes an abandoned managed upload. It never
// touches files outside the managed directory.
func (s *Server) handleDeleteLightVMUpload(w http.ResponseWriter, r *http.Request) {
	if s.lightvm == nil {
		writeError(w, http.StatusServiceUnavailable, "LightVM storage is not configured")
		return
	}
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	if !s.lightvm.Managed(path) {
		writeError(w, http.StatusBadRequest, "path is not in managed LightVM storage")
		return
	}
	if err := s.lightvm.Remove(path); err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// LightVMStartDeadline extends the response deadline for LightVM start
// requests, which may include a first-time buildah image pull. It must wrap
// the gateway handler so the deadline is raised before the RPC runs.
func LightVMStartDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost &&
			strings.HasPrefix(r.URL.Path, "/api/lightvm/instances/") &&
			strings.HasSuffix(r.URL.Path, "/start") {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(15 * time.Minute))
		}
		next.ServeHTTP(w, r)
	})
}

func tooLarge(err error) bool {
	var maxBytes *http.MaxBytesError
	return errors.As(err, &maxBytes)
}
