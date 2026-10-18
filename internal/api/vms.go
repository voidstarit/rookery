package api

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"time"

	"github.com/google/uuid"

	"github.com/voidstarit/rookery/internal/qemu"
	"github.com/voidstarit/rookery/internal/readiness"
	"github.com/voidstarit/rookery/internal/store"
)

const (
	defaultVCPUs       = 1
	defaultMemoryMB    = 512
	maxNameLength      = 64
	maxDescriptionSize = 2000
	maxVCPUs           = 128
	maxMemoryMB        = 1_048_576
	maxVMInterfaces    = 32
)

var vmNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type vmFieldsPayload struct {
	Name           *string  `json:"name"`
	Description    *string  `json:"description"`
	Status         *string  `json:"status"`
	VCPUs          *int     `json:"vcpus"`
	MemoryMB       *int     `json:"memory_mb"`
	BootMediaID    *int64   `json:"boot_media_id"`
	ConsoleEnabled *bool    `json:"console_enabled"`
	InterfaceIDs   *[]int64 `json:"interface_ids"`
	DiskIDs        *[]int64 `json:"disk_ids"`
}

type vmCreatePayload struct {
	vmFieldsPayload
	DiskID *int64 `json:"disk_id"`
}

func (p vmFieldsPayload) toVM() (store.VM, error) {
	vm := store.VM{
		Status:   store.StatusStopped,
		VCPUs:    defaultVCPUs,
		MemoryMB: defaultMemoryMB,
	}

	if p.Name == nil {
		return store.VM{}, errors.New("name is required")
	}
	vm.Name = strings.TrimSpace(*p.Name)
	switch {
	case vm.Name == "":
		return store.VM{}, errors.New("name is required")
	case len(vm.Name) > maxNameLength:
		return store.VM{}, fmt.Errorf("name must be at most %d characters", maxNameLength)
	case !vmNamePattern.MatchString(vm.Name):
		return store.VM{}, errors.New("name must start with a letter or digit and contain only letters, digits, dots, dashes, and underscores")
	}

	if p.Description != nil {
		vm.Description = strings.TrimSpace(*p.Description)
		if utf8.RuneCountInString(vm.Description) > maxDescriptionSize {
			return store.VM{}, fmt.Errorf("description must be at most %d characters", maxDescriptionSize)
		}
	}

	if p.Status != nil {
		vm.Status = strings.TrimSpace(*p.Status)
		if !store.ValidStatus(vm.Status) {
			return store.VM{}, fmt.Errorf("status must be one of: %s", strings.Join(store.Statuses(), ", "))
		}
	}

	if p.VCPUs != nil {
		if *p.VCPUs < 1 || *p.VCPUs > maxVCPUs {
			return store.VM{}, fmt.Errorf("vcpus must be between 1 and %d", maxVCPUs)
		}
		vm.VCPUs = *p.VCPUs
	}

	if p.MemoryMB != nil {
		if *p.MemoryMB < 128 || *p.MemoryMB > maxMemoryMB {
			return store.VM{}, fmt.Errorf("memory_mb must be between 128 and %d", maxMemoryMB)
		}
		vm.MemoryMB = *p.MemoryMB
	}

	if p.BootMediaID != nil {
		if *p.BootMediaID < 1 {
			return store.VM{}, errors.New("boot_media_id must be a positive integer")
		}
		vm.BootMediaID = p.BootMediaID
	}

	if p.ConsoleEnabled != nil {
		vm.ConsoleEnabled = *p.ConsoleEnabled
	}

	return vm, nil
}

func (s *Server) handleListVMs(w http.ResponseWriter, r *http.Request) {
	vms, err := s.store.ListVMs(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, vms)
}

type vmMetricsView struct {
	qemu.Metrics
	VMID                string    `json:"vm_id"`
	Name                string    `json:"name"`
	VCPUs               int       `json:"vcpus"`
	MemoryMB            int       `json:"memory_mb"`
	NetworkRXBytesTotal uint64    `json:"network_rx_bytes_total"`
	NetworkTXBytesTotal uint64    `json:"network_tx_bytes_total"`
	NetworkRXPackets    uint64    `json:"network_rx_packets_total"`
	NetworkTXPackets    uint64    `json:"network_tx_packets_total"`
	SampledAt           time.Time `json:"sampled_at"`
}

func (s *Server) handleGetVMMetrics(w http.ResponseWriter, r *http.Request) {
	id, err := parseVMID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	vm, err := s.store.GetVM(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "vm not found")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	if s.vms == nil {
		writeError(w, http.StatusServiceUnavailable, "VM runtime is not configured")
		return
	}
	metrics, err := s.vms.Metrics(id)
	if err != nil {
		s.logger.Error("read VM metrics failed", "vm_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read VM metrics: "+err.Error())
		return
	}
	view := vmMetricsView{
		Metrics:   metrics,
		VMID:      vm.ID,
		Name:      vm.Name,
		VCPUs:     vm.VCPUs,
		MemoryMB:  vm.MemoryMB,
		SampledAt: time.Now().UTC(),
	}
	if metrics.Running && s.networks != nil && len(vm.Interfaces) > 0 {
		names := make([]string, 0, len(vm.Interfaces))
		for _, iface := range vm.Interfaces {
			names = append(names, iface.Name)
		}
		counters, counterErr := s.networks.InterfaceCounters(r.Context(), names)
		if counterErr != nil {
			s.logger.Warn("read VM network counters failed", "vm_id", id, "error", counterErr)
		} else {
			for _, counter := range counters {
				view.NetworkRXBytesTotal += counter.RXBytes
				view.NetworkTXBytesTotal += counter.TXBytes
				view.NetworkRXPackets += counter.RXPackets
				view.NetworkTXPackets += counter.TXPackets
			}
		}
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleGetVM(w http.ResponseWriter, r *http.Request) {
	id, err := parseVMID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	vm, err := s.store.GetVM(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "vm not found")
	case err != nil:
		s.internalError(w, r, err)
	default:
		writeJSON(w, http.StatusOK, vm)
	}
}

func (s *Server) handleCreateVM(w http.ResponseWriter, r *http.Request) {
	if !s.readiness.Ready() {
		writeError(w, http.StatusServiceUnavailable, blockedMessage(s.readiness.Status()))
		return
	}

	var payload vmCreatePayload
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	vm, err := payload.vmFieldsPayload.toVM()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var diskIDs []int64
	if payload.DiskIDs != nil {
		diskIDs = *payload.DiskIDs
	} else if payload.DiskID != nil {
		diskIDs = []int64{*payload.DiskID}
	}
	diskIDs, err = normalizeVMDiskIDs(diskIDs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	vm.DiskIDs = diskIDs
	vm.DiskID = &diskIDs[0]

	interfaceIDs, err := normalizeVMInterfaceIDs(nil)
	if payload.InterfaceIDs != nil {
		interfaceIDs, err = normalizeVMInterfaceIDs(*payload.InterfaceIDs)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.resourceMu.Lock()

	for _, interfaceID := range interfaceIDs {
		iface, err := s.store.GetInterface(r.Context(), interfaceID)
		switch {
		case errors.Is(err, store.ErrInterfaceNotFound):
			s.resourceMu.Unlock()
			writeError(w, http.StatusBadRequest, fmt.Sprintf("interface %d not found", interfaceID))
			return
		case err != nil:
			s.resourceMu.Unlock()
			s.internalError(w, r, err)
			return
		}
		if iface.AttachedVMID != nil {
			s.resourceMu.Unlock()
			writeError(w, http.StatusConflict, fmt.Sprintf("interface %q is already attached to a VM", iface.Name))
			return
		}
	}

	created, err := s.store.CreateVM(r.Context(), vm)
	if err != nil {
		s.resourceMu.Unlock()
		s.writeCreateVMError(w, r, err)
		return
	}

	for _, interfaceID := range interfaceIDs {
		if _, err := s.store.SetInterfaceAttachment(r.Context(), interfaceID, &created.ID); err != nil {
			if rollbackErr := s.store.DeleteVM(r.Context(), created.ID); rollbackErr != nil {
				s.logger.Error("rollback vm after interface attach failed", "vm_id", created.ID, "error", rollbackErr)
			}
			s.resourceMu.Unlock()
			switch {
			case errors.Is(err, store.ErrInterfaceNotFound):
				writeError(w, http.StatusBadRequest, fmt.Sprintf("interface %d not found", interfaceID))
			case errors.Is(err, store.ErrInterfaceInUse):
				writeError(w, http.StatusConflict, "interface is already attached to a VM")
			default:
				s.internalError(w, r, err)
			}
			return
		}
	}

	if len(interfaceIDs) > 0 {
		created, err = s.store.GetVM(r.Context(), created.ID)
		if err != nil {
			if rollbackErr := s.store.DeleteVM(r.Context(), created.ID); rollbackErr != nil {
				s.logger.Error("rollback vm after reload failed", "vm_id", created.ID, "error", rollbackErr)
			}
			s.resourceMu.Unlock()
			s.internalError(w, r, err)
			return
		}
	}
	s.resourceMu.Unlock()

	w.Header().Set("Location", "/api/vms/"+created.ID)
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) writeCreateVMError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNameTaken):
		writeError(w, http.StatusConflict, "a VM with that name already exists")
	case errors.Is(err, store.ErrDiskNotFound):
		writeError(w, http.StatusBadRequest, "disk not found")
	case errors.Is(err, store.ErrDiskInUse):
		writeError(w, http.StatusConflict, "disk is already attached to another VM")
	case errors.Is(err, store.ErrMediaNotFound):
		writeError(w, http.StatusBadRequest, "boot media not found")
	default:
		s.internalError(w, r, err)
	}
}

func normalizeVMDiskIDs(ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, errors.New("at least one disk is required")
	}
	if len(ids) > 32 {
		return nil, errors.New("at most 32 disks can be attached to a VM")
	}
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id < 1 {
			return nil, errors.New("disk_ids must contain positive integers")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

func normalizeVMInterfaceIDs(ids []int64) ([]int64, error) {
	if len(ids) > maxVMInterfaces {
		return nil, fmt.Errorf("at most %d network interfaces can be attached at creation", maxVMInterfaces)
	}
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id < 1 {
			return nil, errors.New("interface_ids must contain positive integers")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

func (s *Server) handleUpdateVM(w http.ResponseWriter, r *http.Request) {
	id, err := parseVMID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var payload vmFieldsPayload
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	vm, err := payload.toVM()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	vm.ID = id

	changeInterfaces := payload.InterfaceIDs != nil
	var interfaceIDs []int64
	if changeInterfaces {
		interfaceIDs, err = normalizeVMInterfaceIDs(*payload.InterfaceIDs)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	changeDisks := payload.DiskIDs != nil
	var diskIDs []int64
	if changeDisks {
		diskIDs, err = normalizeVMDiskIDs(*payload.DiskIDs)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	s.runMu.Lock()
	defer s.runMu.Unlock()

	if changeInterfaces && s.vms != nil && s.vms.Running(id) {
		writeError(w, http.StatusConflict, "stop the VM before changing its network interfaces")
		return
	}
	if changeDisks && s.vms != nil && s.vms.Running(id) {
		writeError(w, http.StatusConflict, "stop the VM before changing its disks")
		return
	}
	for _, interfaceID := range interfaceIDs {
		iface, err := s.store.GetInterface(r.Context(), interfaceID)
		switch {
		case errors.Is(err, store.ErrInterfaceNotFound):
			writeError(w, http.StatusBadRequest, fmt.Sprintf("interface %d not found", interfaceID))
			return
		case err != nil:
			s.internalError(w, r, err)
			return
		}
		if iface.AttachedVMID != nil && *iface.AttachedVMID != id {
			writeError(w, http.StatusConflict, fmt.Sprintf("interface %q is already attached to another VM", iface.Name))
			return
		}
	}
	for _, diskID := range diskIDs {
		disk, err := s.store.GetDisk(r.Context(), diskID)
		switch {
		case errors.Is(err, store.ErrDiskNotFound):
			writeError(w, http.StatusBadRequest, fmt.Sprintf("disk %d not found", diskID))
			return
		case err != nil:
			s.internalError(w, r, err)
			return
		}
		if disk.AttachedVMID != nil && *disk.AttachedVMID != id {
			writeError(w, http.StatusConflict, fmt.Sprintf("disk %q is already attached to another VM", disk.Name))
			return
		}
	}

	updated, err := s.store.UpdateVM(r.Context(), vm)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "vm not found")
		return
	case errors.Is(err, store.ErrNameTaken):
		writeError(w, http.StatusConflict, "a VM with that name already exists")
		return
	case errors.Is(err, store.ErrMediaNotFound):
		writeError(w, http.StatusBadRequest, "boot media not found")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}

	if changeInterfaces {
		updated, err = s.store.SetVMInterfaces(r.Context(), id, interfaceIDs)
		switch {
		case errors.Is(err, store.ErrInterfaceNotFound):
			writeError(w, http.StatusBadRequest, "interface not found")
			return
		case errors.Is(err, store.ErrInterfaceInUse):
			writeError(w, http.StatusConflict, "interface is already attached to another VM")
			return
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, "vm not found")
			return
		case err != nil:
			s.internalError(w, r, err)
			return
		}
	}
	if changeDisks {
		updated, err = s.store.SetVMDisks(r.Context(), id, diskIDs)
		switch {
		case errors.Is(err, store.ErrDiskNotFound):
			writeError(w, http.StatusBadRequest, "disk not found")
			return
		case errors.Is(err, store.ErrDiskInUse):
			writeError(w, http.StatusConflict, "disk is already attached to another VM")
			return
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, "vm not found")
			return
		case err != nil:
			s.internalError(w, r, err)
			return
		}
	}

	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteVM(w http.ResponseWriter, r *http.Request) {
	id, err := parseVMID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.runMu.Lock()
	defer s.runMu.Unlock()

	if _, err := s.store.GetVM(r.Context(), id); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, "vm not found")
		default:
			s.internalError(w, r, err)
		}
		return
	}
	if s.vms != nil {
		if err := s.vms.Stop(r.Context(), id); err != nil {
			s.logger.Error("stop vm before delete failed", "vm_id", id, "error", err)
			writeError(w, http.StatusInternalServerError, "could not stop VM before deleting it: "+err.Error())
			return
		}
	}

	err = s.store.DeleteVM(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "vm not found")
	case err != nil:
		s.internalError(w, r, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) handleStartVM(w http.ResponseWriter, r *http.Request) {
	id, err := parseVMID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.runMu.Lock()
	defer s.runMu.Unlock()

	vm, err := s.store.GetVM(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "vm not found")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	if len(vm.Disks) == 0 && vm.Disk == nil {
		writeError(w, http.StatusConflict, "vm has no disk")
		return
	}
	if s.vms.Running(id) {
		writeError(w, http.StatusConflict, "vm is already running")
		return
	}

	if err := s.vms.Start(r.Context(), vm); err != nil {
		s.logger.Error("start vm failed", "vm_id", id, "error", err)
		if _, statusErr := s.store.SetVMStatus(r.Context(), id, store.StatusError); statusErr != nil {
			s.logger.Error("set vm error status failed", "vm_id", id, "error", statusErr)
		}
		writeError(w, http.StatusInternalServerError, "could not start VM: "+err.Error())
		return
	}

	updated, err := s.store.SetVMStatus(r.Context(), id, store.StatusRunning)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleStopVM(w http.ResponseWriter, r *http.Request) {
	id, err := parseVMID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.runMu.Lock()
	defer s.runMu.Unlock()

	if _, err := s.store.GetVM(r.Context(), id); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, "vm not found")
		default:
			s.internalError(w, r, err)
		}
		return
	}

	if s.vms.Running(id) {
		if err := s.vms.Stop(r.Context(), id); err != nil {
			s.logger.Error("stop vm failed", "vm_id", id, "error", err)
			writeError(w, http.StatusInternalServerError, "could not stop VM: "+err.Error())
			return
		}
	}

	updated, err := s.store.SetVMStatus(r.Context(), id, store.StatusStopped)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleResetVM(w http.ResponseWriter, r *http.Request) {
	id, err := parseVMID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.runMu.Lock()
	defer s.runMu.Unlock()

	if s.vms == nil {
		writeError(w, http.StatusServiceUnavailable, "vm runtime is not configured")
		return
	}
	vm, err := s.store.GetVM(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "vm not found")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	if !s.vms.Running(id) {
		writeError(w, http.StatusConflict, "vm is not running")
		return
	}

	if err := s.vms.Reset(r.Context(), id); err != nil {
		if errors.Is(err, qemu.ErrMonitorUnavailable) {
			// QEMU was started by an older server without a monitor socket.
			// A power cycle is the closest equivalent to a reset and upgrades
			// the running process to one with a monitor.
			s.logger.Warn("qemu monitor unavailable; power-cycling VM for reset", "vm_id", id)
			if stopErr := s.vms.Stop(r.Context(), id); stopErr != nil {
				s.logger.Error("stop vm for reset fallback failed", "vm_id", id, "error", stopErr)
				writeError(w, http.StatusInternalServerError, "could not stop VM for reset: "+stopErr.Error())
				return
			}
			if startErr := s.vms.Start(r.Context(), vm); startErr != nil {
				s.logger.Error("start vm for reset fallback failed", "vm_id", id, "error", startErr)
				if _, statusErr := s.store.SetVMStatus(r.Context(), id, store.StatusError); statusErr != nil {
					s.logger.Error("set vm error status failed", "vm_id", id, "error", statusErr)
				}
				writeError(w, http.StatusInternalServerError, "could not restart VM: "+startErr.Error())
				return
			}
			if _, err := s.store.SetVMStatus(r.Context(), id, store.StatusRunning); err != nil {
				s.internalError(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		s.logger.Error("reset vm failed", "vm_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not reset VM: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleVMCtrlAltDel(w http.ResponseWriter, r *http.Request) {
	id, err := parseVMID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.runMu.Lock()
	defer s.runMu.Unlock()

	if s.vms == nil {
		writeError(w, http.StatusServiceUnavailable, "vm runtime is not configured")
		return
	}
	if _, err := s.store.GetVM(r.Context(), id); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, "vm not found")
		default:
			s.internalError(w, r, err)
		}
		return
	}
	if !s.vms.Running(id) {
		writeError(w, http.StatusConflict, "vm is not running")
		return
	}

	if err := s.vms.SendCtrlAltDel(r.Context(), id); err != nil {
		if errors.Is(err, qemu.ErrMonitorUnavailable) {
			writeError(w, http.StatusConflict, "VM was started without a QEMU monitor; use Reset or stop/start the VM once")
			return
		}
		s.logger.Error("send ctrl-alt-del failed", "vm_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not send Ctrl+Alt+Del: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseVMID(r *http.Request) (string, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return "", errors.New("invalid vm id")
	}
	return id.String(), nil
}

func blockedMessage(status readiness.Status) string {
	var problems []string
	if !status.ThinPool.Ready {
		problems = append(problems, "thin pool: "+componentReason(status.ThinPool.ComponentStatus))
	}
	if !status.VPP.Ready {
		problems = append(problems, "FD.io VPP: "+componentReason(status.VPP.ComponentStatus))
	}
	if len(problems) == 0 {
		return "VM creation is blocked until prerequisites are ready"
	}
	return "VM creation is blocked: " + strings.Join(problems, "; ")
}

func componentReason(status readiness.ComponentStatus) string {
	if status.Detail != "" {
		return status.Detail
	}
	if status.State != "" {
		return status.State
	}
	return "not ready"
}
