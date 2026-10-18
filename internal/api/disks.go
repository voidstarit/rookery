package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/readiness"
	"github.com/voidstarit/rookery/internal/store"
)

const (
	defaultDiskGB     = 10
	maxDiskNameLength = 127
	minDiskGB         = 1
	maxDiskGB         = 65_536
	maxImportDisks    = 256
)

type diskPayload struct {
	Name             *string `json:"name"`
	SizeGB           *int    `json:"size_gb"`
	StorageBackendID *int64  `json:"storage_backend_id"`
}

func (p diskPayload) toDisk() (store.Disk, error) {
	disk := store.Disk{SizeGB: defaultDiskGB}

	if p.Name == nil {
		return store.Disk{}, errors.New("name is required")
	}
	disk.Name = strings.TrimSpace(*p.Name)
	switch {
	case disk.Name == "":
		return store.Disk{}, errors.New("name is required")
	case len(disk.Name) > maxDiskNameLength:
		return store.Disk{}, fmt.Errorf("name must be at most %d characters", maxDiskNameLength)
	case !vmNamePattern.MatchString(disk.Name):
		return store.Disk{}, errors.New("name must start with a letter or digit and contain only letters, digits, dots, dashes, and underscores")
	}

	if p.SizeGB != nil {
		if *p.SizeGB < minDiskGB || *p.SizeGB > maxDiskGB {
			return store.Disk{}, fmt.Errorf("size_gb must be between %d and %d", minDiskGB, maxDiskGB)
		}
		disk.SizeGB = *p.SizeGB
	}

	return disk, nil
}

func (s *Server) handleListDisks(w http.ResponseWriter, r *http.Request) {
	disks, err := s.store.ListDisks(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, disks)
}

func (s *Server) handleGetDisk(w http.ResponseWriter, r *http.Request) {
	id, err := parseDiskID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	disk, err := s.store.GetDisk(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrDiskNotFound):
		writeError(w, http.StatusNotFound, "disk not found")
	case err != nil:
		s.internalError(w, r, err)
	default:
		writeJSON(w, http.StatusOK, disk)
	}
}

func (s *Server) handleCreateDisk(w http.ResponseWriter, r *http.Request) {
	var payload diskPayload
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	disk, err := payload.toDisk()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	backend, err := s.resolveStorageBackend(r.Context(), payload.StorageBackendID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if backend.Driver != "lvm-thin" {
		writeError(w, http.StatusBadRequest, "selected storage backend does not support thin disks")
		return
	}
	if backend.Config.VG == "" || backend.Config.ThinPool == "" {
		writeError(w, http.StatusBadRequest, "selected storage backend is not configured with a volume group and thin pool")
		return
	}
	if backend.Name == "default" && !s.readiness.Status().ThinPool.Ready {
		writeError(w, http.StatusServiceUnavailable, diskBlockedMessage(s.readiness.Status()))
		return
	}

	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()

	_, err = s.store.GetDiskByName(r.Context(), disk.Name)
	switch {
	case err == nil:
		writeError(w, http.StatusConflict, "a disk with that name already exists")
		return
	case !errors.Is(err, store.ErrDiskNotFound):
		s.internalError(w, r, err)
		return
	}

	disk.StorageBackendID = &backend.ID
	disk.StorageBackendName = backend.Name
	disk.VG = backend.Config.VG
	disk.ThinPool = backend.Config.ThinPool

	if err := s.disks.CreateThinVolume(r.Context(), disk.VG, disk.ThinPool, disk.Name, disk.SizeGB); err != nil {
		s.logger.Error("create thin volume failed",
			"vg", disk.VG,
			"thin_pool", disk.ThinPool,
			"disk", disk.Name,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, "could not create disk volume: "+err.Error())
		return
	}

	created, err := s.store.CreateDisk(r.Context(), disk)
	if err != nil {
		s.rollbackDiskVolume(disk.VG, disk.Name)
		switch {
		case errors.Is(err, store.ErrDiskNameTaken):
			writeError(w, http.StatusConflict, "a disk with that name already exists")
		default:
			s.internalError(w, r, err)
		}
		return
	}

	w.Header().Set("Location", fmt.Sprintf("/api/disks/%d", created.ID))
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleDeleteDisk(w http.ResponseWriter, r *http.Request) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()

	id, err := parseDiskID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	disk, err := s.store.GetDisk(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrDiskNotFound):
		writeError(w, http.StatusNotFound, "disk not found")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}

	if disk.AttachedVMID != nil {
		writeError(w, http.StatusConflict, "disk is attached to a VM; delete the VM first")
		return
	}

	if err := s.disks.DeleteThinVolume(r.Context(), disk.VG, disk.Name); err != nil {
		s.logger.Error("remove thin volume failed",
			"vg", disk.VG,
			"disk", disk.Name,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, "could not remove disk volume: "+err.Error())
		return
	}

	err = s.store.DeleteDisk(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrDiskNotFound):
		writeError(w, http.StatusNotFound, "disk not found")
	case errors.Is(err, store.ErrDiskInUse):
		writeError(w, http.StatusConflict, "disk is attached to a VM; delete the VM first")
	case err != nil:
		s.internalError(w, r, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

type importableDisk struct {
	Name               string  `json:"name"`
	SizeGB             int     `json:"size_gb"`
	Registered         bool    `json:"registered"`
	DiskID             *int64  `json:"disk_id,omitempty"`
	AttachedVMID       *string `json:"attached_vm_id,omitempty"`
	AttachedVMName     string  `json:"attached_vm_name,omitempty"`
	StorageBackendName string  `json:"storage_backend_name,omitempty"`
}

type importDisksPayload struct {
	Names []string `json:"names"`
}

func normalizeImportDiskNames(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, errors.New("at least one disk is required")
	}
	if len(names) > maxImportDisks {
		return nil, fmt.Errorf("at most %d disks can be imported at once", maxImportDisks)
	}
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if err := host.ValidateLVMName("logical volume", name); err != nil {
			return nil, err
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("duplicate disk name %q", name)
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	return out, nil
}

func (s *Server) handleListImportableDisks(w http.ResponseWriter, r *http.Request) {
	id, err := parseStorageBackendID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	backend, err := s.store.GetStorageBackend(r.Context(), id)
	if err != nil {
		writeStorageBackendError(w, r, s, err)
		return
	}
	if backend.Driver != "lvm-thin" || backend.Config.VG == "" || backend.Config.ThinPool == "" {
		writeError(w, http.StatusBadRequest, "storage backend is not configured for LVM thin volumes")
		return
	}

	volumes, err := s.disks.ListThinVolumes(r.Context(), backend.Config.VG, backend.Config.ThinPool)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	disks, err := s.store.ListDisks(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	registered := make(map[string]store.Disk, len(disks))
	for _, disk := range disks {
		registered[strings.ToLower(disk.Name)] = disk
	}

	items := make([]importableDisk, 0, len(volumes))
	for _, volume := range volumes {
		item := importableDisk{Name: volume.Name, SizeGB: volume.SizeGB}
		if disk, ok := registered[strings.ToLower(volume.Name)]; ok {
			item.Registered = true
			item.DiskID = &disk.ID
			item.AttachedVMID = disk.AttachedVMID
			item.AttachedVMName = disk.AttachedVMName
			item.StorageBackendName = disk.StorageBackendName
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		left := strings.ToLower(items[i].Name)
		right := strings.ToLower(items[j].Name)
		if left == right {
			return items[i].Name < items[j].Name
		}
		return left < right
	})
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleImportDisks(w http.ResponseWriter, r *http.Request) {
	id, err := parseStorageBackendID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	backend, err := s.store.GetStorageBackend(r.Context(), id)
	if err != nil {
		writeStorageBackendError(w, r, s, err)
		return
	}
	if backend.Driver != "lvm-thin" || backend.Config.VG == "" || backend.Config.ThinPool == "" {
		writeError(w, http.StatusBadRequest, "storage backend is not configured for LVM thin volumes")
		return
	}

	var payload importDisksPayload
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	names, err := normalizeImportDiskNames(payload.Names)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()

	volumes, err := s.disks.ListThinVolumes(r.Context(), backend.Config.VG, backend.Config.ThinPool)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	byName := make(map[string]host.ThinVolume, len(volumes))
	for _, volume := range volumes {
		byName[volume.Name] = volume
	}

	imports := make([]store.ImportDisk, 0, len(names))
	for _, name := range names {
		volume, ok := byName[name]
		if !ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("thin volume %q was not found in the backend", name))
			return
		}
		switch _, err := s.store.GetDiskByName(r.Context(), name); {
		case err == nil:
			writeError(w, http.StatusConflict, fmt.Sprintf("disk %q is already registered", name))
			return
		case !errors.Is(err, store.ErrDiskNotFound):
			s.internalError(w, r, err)
			return
		}
		imports = append(imports, store.ImportDisk{Name: volume.Name, SizeGB: volume.SizeGB})
	}

	created, err := s.store.ImportDisks(r.Context(), backend.ID, backend.Config.VG, backend.Config.ThinPool, imports)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrDiskNameTaken):
			writeError(w, http.StatusConflict, "a disk with that name already exists")
		default:
			s.internalError(w, r, err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) resolveStorageBackend(ctx context.Context, id *int64) (store.StorageBackend, error) {
	if id != nil {
		backend, err := s.store.GetStorageBackend(ctx, *id)
		if err != nil {
			if errors.Is(err, store.ErrStorageBackendNotFound) {
				return store.StorageBackend{}, errors.New("storage backend not found")
			}
			return store.StorageBackend{}, err
		}
		return backend, nil
	}
	backend, err := s.store.GetStorageBackendByName(ctx, "default")
	if err == nil {
		return backend, nil
	}
	if !errors.Is(err, store.ErrStorageBackendNotFound) {
		return store.StorageBackend{}, err
	}
	backends, listErr := s.store.ListStorageBackends(ctx)
	if listErr != nil {
		return store.StorageBackend{}, listErr
	}
	for _, candidate := range backends {
		if candidate.Driver == "lvm-thin" && candidate.Config.VG != "" && candidate.Config.ThinPool != "" {
			return candidate, nil
		}
	}
	status := s.readiness.Status()
	if status.ThinPool.VG == "" || status.ThinPool.Name == "" {
		return store.StorageBackend{}, errors.New("no storage backend selected and no default backend is configured")
	}
	return s.store.CreateStorageBackend(ctx, store.StorageBackend{
		Name:        "default",
		Title:       "Default LVM thin pool",
		Description: "Bootstrapped from the API -vg and -thin-pool flags",
		Driver:      "lvm-thin",
		Config:      store.StorageBackendConfig{VG: status.ThinPool.VG, ThinPool: status.ThinPool.Name},
	})
}

func (s *Server) rollbackDiskVolume(vg, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.disks.DeleteThinVolume(ctx, vg, name); err != nil {
		s.logger.Error("rollback disk volume failed",
			"vg", vg,
			"disk", name,
			"error", err,
		)
	}
}

func parseDiskID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("invalid disk id")
	}
	return id, nil
}

func diskBlockedMessage(status readiness.Status) string {
	if status.ThinPool.Ready {
		return "disk creation is blocked until the thin pool is ready"
	}
	return "disk creation is blocked: thin pool: " + componentReason(status.ThinPool.ComponentStatus)
}
