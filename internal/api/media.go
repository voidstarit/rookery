package api

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/voidstarit/rookery/internal/store"
)

const maxMediaNameLength = 127

type mediaPayload struct {
	Name *string `json:"name"`
	Path *string `json:"path"`
}

func (p mediaPayload) toMedia() (store.Media, error) {
	media := store.Media{}

	if p.Name == nil {
		return store.Media{}, errors.New("name is required")
	}
	media.Name = strings.TrimSpace(*p.Name)
	switch {
	case media.Name == "":
		return store.Media{}, errors.New("name is required")
	case len(media.Name) > maxMediaNameLength:
		return store.Media{}, fmt.Errorf("name must be at most %d characters", maxMediaNameLength)
	case !vmNamePattern.MatchString(media.Name):
		return store.Media{}, errors.New("name must start with a letter or digit and contain only letters, digits, dots, dashes, and underscores")
	}

	if p.Path == nil {
		return store.Media{}, errors.New("path is required")
	}
	media.Path = strings.TrimSpace(*p.Path)
	if media.Path == "" {
		return store.Media{}, errors.New("path is required")
	}
	if !filepath.IsAbs(media.Path) {
		return store.Media{}, errors.New("path must be absolute")
	}
	media.Path = filepath.Clean(media.Path)

	info, err := os.Stat(media.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return store.Media{}, errors.New("media file not found")
	case err != nil:
		return store.Media{}, fmt.Errorf("inspect media file: %v", err)
	case !info.Mode().IsRegular():
		return store.Media{}, errors.New("media path must point to a regular file")
	}
	media.SizeBytes = info.Size()

	return media, nil
}

func (s *Server) handleListMedia(w http.ResponseWriter, r *http.Request) {
	media, err := s.store.ListMedia(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, media)
}

func (s *Server) handleGetMedia(w http.ResponseWriter, r *http.Request) {
	id, err := parseMediaID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	media, err := s.store.GetMedia(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrMediaNotFound):
		writeError(w, http.StatusNotFound, "media not found")
	case err != nil:
		s.internalError(w, r, err)
	default:
		writeJSON(w, http.StatusOK, media)
	}
}

func (s *Server) handleCreateMedia(w http.ResponseWriter, r *http.Request) {
	var payload mediaPayload
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	media, err := payload.toMedia()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	created, err := s.store.CreateMedia(r.Context(), media)
	switch {
	case errors.Is(err, store.ErrMediaNameTaken):
		writeError(w, http.StatusConflict, "media with that name already exists")
	case err != nil:
		s.internalError(w, r, err)
	default:
		w.Header().Set("Location", fmt.Sprintf("/api/media/%d", created.ID))
		writeJSON(w, http.StatusCreated, created)
	}
}

func (s *Server) handleDeleteMedia(w http.ResponseWriter, r *http.Request) {
	id, err := parseMediaID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	err = s.store.DeleteMedia(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrMediaNotFound):
		writeError(w, http.StatusNotFound, "media not found")
	case errors.Is(err, store.ErrMediaInUse):
		writeError(w, http.StatusConflict, "media is used as boot media by a VM; clear it first")
	case err != nil:
		s.internalError(w, r, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func parseMediaID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("invalid media id")
	}
	return id, nil
}
