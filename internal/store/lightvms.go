package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrLightVMNotFound is returned when a LightVM does not exist.
	ErrLightVMNotFound = errors.New("lightvm not found")
	// ErrLightVMNameTaken is returned when a LightVM name is already in use.
	ErrLightVMNameTaken = errors.New("lightvm name already exists")
)

const (
	// DefaultLightVMAppend is the kernel command line used when a LightVM
	// definition does not set one.
	DefaultLightVMAppend = "console=hvc0 rdinit=/init"
)

// LightVMEnvVar is one environment variable passed to a LightVM workload.
type LightVMEnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// LightVM is a container-backed microVM definition. It is inventory-only until
// a runtime is implemented; kernels and initramfs images must exist.
type LightVM struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	VCPUs          int             `json:"vcpus"`
	MemoryMB       int             `json:"memory_mb"`
	ConsoleEnabled bool            `json:"console_enabled"`
	CgroupClass    string          `json:"cgroup_class,omitempty"`
	KernelID       int64           `json:"kernel_id"`
	KernelName     string          `json:"kernel_name"`
	InitramfsID    int64           `json:"initramfs_id"`
	InitramfsName  string          `json:"initramfs_name"`
	Image          string          `json:"image"`
	Env            []LightVMEnvVar `json:"env"`
	ExecutablePath string          `json:"executable_path"`
	ExecutableArgs []string        `json:"executable_args"`
	Append         string          `json:"append"`
	Status         string          `json:"status"`
	DesiredState   string          `json:"desired_state"`
	KernelPath     string          `json:"-"`
	InitramfsPath  string          `json:"-"`
	InterfaceIDs   []int64         `json:"interface_ids,omitempty"`
	Interfaces     []VMInterface   `json:"interfaces,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

const lightVMSelect = `
	SELECT l.id, l.name, l.description, l.vcpus, l.memory_mb, l.console_enabled,
	       l.cgroup_class, l.kernel_id, k.name, k.path, l.initramfs_id, i.name,
	       i.path, l.image, l.env, l.executable_path, l.executable_args, l.append,
	       l.status, l.desired_state, l.created_at, l.updated_at
	FROM lightvms AS l
	JOIN lightvm_kernels AS k ON k.id = l.kernel_id
	JOIN lightvm_initramfs AS i ON i.id = l.initramfs_id`

// ListLightVMs returns every LightVM ordered by name.
func (s *Store) ListLightVMs(ctx context.Context) ([]LightVM, error) {
	rows, err := s.db.QueryContext(ctx, lightVMSelect+` ORDER BY l.name COLLATE NOCASE, l.id`)
	if err != nil {
		return nil, fmt.Errorf("query lightvms: %w", err)
	}
	defer rows.Close()

	vms := make([]LightVM, 0)
	for rows.Next() {
		vm, err := scanLightVM(rows)
		if err != nil {
			return nil, err
		}
		vms = append(vms, vm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lightvms: %w", err)
	}
	if err := s.loadLightVMInterfaces(ctx, vms); err != nil {
		return nil, err
	}
	return vms, nil
}

// GetLightVM returns one LightVM by ID.
func (s *Store) GetLightVM(ctx context.Context, id string) (LightVM, error) {
	row := s.db.QueryRowContext(ctx, lightVMSelect+` WHERE l.id = ?`, id)
	vm, err := scanLightVM(row)
	if errors.Is(err, sql.ErrNoRows) {
		return LightVM{}, ErrLightVMNotFound
	}
	if err != nil {
		return LightVM{}, err
	}
	items := []LightVM{vm}
	if err := s.loadLightVMInterfaces(ctx, items); err != nil {
		return LightVM{}, err
	}
	return items[0], nil
}

// CreateLightVM inserts a LightVM definition. Interface attachments are
// managed separately by SetLightVMInterfaces.
func (s *Store) CreateLightVM(ctx context.Context, vm LightVM) (LightVM, error) {
	if strings.TrimSpace(vm.ID) == "" {
		vm.ID = uuid.NewString()
	}
	if _, err := s.GetLightVMKernel(ctx, vm.KernelID); err != nil {
		return LightVM{}, err
	}
	if _, err := s.GetLightVMInitramfs(ctx, vm.InitramfsID); err != nil {
		return LightVM{}, err
	}
	if strings.TrimSpace(vm.Append) == "" {
		vm.Append = DefaultLightVMAppend
	}
	if vm.Status == "" {
		vm.Status = StatusStopped
	}
	if vm.DesiredState == "" {
		vm.DesiredState = DesiredStopped
	}
	interfaceIDs, err := s.normalizeLightVMInterfaceIDs(ctx, vm.ID, vm.InterfaceIDs)
	if err != nil {
		return LightVM{}, err
	}
	envJSON, err := marshalLightVMEnv(vm.Env)
	if err != nil {
		return LightVM{}, err
	}
	argsJSON, err := marshalLightVMArgs(vm.ExecutableArgs)
	if err != nil {
		return LightVM{}, err
	}

	now := time.Now().UTC().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LightVM{}, fmt.Errorf("begin create lightvm: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO lightvms
			(id, name, description, vcpus, memory_mb, console_enabled,
			 cgroup_class, kernel_id, initramfs_id, image, env,
			 executable_path, executable_args, append, status, desired_state,
			 created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		vm.ID, vm.Name, vm.Description, vm.VCPUs, vm.MemoryMB, vm.ConsoleEnabled,
		vm.CgroupClass, vm.KernelID, vm.InitramfsID, vm.Image, envJSON,
		vm.ExecutablePath, argsJSON, vm.Append, vm.Status, vm.DesiredState, now, now,
	); err != nil {
		if isUniqueViolation(err) {
			return LightVM{}, fmt.Errorf("%w: %q", ErrLightVMNameTaken, vm.Name)
		}
		return LightVM{}, fmt.Errorf("insert lightvm: %w", err)
	}
	if err := insertLightVMInterfaces(ctx, tx, vm.ID, interfaceIDs); err != nil {
		return LightVM{}, err
	}
	if err := tx.Commit(); err != nil {
		return LightVM{}, fmt.Errorf("commit create lightvm: %w", err)
	}
	return s.GetLightVM(ctx, vm.ID)
}

// UpdateLightVM replaces the editable fields of a LightVM. Interface
// attachments are managed separately by SetLightVMInterfaces.
func (s *Store) UpdateLightVM(ctx context.Context, vm LightVM) (LightVM, error) {
	if _, err := s.GetLightVMKernel(ctx, vm.KernelID); err != nil {
		return LightVM{}, err
	}
	if _, err := s.GetLightVMInitramfs(ctx, vm.InitramfsID); err != nil {
		return LightVM{}, err
	}
	interfaceIDs, err := s.normalizeLightVMInterfaceIDs(ctx, vm.ID, vm.InterfaceIDs)
	if err != nil {
		return LightVM{}, err
	}
	envJSON, err := marshalLightVMEnv(vm.Env)
	if err != nil {
		return LightVM{}, err
	}
	argsJSON, err := marshalLightVMArgs(vm.ExecutableArgs)
	if err != nil {
		return LightVM{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LightVM{}, fmt.Errorf("begin update lightvm: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `
		UPDATE lightvms
		SET name = ?, description = ?, vcpus = ?, memory_mb = ?,
		    console_enabled = ?, cgroup_class = ?, kernel_id = ?,
		    initramfs_id = ?, image = ?, env = ?, executable_path = ?,
		    executable_args = ?, append = ?, updated_at = ?
		WHERE id = ?`,
		vm.Name, vm.Description, vm.VCPUs, vm.MemoryMB,
		vm.ConsoleEnabled, vm.CgroupClass, vm.KernelID,
		vm.InitramfsID, vm.Image, envJSON, vm.ExecutablePath,
		argsJSON, vm.Append, time.Now().UTC().UnixMilli(), vm.ID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return LightVM{}, fmt.Errorf("%w: %q", ErrLightVMNameTaken, vm.Name)
		}
		return LightVM{}, fmt.Errorf("update lightvm: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return LightVM{}, fmt.Errorf("read updated lightvm rows: %w", err)
	}
	if affected == 0 {
		return LightVM{}, ErrLightVMNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM lightvm_interfaces WHERE lightvm_id = ?`, vm.ID); err != nil {
		return LightVM{}, fmt.Errorf("clear lightvm interfaces: %w", err)
	}
	if err := insertLightVMInterfaces(ctx, tx, vm.ID, interfaceIDs); err != nil {
		return LightVM{}, err
	}
	if err := tx.Commit(); err != nil {
		return LightVM{}, fmt.Errorf("commit update lightvm: %w", err)
	}
	return s.GetLightVM(ctx, vm.ID)
}

// SetLightVMStatus updates only the runtime status of a LightVM.
func (s *Store) SetLightVMStatus(ctx context.Context, id, status string) (LightVM, error) {
	if !ValidStatus(status) {
		return LightVM{}, fmt.Errorf("invalid status %q", status)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE lightvms SET status = ?, updated_at = ? WHERE id = ?`,
		status, time.Now().UTC().UnixMilli(), id,
	)
	if err != nil {
		return LightVM{}, fmt.Errorf("update lightvm status: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return LightVM{}, fmt.Errorf("read updated lightvm rows: %w", err)
	}
	if affected == 0 {
		return LightVM{}, ErrLightVMNotFound
	}
	return s.GetLightVM(ctx, id)
}

// SetLightVMDesiredState records whether a LightVM should be running after an
// API or host restart.
func (s *Store) SetLightVMDesiredState(ctx context.Context, id, state string) (LightVM, error) {
	if !ValidDesiredState(state) {
		return LightVM{}, fmt.Errorf("invalid desired state %q", state)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE lightvms SET desired_state = ?, updated_at = ? WHERE id = ?`,
		state, time.Now().UTC().UnixMilli(), id,
	)
	if err != nil {
		return LightVM{}, fmt.Errorf("update lightvm desired state: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return LightVM{}, fmt.Errorf("read updated lightvm rows: %w", err)
	}
	if affected == 0 {
		return LightVM{}, ErrLightVMNotFound
	}
	return s.GetLightVM(ctx, id)
}

// DeleteLightVM removes a LightVM and its interface join rows.
func (s *Store) DeleteLightVM(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM lightvms WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete lightvm: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted lightvm rows: %w", err)
	}
	if affected == 0 {
		return ErrLightVMNotFound
	}
	return nil
}

// normalizeLightVMInterfaceIDs deduplicates interface IDs and validates that
// each interface exists, is not attached to a VM, and is not owned by another
// LightVM.
func (s *Store) normalizeLightVMInterfaceIDs(ctx context.Context, lightVMID string, interfaceIDs []int64) ([]int64, error) {
	seen := make(map[int64]struct{}, len(interfaceIDs))
	unique := make([]int64, 0, len(interfaceIDs))
	for _, interfaceID := range interfaceIDs {
		if interfaceID < 1 {
			return nil, ErrInterfaceNotFound
		}
		if _, ok := seen[interfaceID]; ok {
			continue
		}
		seen[interfaceID] = struct{}{}
		unique = append(unique, interfaceID)

		iface, err := s.GetInterface(ctx, interfaceID)
		if err != nil {
			return nil, err
		}
		if iface.AttachedVMID != nil {
			return nil, ErrInterfaceInUse
		}
		if err := s.checkLightVMInterfaceUse(ctx, interfaceID, lightVMID); err != nil {
			return nil, err
		}
	}
	return unique, nil
}

func insertLightVMInterfaces(ctx context.Context, tx *sql.Tx, lightVMID string, interfaceIDs []int64) error {
	for position, interfaceID := range interfaceIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO lightvm_interfaces (lightvm_id, interface_id, position)
			VALUES (?, ?, ?)`, lightVMID, interfaceID, position); err != nil {
			if isForeignKeyViolation(err) {
				return ErrInterfaceInUse
			}
			return fmt.Errorf("attach lightvm interface: %w", err)
		}
	}
	return nil
}

// loadLightVMInterfaces fills LightVM.Interfaces and InterfaceIDs for the
// supplied records.
func (s *Store) loadLightVMInterfaces(ctx context.Context, vms []LightVM) error {
	if len(vms) == 0 {
		return nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT li.lightvm_id, i.id, i.name, i.network_id, n.name,
		       i.socket_path, i.mac, i.sw_if_index, ai.name, ae.name
		FROM lightvm_interfaces AS li
		JOIN virtual_interfaces AS i ON i.id = li.interface_id
		JOIN networks AS n ON n.id = i.network_id
		LEFT JOIN acls AS ai ON ai.id = i.ingress_acl_id
		LEFT JOIN acls AS ae ON ae.id = i.egress_acl_id
		ORDER BY li.lightvm_id, li.position, i.id`)
	if err != nil {
		return fmt.Errorf("query lightvm interfaces: %w", err)
	}
	defer rows.Close()

	byVM := make(map[string][]VMInterface)
	for rows.Next() {
		var (
			vmID    string
			iface   VMInterface
			swIf    int64
			ingress sql.NullString
			egress  sql.NullString
		)
		if err := rows.Scan(
			&vmID, &iface.ID, &iface.Name, &iface.NetworkID, &iface.NetworkName,
			&iface.SocketPath, &iface.MAC, &swIf, &ingress, &egress,
		); err != nil {
			return err
		}
		iface.SwIfIndex = uint32(swIf)
		if ingress.Valid {
			iface.IngressACLName = ingress.String
		}
		if egress.Valid {
			iface.EgressACLName = egress.String
		}
		byVM[vmID] = append(byVM[vmID], iface)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate lightvm interfaces: %w", err)
	}

	for i := range vms {
		vms[i].Interfaces = byVM[vms[i].ID]
		vms[i].InterfaceIDs = make([]int64, 0, len(vms[i].Interfaces))
		for _, iface := range vms[i].Interfaces {
			vms[i].InterfaceIDs = append(vms[i].InterfaceIDs, iface.ID)
		}
	}
	return nil
}

// checkLightVMInterfaceUse reports whether an interface belongs to a LightVM
// other than exceptLightVMID. An empty exception rejects every attachment.
func (s *Store) checkLightVMInterfaceUse(ctx context.Context, interfaceID int64, exceptLightVMID string) error {
	query := `SELECT lightvm_id FROM lightvm_interfaces WHERE interface_id = ?`
	args := []any{interfaceID}
	if strings.TrimSpace(exceptLightVMID) != "" {
		query += ` AND lightvm_id <> ?`
		args = append(args, exceptLightVMID)
	}
	var owner string
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&owner)
	switch {
	case err == nil:
		return ErrInterfaceInUse
	case errors.Is(err, sql.ErrNoRows):
		return nil
	default:
		return fmt.Errorf("check lightvm interface usage: %w", err)
	}
}

func scanLightVM(s scanner) (LightVM, error) {
	var (
		vm        LightVM
		envJSON   string
		argsJSON  string
		createdAt int64
		updatedAt int64
	)
	if err := s.Scan(
		&vm.ID, &vm.Name, &vm.Description, &vm.VCPUs, &vm.MemoryMB,
		&vm.ConsoleEnabled, &vm.CgroupClass, &vm.KernelID, &vm.KernelName,
		&vm.KernelPath, &vm.InitramfsID, &vm.InitramfsName, &vm.InitramfsPath,
		&vm.Image, &envJSON, &vm.ExecutablePath, &argsJSON, &vm.Append,
		&vm.Status, &vm.DesiredState, &createdAt, &updatedAt,
	); err != nil {
		return LightVM{}, err
	}
	if err := json.Unmarshal([]byte(envJSON), &vm.Env); err != nil {
		return LightVM{}, fmt.Errorf("decode lightvm environment: %w", err)
	}
	if err := json.Unmarshal([]byte(argsJSON), &vm.ExecutableArgs); err != nil {
		return LightVM{}, fmt.Errorf("decode lightvm arguments: %w", err)
	}
	if vm.Env == nil {
		vm.Env = []LightVMEnvVar{}
	}
	if vm.ExecutableArgs == nil {
		vm.ExecutableArgs = []string{}
	}
	vm.CreatedAt = time.UnixMilli(createdAt).UTC()
	vm.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return vm, nil
}

func marshalLightVMEnv(values []LightVMEnvVar) (string, error) {
	if values == nil {
		values = []LightVMEnvVar{}
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("encode lightvm environment: %w", err)
	}
	return string(data), nil
}

func marshalLightVMArgs(values []string) (string, error) {
	if values == nil {
		values = []string{}
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("encode lightvm arguments: %w", err)
	}
	return string(data), nil
}
