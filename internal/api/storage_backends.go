package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/store"
)

type storageBackendPayload struct {
	Name        string                     `json:"name"`
	Title       string                     `json:"title"`
	Description string                     `json:"description"`
	Driver      string                     `json:"driver"`
	Config      store.StorageBackendConfig `json:"config"`
}

func normalizeStorageBackend(payload storageBackendPayload) (store.StorageBackend, error) {
	name := strings.TrimSpace(payload.Name)
	switch {
	case name == "":
		return store.StorageBackend{}, errors.New("name is required")
	case len(name) > 64:
		return store.StorageBackend{}, errors.New("name must be at most 64 characters")
	case !vmNamePattern.MatchString(name):
		return store.StorageBackend{}, errors.New("name must start with a letter or digit and contain only letters, digits, dots, dashes, and underscores")
	}
	title := strings.TrimSpace(payload.Title)
	if len(title) > 128 {
		return store.StorageBackend{}, errors.New("title must be at most 128 characters")
	}
	description := strings.TrimSpace(payload.Description)
	if len(description) > 2000 {
		return store.StorageBackend{}, errors.New("description must be at most 2000 characters")
	}
	driver := strings.ToLower(strings.TrimSpace(payload.Driver))
	if driver == "" {
		driver = "lvm-thin"
	}
	if driver != "lvm-thin" {
		return store.StorageBackend{}, fmt.Errorf("unsupported storage driver %q", driver)
	}
	vg := strings.TrimSpace(payload.Config.VG)
	thinPool := strings.TrimSpace(payload.Config.ThinPool)
	if err := host.ValidateLVMName("volume group", vg); err != nil {
		return store.StorageBackend{}, err
	}
	if err := host.ValidateLVMName("thin pool", thinPool); err != nil {
		return store.StorageBackend{}, err
	}
	return store.StorageBackend{
		Name:        name,
		Title:       title,
		Description: description,
		Driver:      driver,
		Config:      store.StorageBackendConfig{VG: vg, ThinPool: thinPool},
	}, nil
}

func (s *Server) handleListStorageBackends(w http.ResponseWriter, r *http.Request) {
	backends, err := s.store.ListStorageBackends(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, backends)
}

func (s *Server) handleGetStorageBackend(w http.ResponseWriter, r *http.Request) {
	id, err := parseStorageBackendID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	backend, err := s.store.GetStorageBackend(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrStorageBackendNotFound):
		writeError(w, http.StatusNotFound, "storage backend not found")
	case err != nil:
		s.internalError(w, r, err)
	default:
		writeJSON(w, http.StatusOK, backend)
	}
}

func (s *Server) handleCreateStorageBackend(w http.ResponseWriter, r *http.Request) {
	var payload storageBackendPayload
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	backend, err := normalizeStorageBackend(payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := s.store.CreateStorageBackend(r.Context(), backend)
	if err != nil {
		writeStorageBackendError(w, r, s, err)
		return
	}
	s.configureStorageReadiness(created)
	w.Header().Set("Location", fmt.Sprintf("/api/storage-backends/%d", created.ID))
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleUpdateStorageBackend(w http.ResponseWriter, r *http.Request) {
	id, err := parseStorageBackendID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	existing, err := s.store.GetStorageBackend(r.Context(), id)
	if err != nil {
		writeStorageBackendError(w, r, s, err)
		return
	}
	var payload storageBackendPayload
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	backend, err := normalizeStorageBackend(payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	backend.ID = existing.ID
	if existing.DiskCount > 0 && (backend.Driver != existing.Driver || backend.Config != existing.Config) {
		writeError(w, http.StatusConflict, "storage backend configuration cannot change while disks use it")
		return
	}
	updated, err := s.store.UpdateStorageBackend(r.Context(), backend)
	if err != nil {
		writeStorageBackendError(w, r, s, err)
		return
	}
	s.configureStorageReadiness(updated)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteStorageBackend(w http.ResponseWriter, r *http.Request) {
	id, err := parseStorageBackendID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	existing, err := s.store.GetStorageBackend(r.Context(), id)
	if err != nil {
		writeStorageBackendError(w, r, s, err)
		return
	}
	if err := s.store.DeleteStorageBackend(r.Context(), id); err != nil {
		writeStorageBackendError(w, r, s, err)
		return
	}
	s.configureStorageReadinessAfterDelete(existing)
	w.WriteHeader(http.StatusNoContent)
}

type storageReadinessConfigurer interface {
	StorageConfig() (vg, thinPool string)
	SetStorage(vg, thinPool string)
}

func (s *Server) configureStorageReadiness(backend store.StorageBackend) {
	manager, ok := s.readiness.(storageReadinessConfigurer)
	if !ok || backend.Driver != "lvm-thin" {
		return
	}
	vg, _ := manager.StorageConfig()
	if vg == "" || backend.Name == "default" {
		manager.SetStorage(backend.Config.VG, backend.Config.ThinPool)
	}
}

func (s *Server) configureStorageReadinessAfterDelete(backend store.StorageBackend) {
	manager, ok := s.readiness.(storageReadinessConfigurer)
	if !ok {
		return
	}
	vg, thinPool := manager.StorageConfig()
	if vg != backend.Config.VG || thinPool != backend.Config.ThinPool {
		return
	}
	backends, err := s.store.ListStorageBackends(context.Background())
	if err != nil {
		return
	}
	for _, candidate := range backends {
		if candidate.Driver == "lvm-thin" && candidate.Config.VG != "" && candidate.Config.ThinPool != "" {
			manager.SetStorage(candidate.Config.VG, candidate.Config.ThinPool)
			return
		}
	}
	manager.SetStorage("", "")
}

func parseStorageBackendID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("invalid storage backend id")
	}
	return id, nil
}

func writeStorageBackendError(w http.ResponseWriter, r *http.Request, s *Server, err error) {
	switch {
	case errors.Is(err, store.ErrStorageBackendNotFound):
		writeError(w, http.StatusNotFound, "storage backend not found")
	case errors.Is(err, store.ErrStorageBackendNameTaken):
		writeError(w, http.StatusConflict, "a storage backend with that name already exists")
	case errors.Is(err, store.ErrStorageBackendInUse):
		writeError(w, http.StatusConflict, "storage backend still has disks")
	default:
		s.internalError(w, r, err)
	}
}
