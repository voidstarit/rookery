// Package store persists VM and disk records in SQLite.
package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

var (
	// ErrNotFound is returned when a VM does not exist.
	ErrNotFound = errors.New("vm not found")
	// ErrNameTaken is returned when a VM name is already in use.
	ErrNameTaken = errors.New("vm name already exists")
	// ErrDiskNotFound is returned when a disk does not exist.
	ErrDiskNotFound = errors.New("disk not found")
	// ErrDiskNameTaken is returned when a disk name is already in use.
	ErrDiskNameTaken = errors.New("disk name already exists")
	// ErrDiskInUse is returned when a disk is attached to a VM.
	ErrDiskInUse = errors.New("disk is attached to a vm")
	// ErrDiskIsTemplate is returned when a template disk is attached to a VM.
	ErrDiskIsTemplate = errors.New("template disks cannot be attached to a vm")
	// ErrStorageBackendNotFound is returned when a storage backend does not exist.
	ErrStorageBackendNotFound = errors.New("storage backend not found")
	// ErrStorageBackendNameTaken is returned when a backend name is already in use.
	ErrStorageBackendNameTaken = errors.New("storage backend name already exists")
	// ErrStorageBackendInUse is returned when a backend still has disks.
	ErrStorageBackendInUse = errors.New("storage backend has disks")
	// ErrMediaNotFound is returned when an installation media entry does not exist.
	ErrMediaNotFound = errors.New("media not found")
	// ErrMediaNameTaken is returned when a media name is already in use.
	ErrMediaNameTaken = errors.New("media name already exists")
	// ErrMediaInUse is returned when media is set as a VM's boot media.
	ErrMediaInUse = errors.New("media is used as boot media")
	// ErrNetworkNotFound is returned when a network does not exist.
	ErrNetworkNotFound = errors.New("network not found")
	// ErrNetworkNameTaken is returned when a network name is already in use.
	ErrNetworkNameTaken = errors.New("network name already exists")
	// ErrNetworkInUse is returned when a network still has virtual interfaces.
	ErrNetworkInUse = errors.New("network has virtual interfaces")
	// ErrInterfaceNotFound is returned when a virtual interface does not exist.
	ErrInterfaceNotFound = errors.New("interface not found")
	// ErrInterfaceNameTaken is returned when an interface name is already in use.
	ErrInterfaceNameTaken = errors.New("interface name already exists")
	// ErrInterfaceInUse is returned when a virtual interface is attached to a VM.
	ErrInterfaceInUse = errors.New("interface is attached to a vm")
	// ErrVNFNotFound is returned when a network function does not exist.
	ErrVNFNotFound = errors.New("vnf not found")
	// ErrVNFNameTaken is returned when a network function name is already in use.
	ErrVNFNameTaken = errors.New("vnf name already exists")
	// ErrACLNotFound is returned when an ACL does not exist.
	ErrACLNotFound = errors.New("acl not found")
	// ErrACLNameTaken is returned when an ACL name is already in use.
	ErrACLNameTaken = errors.New("acl name already exists")
	// ErrACLInUse is returned when an ACL is assigned to an interface.
	ErrACLInUse = errors.New("acl is assigned to an interface")
)

const (
	StatusStopped  = "stopped"
	StatusStarting = "starting"
	StatusRunning  = "running"
	StatusStopping = "stopping"
	StatusPaused   = "paused"
	StatusError    = "error"
)

var validStatuses = map[string]struct{}{
	StatusStopped:  {},
	StatusStarting: {},
	StatusRunning:  {},
	StatusStopping: {},
	StatusPaused:   {},
	StatusError:    {},
}

// ValidStatus reports whether s is a status understood by the API.
func ValidStatus(s string) bool {
	_, ok := validStatuses[s]
	return ok
}

// Statuses returns the accepted VM statuses in display order.
func Statuses() []string {
	return []string{
		StatusStopped,
		StatusStarting,
		StatusRunning,
		StatusStopping,
		StatusPaused,
		StatusError,
	}
}

// VM is a virtual machine record.
type VM struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	Description    string        `json:"description"`
	Status         string        `json:"status"`
	VCPUs          int           `json:"vcpus"`
	MemoryMB       int           `json:"memory_mb"`
	DiskID         *int64        `json:"disk_id,omitempty"`
	Disk           *VMDisk       `json:"disk,omitempty"`
	DiskIDs        []int64       `json:"disk_ids,omitempty"`
	Disks          []VMDisk      `json:"disks,omitempty"`
	BootMediaID    *int64        `json:"boot_media_id,omitempty"`
	BootMedia      *VMMedia      `json:"boot_media,omitempty"`
	Interfaces     []VMInterface `json:"interfaces,omitempty"`
	ConsoleEnabled bool          `json:"console_enabled"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
}

// VMDisk is the disk summary returned with a VM.
type VMDisk struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	SizeGB             int    `json:"size_gb"`
	StorageBackendID   *int64 `json:"storage_backend_id,omitempty"`
	StorageBackendName string `json:"storage_backend_name,omitempty"`
	VG                 string `json:"vg"`
	ThinPool           string `json:"thin_pool"`
	Device             string `json:"device"`
}

// VMMedia is the boot media summary returned with a VM.
type VMMedia struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
}

// Network is a VPP bridge domain presented as a Nerd's Network.
type Network struct {
	ID               int64     `json:"id"`
	Name             string    `json:"name"`
	BridgeDomainID   uint32    `json:"bridge_domain_id"`
	InterfaceCount   int       `json:"interface_count"`
	DHCPEnabled      bool      `json:"-"`
	DHCPGateway      string    `json:"-"`
	DHCPRangeStart   string    `json:"-"`
	DHCPRangeEnd     string    `json:"-"`
	DHCPDNS          string    `json:"-"`
	DHCPTapSwIfIndex uint32    `json:"-"`
	DHCPTapHostName  string    `json:"-"`
	BVIHostName      string    `json:"bvi_host_name,omitempty"`
	BVISwIfIndex     uint32    `json:"bvi_sw_if_index,omitempty"`
	IPv4Gateway      string    `json:"ipv4_gateway,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

const (
	// VNFTypeDHCPv4 is a DHCPv4 server function.
	VNFTypeDHCPv4 = "dhcpv4"
	// VNFTypeDHCPv6 is a DHCPv6 server function.
	VNFTypeDHCPv6 = "dhcpv6"
	// VNFTypeNAT44 is a VPP NAT44 function.
	VNFTypeNAT44 = "nat44"
)

// ValidVNFType reports whether type is a supported network function.
func ValidVNFType(vnfType string) bool {
	return vnfType == VNFTypeDHCPv4 || vnfType == VNFTypeDHCPv6 || vnfType == VNFTypeNAT44
}

// VNFConfig is the shared configuration for DHCP network functions. IPv4 and
// IPv6 functions use the same fields with addresses in the corresponding family.
type VNFConfig struct {
	Gateway    string `json:"gateway"`
	RangeStart string `json:"range_start"`
	RangeEnd   string `json:"range_end"`
	DNS        string `json:"dns,omitempty"`
	LeaseTime  string `json:"lease_time,omitempty"`

	// BindIP is the address assigned to a DHCP VNF's Linux tap. It is normally
	// empty (the gateway is used). On a NAT network it is the .2 address so the
	// BVI can own the .1 gateway.
	BindIP string `json:"bind_ip,omitempty"`

	// StaticAssignments reserve IPv4 addresses for specific MAC addresses.
	StaticAssignments []DHCPStaticAssignment `json:"static_assignments,omitempty"`

	// NAT44 configuration.
	OutsideInterface string `json:"outside_interface,omitempty"`
	OutsideAddress   string `json:"outside_address,omitempty"`
	OutsideGateway   string `json:"outside_gateway,omitempty"`
	NAT44Pool        string `json:"nat44_pool,omitempty"`

	// DNATRules expose services on the NAT44 network to clients on the
	// outside interface. The external address is the outside interface's
	// address, so rules keep working when the address is DHCP-assigned.
	DNATRules []DNATRule `json:"dnat_rules,omitempty"`
}

// DHCPStaticAssignment reserves one IPv4 address for a MAC address.
type DHCPStaticAssignment struct {
	MAC string `json:"mac"`
	IP  string `json:"ip"`
}

// DNATRule forwards one external TCP or UDP port to an address inside a NAT44
// network.
type DNATRule struct {
	Protocol     string `json:"protocol"`
	ExternalPort int    `json:"external_port"`
	LocalIP      string `json:"local_ip"`
	LocalPort    int    `json:"local_port"`
}

// VNF is a network function attached to a Nerd's Network.
type VNF struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	NetworkID      int64     `json:"network_id"`
	NetworkName    string    `json:"network_name"`
	Type           string    `json:"type"`
	Enabled        bool      `json:"enabled"`
	Config         VNFConfig `json:"config"`
	TapSwIfIndex   uint32    `json:"tap_sw_if_index,omitempty"`
	TapHostName    string    `json:"tap_host_name,omitempty"`
	MemifSwIfIndex uint32    `json:"memif_sw_if_index,omitempty"`
	MemifSocket    string    `json:"memif_socket,omitempty"`
	MemifHostName  string    `json:"memif_host_name,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// VNFLease is one address handed out by a DHCP network function.
type VNFLease struct {
	ClientID  string    `json:"client_id"`
	Address   string    `json:"address"`
	Hostname  string    `json:"hostname,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
	Active    bool      `json:"active"`
}

// ACLRule is one ordered permit/deny rule in an ACL.
type ACLRule struct {
	Action               string `json:"action"`
	Description          string `json:"description,omitempty"`
	Protocol             string `json:"protocol"`
	Source               string `json:"source"`
	Destination          string `json:"destination"`
	SourcePortStart      int    `json:"source_port_start,omitempty"`
	SourcePortEnd        int    `json:"source_port_end,omitempty"`
	DestinationPortStart int    `json:"destination_port_start,omitempty"`
	DestinationPortEnd   int    `json:"destination_port_end,omitempty"`
}

// ACL is a named ordered rule set used as an interface ingress or egress ACL.
type ACL struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Rules       []ACLRule `json:"rules"`
	VPPIndex    uint32    `json:"vpp_index,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// VirtualInterface is a VPP vhost-user interface belonging to a network.
type VirtualInterface struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	NetworkID      int64     `json:"network_id"`
	NetworkName    string    `json:"network_name"`
	SocketPath     string    `json:"socket_path"`
	MAC            string    `json:"mac"`
	SwIfIndex      uint32    `json:"sw_if_index"`
	AttachedVMID   *string   `json:"attached_vm_id,omitempty"`
	AttachedVMName string    `json:"attached_vm_name,omitempty"`
	IngressACLID   *int64    `json:"ingress_acl_id,omitempty"`
	IngressACLName string    `json:"ingress_acl_name,omitempty"`
	EgressACLID    *int64    `json:"egress_acl_id,omitempty"`
	EgressACLName  string    `json:"egress_acl_name,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// VMInterface is the network interface summary returned with a VM.
type VMInterface struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	NetworkID      int64  `json:"network_id"`
	NetworkName    string `json:"network_name"`
	SocketPath     string `json:"socket_path"`
	MAC            string `json:"mac"`
	SwIfIndex      uint32 `json:"sw_if_index"`
	IngressACLName string `json:"ingress_acl_name,omitempty"`
	EgressACLName  string `json:"egress_acl_name,omitempty"`
}

// Disk is a pre-created thin-LVM disk that can be attached to a VM.
type Disk struct {
	ID                 int64     `json:"id"`
	Name               string    `json:"name"`
	SizeGB             int       `json:"size_gb"`
	StorageBackendID   *int64    `json:"storage_backend_id,omitempty"`
	StorageBackendName string    `json:"storage_backend_name,omitempty"`
	VG                 string    `json:"vg"`
	ThinPool           string    `json:"thin_pool"`
	Device             string    `json:"device"`
	IsTemplate         bool      `json:"is_template"`
	TemplateDiskID     *int64    `json:"template_disk_id,omitempty"`
	TemplateDiskName   string    `json:"template_disk_name,omitempty"`
	PreparedByScript   bool      `json:"-"`
	AttachedVMID       *string   `json:"attached_vm_id,omitempty"`
	AttachedVMName     string    `json:"attached_vm_name,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// ImportDisk describes an existing volume that should be added to inventory.
type ImportDisk struct {
	Name   string
	SizeGB int
}

// Media is a read-only installation file (usually an ISO) on the host.
type Media struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store is a SQLite-backed VM and disk store.
type Store struct {
	db *sql.DB
}

// Open opens (and creates, if necessary) the SQLite database at dbPath and
// applies any pending embedded migrations.
func Open(dbPath string) (*Store, error) {
	if strings.TrimSpace(dbPath) == "" {
		return nil, errors.New("database path is required")
	}

	if dbPath != ":memory:" {
		abs, err := filepath.Abs(dbPath)
		if err != nil {
			return nil, fmt.Errorf("resolve database path: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// SQLite allows a single writer. Serializing access keeps the simple API
	// free of SQLITE_BUSY retries and is plenty for one management server.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	for _, pragma := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA foreign_keys = ON",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("apply %q: %w", pragma, err)
		}
	}

	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}

// Ping verifies the database is reachable.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// CreateVM inserts a VM and returns the stored record.
func (s *Store) CreateVM(ctx context.Context, vm VM) (VM, error) {
	if vm.ID == "" {
		vm.ID = uuid.NewString()
	}
	diskIDs := vm.DiskIDs
	if len(diskIDs) == 0 && vm.DiskID != nil {
		diskIDs = []int64{*vm.DiskID}
	}
	diskIDs, err := normalizeDiskIDs(diskIDs)
	if err != nil {
		return VM{}, err
	}
	for _, diskID := range diskIDs {
		if err := s.checkDiskAvailable(ctx, diskID, ""); err != nil {
			return VM{}, err
		}
	}
	if err := s.checkBootMedia(ctx, vm.BootMediaID); err != nil {
		return VM{}, err
	}
	vm.DiskID = &diskIDs[0]
	vm.DiskIDs = diskIDs

	now := time.Now().UTC().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return VM{}, fmt.Errorf("begin create vm: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO vms (id, name, description, status, vcpus, memory_mb, disk_id, boot_media_id, console_enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		vm.ID, vm.Name, vm.Description, vm.Status, vm.VCPUs, vm.MemoryMB, diskIDs[0], vm.BootMediaID, vm.ConsoleEnabled, now, now,
	); err != nil {
		if constraintErr := classifyVMCreateError(err, vm.Name); constraintErr != nil {
			return VM{}, constraintErr
		}
		return VM{}, fmt.Errorf("insert vm: %w", err)
	}
	for position, diskID := range diskIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO vm_disks (vm_id, disk_id, position) VALUES (?, ?, ?)`,
			vm.ID, diskID, position,
		); err != nil {
			if isForeignKeyViolation(err) {
				return VM{}, ErrDiskInUse
			}
			return VM{}, fmt.Errorf("attach vm disk: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return VM{}, fmt.Errorf("commit create vm: %w", err)
	}
	return s.GetVM(ctx, vm.ID)
}

// SetVMDisks replaces the disks attached to a stopped VM.
func (s *Store) SetVMDisks(ctx context.Context, vmID string, diskIDs []int64) (VM, error) {
	if _, err := s.GetVM(ctx, vmID); err != nil {
		return VM{}, err
	}
	diskIDs, err := normalizeDiskIDs(diskIDs)
	if err != nil {
		return VM{}, err
	}
	for _, diskID := range diskIDs {
		if err := s.checkDiskAvailable(ctx, diskID, vmID); err != nil {
			return VM{}, err
		}
	}
	now := time.Now().UTC().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return VM{}, fmt.Errorf("begin update vm disks: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM vm_disks WHERE vm_id = ?`, vmID); err != nil {
		return VM{}, fmt.Errorf("clear vm disks: %w", err)
	}
	for position, diskID := range diskIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO vm_disks (vm_id, disk_id, position) VALUES (?, ?, ?)`,
			vmID, diskID, position,
		); err != nil {
			if isForeignKeyViolation(err) {
				return VM{}, ErrDiskInUse
			}
			return VM{}, fmt.Errorf("attach vm disk: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE vms SET disk_id = ?, updated_at = ? WHERE id = ?`,
		diskIDs[0], now, vmID,
	); err != nil {
		return VM{}, fmt.Errorf("update primary vm disk: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return VM{}, fmt.Errorf("commit update vm disks: %w", err)
	}
	return s.GetVM(ctx, vmID)
}

func normalizeDiskIDs(ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, errors.New("at least one disk is required")
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

// ListVMs returns all VMs ordered by name.
func (s *Store) ListVMs(ctx context.Context) ([]VM, error) {
	rows, err := s.db.QueryContext(ctx, vmSelect+`
		ORDER BY v.name COLLATE NOCASE, v.id`)
	if err != nil {
		return nil, fmt.Errorf("query vms: %w", err)
	}
	defer rows.Close()

	vms := make([]VM, 0)
	for rows.Next() {
		vm, err := scanVM(rows)
		if err != nil {
			return nil, err
		}
		vms = append(vms, vm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vms: %w", err)
	}
	if err := s.loadVMDisks(ctx, vms); err != nil {
		return nil, err
	}
	if err := s.loadVMInterfaces(ctx, vms); err != nil {
		return nil, err
	}

	return vms, nil
}

// GetVM returns a single VM by ID.
func (s *Store) GetVM(ctx context.Context, id string) (VM, error) {
	row := s.db.QueryRowContext(ctx, vmSelect+`
		WHERE v.id = ?`, id)

	vm, err := scanVM(row)
	if errors.Is(err, sql.ErrNoRows) {
		return VM{}, ErrNotFound
	}
	if err != nil {
		return VM{}, err
	}
	items := []VM{vm}
	if err := s.loadVMDisks(ctx, items); err != nil {
		return VM{}, err
	}
	if err := s.loadVMInterfaces(ctx, items); err != nil {
		return VM{}, err
	}
	return items[0], nil
}

// UpdateVM replaces the editable fields of an existing VM. Disk attachments are
// managed separately by SetVMDisks.
func (s *Store) UpdateVM(ctx context.Context, vm VM) (VM, error) {
	if err := s.checkBootMedia(ctx, vm.BootMediaID); err != nil {
		return VM{}, err
	}

	now := time.Now().UTC().UnixMilli()

	res, err := s.db.ExecContext(ctx, `
		UPDATE vms
		SET name = ?, description = ?, status = ?, vcpus = ?, memory_mb = ?, boot_media_id = ?, console_enabled = ?, updated_at = ?
		WHERE id = ?`,
		vm.Name, vm.Description, vm.Status, vm.VCPUs, vm.MemoryMB, vm.BootMediaID, vm.ConsoleEnabled, now, vm.ID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return VM{}, fmt.Errorf("%w: %q", ErrNameTaken, vm.Name)
		}
		return VM{}, fmt.Errorf("update vm: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return VM{}, fmt.Errorf("read updated rows: %w", err)
	}
	if affected == 0 {
		return VM{}, ErrNotFound
	}

	return s.GetVM(ctx, vm.ID)
}

// SetVMStatus updates only the runtime status of a VM.
func (s *Store) SetVMStatus(ctx context.Context, id, status string) (VM, error) {
	now := time.Now().UTC().UnixMilli()

	res, err := s.db.ExecContext(ctx, `
		UPDATE vms SET status = ?, updated_at = ? WHERE id = ?`,
		status, now, id,
	)
	if err != nil {
		return VM{}, fmt.Errorf("update vm status: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return VM{}, fmt.Errorf("read updated rows: %w", err)
	}
	if affected == 0 {
		return VM{}, ErrNotFound
	}

	return s.GetVM(ctx, id)
}

// DeleteVM deletes a VM by ID, releasing its disks and network interfaces.
func (s *Store) DeleteVM(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete vm: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx,
		`UPDATE virtual_interfaces SET attached_vm_id = NULL, updated_at = ? WHERE attached_vm_id = ?`,
		now, id,
	); err != nil {
		return fmt.Errorf("detach vm interfaces: %w", err)
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM vms WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete vm: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// CreateDisk inserts a disk record and returns the stored record.
func (s *Store) CreateDisk(ctx context.Context, disk Disk) (Disk, error) {
	now := time.Now().UTC().UnixMilli()

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO disks (name, size_gb, vg, thin_pool, storage_backend_id, is_template, template_disk_id, prepared_by_script, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		disk.Name, disk.SizeGB, disk.VG, disk.ThinPool, disk.StorageBackendID, disk.IsTemplate, disk.TemplateDiskID, disk.PreparedByScript, now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return Disk{}, fmt.Errorf("%w: %q", ErrDiskNameTaken, disk.Name)
		}
		return Disk{}, fmt.Errorf("insert disk: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Disk{}, fmt.Errorf("read inserted disk id: %w", err)
	}

	return s.GetDisk(ctx, id)
}

// ImportDisks registers existing volumes as disks in a single transaction.
func (s *Store) ImportDisks(ctx context.Context, backendID int64, vg, thinPool string, imports []ImportDisk) ([]Disk, error) {
	if len(imports) == 0 {
		return nil, errors.New("at least one disk is required")
	}

	now := time.Now().UTC().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin import disks: %w", err)
	}
	defer tx.Rollback()

	ids := make([]int64, 0, len(imports))
	for _, item := range imports {
		if item.SizeGB < 1 {
			return nil, fmt.Errorf("disk %q must be at least 1 GB", item.Name)
		}
		res, err := tx.ExecContext(ctx, `
			INSERT INTO disks (name, size_gb, vg, thin_pool, storage_backend_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			item.Name, item.SizeGB, vg, thinPool, backendID, now, now,
		)
		if err != nil {
			if isUniqueViolation(err) {
				return nil, fmt.Errorf("%w: %q", ErrDiskNameTaken, item.Name)
			}
			return nil, fmt.Errorf("insert imported disk %q: %w", item.Name, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("read imported disk id for %q: %w", item.Name, err)
		}
		ids = append(ids, id)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit import disks: %w", err)
	}

	disks := make([]Disk, 0, len(ids))
	for _, id := range ids {
		disk, err := s.GetDisk(ctx, id)
		if err != nil {
			return nil, err
		}
		disks = append(disks, disk)
	}
	return disks, nil
}

// ListDisks returns all disks ordered by name.
func (s *Store) ListDisks(ctx context.Context) ([]Disk, error) {
	rows, err := s.db.QueryContext(ctx, diskSelect+`
		ORDER BY d.name COLLATE NOCASE, d.id`)
	if err != nil {
		return nil, fmt.Errorf("query disks: %w", err)
	}
	defer rows.Close()

	disks := make([]Disk, 0)
	for rows.Next() {
		disk, err := scanDisk(rows)
		if err != nil {
			return nil, err
		}
		disks = append(disks, disk)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate disks: %w", err)
	}

	return disks, nil
}

// GetDisk returns a single disk by ID.
func (s *Store) GetDisk(ctx context.Context, id int64) (Disk, error) {
	row := s.db.QueryRowContext(ctx, diskSelect+`
		WHERE d.id = ?`, id)

	disk, err := scanDisk(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Disk{}, ErrDiskNotFound
	}
	if err != nil {
		return Disk{}, err
	}
	return disk, nil
}

// GetDiskByName returns a single disk by case-insensitive name.
func (s *Store) GetDiskByName(ctx context.Context, name string) (Disk, error) {
	row := s.db.QueryRowContext(ctx, diskSelect+`
		WHERE d.name = ?`, name)

	disk, err := scanDisk(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Disk{}, ErrDiskNotFound
	}
	if err != nil {
		return Disk{}, err
	}
	return disk, nil
}

// SetDiskTemplate marks or unmarks an unattached disk as a template.
func (s *Store) SetDiskTemplate(ctx context.Context, id int64, isTemplate bool) (Disk, error) {
	disk, err := s.GetDisk(ctx, id)
	if err != nil {
		return Disk{}, err
	}
	if disk.AttachedVMID != nil {
		return Disk{}, ErrDiskInUse
	}
	now := time.Now().UTC().UnixMilli()
	if _, err := s.db.ExecContext(ctx, `UPDATE disks SET is_template = ?, updated_at = ? WHERE id = ?`, isTemplate, now, id); err != nil {
		return Disk{}, fmt.Errorf("update disk template flag: %w", err)
	}
	return s.GetDisk(ctx, id)
}

// CountDiskClones returns the number of disks created from a template.
func (s *Store) CountDiskClones(ctx context.Context, id int64) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM disks WHERE template_disk_id = ?`, id).Scan(&count); err != nil {
		return 0, fmt.Errorf("count disk clones: %w", err)
	}
	return count, nil
}

// DeleteDisk deletes an unattached disk record.
func (s *Store) DeleteDisk(ctx context.Context, id int64) error {
	disk, err := s.GetDisk(ctx, id)
	if err != nil {
		return err
	}
	if disk.AttachedVMID != nil {
		return ErrDiskInUse
	}

	res, err := s.db.ExecContext(ctx, `DELETE FROM disks WHERE id = ?`, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return ErrDiskInUse
		}
		return fmt.Errorf("delete disk: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted rows: %w", err)
	}
	if affected == 0 {
		return ErrDiskNotFound
	}
	return nil
}

func (s *Store) checkDiskAvailable(ctx context.Context, diskID int64, exceptVMID string) error {
	disk, err := s.GetDisk(ctx, diskID)
	if err != nil {
		return err
	}
	if disk.IsTemplate {
		return ErrDiskIsTemplate
	}
	if disk.AttachedVMID != nil && *disk.AttachedVMID != exceptVMID {
		return ErrDiskInUse
	}
	return nil
}

// CreateMedia inserts an installation media record and returns it.
func (s *Store) CreateMedia(ctx context.Context, media Media) (Media, error) {
	now := time.Now().UTC().UnixMilli()

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO media (name, path, size_bytes, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`,
		media.Name, media.Path, media.SizeBytes, now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return Media{}, fmt.Errorf("%w: %q", ErrMediaNameTaken, media.Name)
		}
		return Media{}, fmt.Errorf("insert media: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Media{}, fmt.Errorf("read inserted media id: %w", err)
	}

	return s.GetMedia(ctx, id)
}

// ListMedia returns all installation media ordered by name.
func (s *Store) ListMedia(ctx context.Context) ([]Media, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, path, size_bytes, created_at, updated_at
		FROM media
		ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, fmt.Errorf("query media: %w", err)
	}
	defer rows.Close()

	media := make([]Media, 0)
	for rows.Next() {
		item, err := scanMedia(rows)
		if err != nil {
			return nil, err
		}
		media = append(media, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate media: %w", err)
	}

	return media, nil
}

// GetMedia returns a single installation media record by ID.
func (s *Store) GetMedia(ctx context.Context, id int64) (Media, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, path, size_bytes, created_at, updated_at
		FROM media
		WHERE id = ?`, id)

	media, err := scanMedia(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Media{}, ErrMediaNotFound
	}
	if err != nil {
		return Media{}, err
	}
	return media, nil
}

// DeleteMedia removes an installation media record that is not used as boot
// media by any VM. It only removes the inventory entry, never the file.
func (s *Store) DeleteMedia(ctx context.Context, id int64) error {
	var vmID string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM vms WHERE boot_media_id = ? LIMIT 1`, id).Scan(&vmID)
	switch {
	case err == nil:
		return ErrMediaInUse
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("check media usage: %w", err)
	}

	res, err := s.db.ExecContext(ctx, `DELETE FROM media WHERE id = ?`, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return ErrMediaInUse
		}
		return fmt.Errorf("delete media: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted rows: %w", err)
	}
	if affected == 0 {
		return ErrMediaNotFound
	}
	return nil
}

func (s *Store) checkBootMedia(ctx context.Context, mediaID *int64) error {
	if mediaID == nil {
		return nil
	}
	if *mediaID < 1 {
		return ErrMediaNotFound
	}
	_, err := s.GetMedia(ctx, *mediaID)
	return err
}

// CreateNetwork inserts a VPP bridge-domain record.
func (s *Store) CreateNetwork(ctx context.Context, network Network) (Network, error) {
	now := time.Now().UTC().UnixMilli()

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO networks (name, bridge_domain_id, created_at, updated_at)
		VALUES (?, ?, ?, ?)`,
		network.Name, network.BridgeDomainID, now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return Network{}, fmt.Errorf("%w: %q", ErrNetworkNameTaken, network.Name)
		}
		return Network{}, fmt.Errorf("insert network: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Network{}, fmt.Errorf("read inserted network id: %w", err)
	}
	return s.GetNetwork(ctx, id)
}

// ListNetworks returns all networks ordered by name.
func (s *Store) ListNetworks(ctx context.Context) ([]Network, error) {
	rows, err := s.db.QueryContext(ctx, networkSelect+`
		ORDER BY n.name COLLATE NOCASE, n.id`)
	if err != nil {
		return nil, fmt.Errorf("query networks: %w", err)
	}
	defer rows.Close()

	networks := make([]Network, 0)
	for rows.Next() {
		network, err := scanNetwork(rows)
		if err != nil {
			return nil, err
		}
		networks = append(networks, network)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate networks: %w", err)
	}
	return networks, nil
}

// GetNetwork returns one network by ID.
func (s *Store) GetNetwork(ctx context.Context, id int64) (Network, error) {
	row := s.db.QueryRowContext(ctx, networkSelect+`
		WHERE n.id = ?`, id)

	network, err := scanNetwork(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Network{}, ErrNetworkNotFound
	}
	if err != nil {
		return Network{}, err
	}
	return network, nil
}

// SetNetworkDHCP updates the DHCP configuration for a network.
func (s *Store) SetNetworkDHCP(ctx context.Context, id int64, network Network) (Network, error) {
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		UPDATE networks
		SET dhcp_enabled = ?, dhcp_gateway = ?, dhcp_range_start = ?,
		    dhcp_range_end = ?, dhcp_dns = ?, dhcp_tap_sw_if_index = ?,
		    dhcp_tap_host_name = ?, updated_at = ?
		WHERE id = ?`,
		network.DHCPEnabled, network.DHCPGateway, network.DHCPRangeStart,
		network.DHCPRangeEnd, network.DHCPDNS, network.DHCPTapSwIfIndex,
		network.DHCPTapHostName, now, id,
	)
	if err != nil {
		return Network{}, fmt.Errorf("update network dhcp: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return Network{}, fmt.Errorf("read updated rows: %w", err)
	}
	if affected == 0 {
		return Network{}, ErrNetworkNotFound
	}
	return s.GetNetwork(ctx, id)
}

// CreateVNF inserts a network function record.
func (s *Store) CreateVNF(ctx context.Context, vnf VNF) (VNF, error) {
	if _, err := s.GetNetwork(ctx, vnf.NetworkID); err != nil {
		return VNF{}, err
	}
	config, err := json.Marshal(vnf.Config)
	if err != nil {
		return VNF{}, fmt.Errorf("encode vnf config: %w", err)
	}

	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO vnfs
			(name, network_id, type, enabled, config, tap_sw_if_index,
			 tap_host_name, memif_sw_if_index, memif_socket, memif_host_name,
			 created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		vnf.Name, vnf.NetworkID, vnf.Type, vnf.Enabled, string(config),
		vnf.TapSwIfIndex, vnf.TapHostName, vnf.MemifSwIfIndex, vnf.MemifSocket,
		vnf.MemifHostName, now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return VNF{}, fmt.Errorf("%w: %q", ErrVNFNameTaken, vnf.Name)
		}
		return VNF{}, fmt.Errorf("insert vnf: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return VNF{}, fmt.Errorf("read inserted vnf id: %w", err)
	}
	return s.GetVNF(ctx, id)
}

// ListVNFs returns network functions ordered by name, optionally filtered by network.
func (s *Store) ListVNFs(ctx context.Context, networkID *int64) ([]VNF, error) {
	query := vnfSelect
	args := make([]any, 0, 1)
	if networkID != nil {
		query += ` WHERE f.network_id = ?`
		args = append(args, *networkID)
	}
	query += ` ORDER BY f.name COLLATE NOCASE, f.id`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query vnfs: %w", err)
	}
	defer rows.Close()

	vnfs := make([]VNF, 0)
	for rows.Next() {
		vnf, err := scanVNF(rows)
		if err != nil {
			return nil, err
		}
		vnfs = append(vnfs, vnf)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vnfs: %w", err)
	}
	return vnfs, nil
}

// ListVNFsByNetwork returns the network functions attached to one network.
func (s *Store) ListVNFsByNetwork(ctx context.Context, networkID int64) ([]VNF, error) {
	return s.ListVNFs(ctx, &networkID)
}

// GetVNF returns one network function by ID.
func (s *Store) GetVNF(ctx context.Context, id int64) (VNF, error) {
	row := s.db.QueryRowContext(ctx, vnfSelect+`
		WHERE f.id = ?`, id)

	vnf, err := scanVNF(row)
	if errors.Is(err, sql.ErrNoRows) {
		return VNF{}, ErrVNFNotFound
	}
	if err != nil {
		return VNF{}, err
	}
	return vnf, nil
}

// UpdateVNF updates the mutable fields of a network function.
func (s *Store) UpdateVNF(ctx context.Context, id int64, vnf VNF) (VNF, error) {
	config, err := json.Marshal(vnf.Config)
	if err != nil {
		return VNF{}, fmt.Errorf("encode vnf config: %w", err)
	}
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		UPDATE vnfs
		SET name = ?, enabled = ?, config = ?, tap_sw_if_index = ?,
		    tap_host_name = ?, memif_sw_if_index = ?, memif_socket = ?,
		    memif_host_name = ?, updated_at = ?
		WHERE id = ?`,
		vnf.Name, vnf.Enabled, string(config), vnf.TapSwIfIndex,
		vnf.TapHostName, vnf.MemifSwIfIndex, vnf.MemifSocket, vnf.MemifHostName,
		now, id,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return VNF{}, fmt.Errorf("%w: %q", ErrVNFNameTaken, vnf.Name)
		}
		return VNF{}, fmt.Errorf("update vnf: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return VNF{}, fmt.Errorf("read updated rows: %w", err)
	}
	if affected == 0 {
		return VNF{}, ErrVNFNotFound
	}
	return s.GetVNF(ctx, id)
}

// DeleteVNF removes a network function record.
func (s *Store) DeleteVNF(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM vnfs WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete vnf: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted rows: %w", err)
	}
	if affected == 0 {
		return ErrVNFNotFound
	}
	return nil
}

// SetNetworkBVI updates the bridge-domain BVI metadata for a network.
func (s *Store) SetNetworkBVI(ctx context.Context, id int64, hostName string, swIfIndex uint32, gateway string) (Network, error) {
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		UPDATE networks
		SET bvi_host_name = ?, bvi_sw_if_index = ?, ipv4_gateway = ?, updated_at = ?
		WHERE id = ?`,
		hostName, swIfIndex, gateway, now, id,
	)
	if err != nil {
		return Network{}, fmt.Errorf("update network BVI: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return Network{}, fmt.Errorf("read updated rows: %w", err)
	}
	if affected == 0 {
		return Network{}, ErrNetworkNotFound
	}
	return s.GetNetwork(ctx, id)
}

// DeleteNetwork removes a network that has no virtual interfaces.
func (s *Store) DeleteNetwork(ctx context.Context, id int64) error {
	var ifaceID int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM virtual_interfaces WHERE network_id = ? LIMIT 1`, id,
	).Scan(&ifaceID)
	switch {
	case err == nil:
		return ErrNetworkInUse
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("check network usage: %w", err)
	}

	res, err := s.db.ExecContext(ctx, `DELETE FROM networks WHERE id = ?`, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return ErrNetworkInUse
		}
		return fmt.Errorf("delete network: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted rows: %w", err)
	}
	if affected == 0 {
		return ErrNetworkNotFound
	}
	return nil
}

// CreateInterface inserts a vhost-user interface record.
func (s *Store) CreateInterface(ctx context.Context, iface VirtualInterface) (VirtualInterface, error) {
	if _, err := s.GetNetwork(ctx, iface.NetworkID); err != nil {
		return VirtualInterface{}, err
	}
	if iface.AttachedVMID != nil {
		if _, err := s.GetVM(ctx, *iface.AttachedVMID); err != nil {
			return VirtualInterface{}, err
		}
	}

	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO virtual_interfaces
			(name, network_id, socket_path, mac, sw_if_index, attached_vm_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		iface.Name, iface.NetworkID, iface.SocketPath, iface.MAC, iface.SwIfIndex, iface.AttachedVMID, now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return VirtualInterface{}, fmt.Errorf("%w: %q", ErrInterfaceNameTaken, iface.Name)
		}
		return VirtualInterface{}, fmt.Errorf("insert interface: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return VirtualInterface{}, fmt.Errorf("read inserted interface id: %w", err)
	}
	return s.GetInterface(ctx, id)
}

// ListInterfaces returns all virtual interfaces ordered by name.
func (s *Store) ListInterfaces(ctx context.Context) ([]VirtualInterface, error) {
	rows, err := s.db.QueryContext(ctx, interfaceSelect+`
		ORDER BY i.name COLLATE NOCASE, i.id`)
	if err != nil {
		return nil, fmt.Errorf("query interfaces: %w", err)
	}
	defer rows.Close()

	interfaces := make([]VirtualInterface, 0)
	for rows.Next() {
		iface, err := scanInterface(rows)
		if err != nil {
			return nil, err
		}
		interfaces = append(interfaces, iface)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate interfaces: %w", err)
	}
	return interfaces, nil
}

// GetInterface returns one virtual interface by ID.
func (s *Store) GetInterface(ctx context.Context, id int64) (VirtualInterface, error) {
	row := s.db.QueryRowContext(ctx, interfaceSelect+`
		WHERE i.id = ?`, id)

	iface, err := scanInterface(row)
	if errors.Is(err, sql.ErrNoRows) {
		return VirtualInterface{}, ErrInterfaceNotFound
	}
	if err != nil {
		return VirtualInterface{}, err
	}
	return iface, nil
}

// SetInterfaceAttachment attaches or detaches a virtual interface.
func (s *Store) SetInterfaceAttachment(ctx context.Context, id int64, vmID *string) (VirtualInterface, error) {
	if _, err := s.GetInterface(ctx, id); err != nil {
		return VirtualInterface{}, err
	}
	if vmID != nil && strings.TrimSpace(*vmID) == "" {
		vmID = nil
	}
	if vmID != nil {
		if _, err := s.GetVM(ctx, *vmID); err != nil {
			return VirtualInterface{}, err
		}
	}

	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		UPDATE virtual_interfaces SET attached_vm_id = ?, updated_at = ? WHERE id = ?`,
		vmID, now, id,
	)
	if err != nil {
		if isForeignKeyViolation(err) {
			return VirtualInterface{}, ErrNotFound
		}
		return VirtualInterface{}, fmt.Errorf("update interface attachment: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return VirtualInterface{}, fmt.Errorf("read updated rows: %w", err)
	}
	if affected == 0 {
		return VirtualInterface{}, ErrInterfaceNotFound
	}
	return s.GetInterface(ctx, id)
}

// SetInterfaceRuntime updates VPP runtime metadata for a virtual interface.
func (s *Store) SetInterfaceRuntime(ctx context.Context, id int64, swIfIndex uint32, socketPath string) (VirtualInterface, error) {
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		UPDATE virtual_interfaces SET sw_if_index = ?, socket_path = ?, updated_at = ? WHERE id = ?`,
		swIfIndex, socketPath, now, id,
	)
	if err != nil {
		return VirtualInterface{}, fmt.Errorf("update interface runtime: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return VirtualInterface{}, fmt.Errorf("read updated rows: %w", err)
	}
	if affected == 0 {
		return VirtualInterface{}, ErrInterfaceNotFound
	}
	return s.GetInterface(ctx, id)
}

// SetVMInterfaces replaces the network interfaces attached to a VM.
func (s *Store) SetVMInterfaces(ctx context.Context, vmID string, interfaceIDs []int64) (VM, error) {
	if _, err := s.GetVM(ctx, vmID); err != nil {
		return VM{}, err
	}

	seen := make(map[int64]struct{}, len(interfaceIDs))
	unique := make([]int64, 0, len(interfaceIDs))
	for _, id := range interfaceIDs {
		if id < 1 {
			return VM{}, ErrInterfaceNotFound
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)

		iface, err := s.GetInterface(ctx, id)
		if err != nil {
			return VM{}, err
		}
		if iface.AttachedVMID != nil && *iface.AttachedVMID != vmID {
			return VM{}, ErrInterfaceInUse
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return VM{}, fmt.Errorf("begin set vm interfaces: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().UnixMilli()
	if len(unique) == 0 {
		if _, err := tx.ExecContext(ctx,
			`UPDATE virtual_interfaces SET attached_vm_id = NULL, updated_at = ? WHERE attached_vm_id = ?`,
			now, vmID,
		); err != nil {
			return VM{}, fmt.Errorf("detach vm interfaces: %w", err)
		}
	} else {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(unique)), ",")
		args := make([]any, 0, len(unique)+2)
		args = append(args, now, vmID)
		for _, id := range unique {
			args = append(args, id)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE virtual_interfaces SET attached_vm_id = NULL, updated_at = ? `+
				`WHERE attached_vm_id = ? AND id NOT IN (`+placeholders+`)`,
			args...,
		); err != nil {
			return VM{}, fmt.Errorf("detach removed vm interfaces: %w", err)
		}

		args = make([]any, 0, len(unique)+2)
		args = append(args, vmID, now)
		for _, id := range unique {
			args = append(args, id)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE virtual_interfaces SET attached_vm_id = ?, updated_at = ? `+
				`WHERE id IN (`+placeholders+`)`,
			args...,
		); err != nil {
			return VM{}, fmt.Errorf("attach vm interfaces: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return VM{}, fmt.Errorf("commit vm interfaces: %w", err)
	}
	return s.GetVM(ctx, vmID)
}

// DeleteInterface removes a virtual interface that is not attached to a VM.
func (s *Store) DeleteInterface(ctx context.Context, id int64) error {
	iface, err := s.GetInterface(ctx, id)
	if err != nil {
		return err
	}
	if iface.AttachedVMID != nil {
		return ErrInterfaceInUse
	}

	res, err := s.db.ExecContext(ctx, `DELETE FROM virtual_interfaces WHERE id = ?`, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return ErrInterfaceInUse
		}
		return fmt.Errorf("delete interface: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted rows: %w", err)
	}
	if affected == 0 {
		return ErrInterfaceNotFound
	}
	return nil
}

// loadVMDisks fills VM.Disks for the supplied VM records.
func (s *Store) loadVMDisks(ctx context.Context, vms []VM) error {
	if len(vms) == 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT vd.vm_id, d.id, d.name, d.size_gb, d.vg, d.thin_pool,
		       d.storage_backend_id, sb.name
		FROM vm_disks AS vd
		JOIN disks AS d ON d.id = vd.disk_id
		LEFT JOIN storage_backends AS sb ON sb.id = d.storage_backend_id
		ORDER BY vd.vm_id, vd.position, d.name COLLATE NOCASE`)
	if err != nil {
		return fmt.Errorf("query vm disks: %w", err)
	}
	defer rows.Close()
	byVM := make(map[string][]VMDisk)
	for rows.Next() {
		var (
			vmID        string
			disk        VMDisk
			backend     sql.NullInt64
			backendName sql.NullString
			vg          string
			pool        string
		)
		if err := rows.Scan(&vmID, &disk.ID, &disk.Name, &disk.SizeGB, &vg, &pool, &backend, &backendName); err != nil {
			return err
		}
		disk.VG = vg
		disk.ThinPool = pool
		disk.Device = devicePath(vg, disk.Name)
		if backend.Valid {
			id := backend.Int64
			disk.StorageBackendID = &id
		}
		if backendName.Valid {
			disk.StorageBackendName = backendName.String
		}
		byVM[vmID] = append(byVM[vmID], disk)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate vm disks: %w", err)
	}
	for i := range vms {
		disks := byVM[vms[i].ID]
		if len(disks) == 0 {
			continue
		}
		vms[i].Disks = disks
		vms[i].Disk = &vms[i].Disks[0]
		vms[i].DiskID = &vms[i].Disks[0].ID
		vms[i].DiskIDs = make([]int64, 0, len(disks))
		for _, disk := range disks {
			vms[i].DiskIDs = append(vms[i].DiskIDs, disk.ID)
		}
	}
	return nil
}

// loadVMInterfaces fills VM.Interfaces for the supplied VM records.
func (s *Store) loadVMInterfaces(ctx context.Context, vms []VM) error {
	if len(vms) == 0 {
		return nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT i.id, i.name, i.network_id, n.name, i.socket_path, i.mac,
		       i.sw_if_index, i.attached_vm_id, ai.name, ae.name
		FROM virtual_interfaces AS i
		JOIN networks AS n ON n.id = i.network_id
		LEFT JOIN acls AS ai ON ai.id = i.ingress_acl_id
		LEFT JOIN acls AS ae ON ae.id = i.egress_acl_id
		WHERE i.attached_vm_id IS NOT NULL
		ORDER BY i.name COLLATE NOCASE, i.id`)
	if err != nil {
		return fmt.Errorf("query vm interfaces: %w", err)
	}
	defer rows.Close()

	byVM := make(map[string][]VMInterface)
	for rows.Next() {
		var (
			iface    VMInterface
			swIf     int64
			attached string
			ingress  sql.NullString
			egress   sql.NullString
		)
		if err := rows.Scan(
			&iface.ID, &iface.Name, &iface.NetworkID, &iface.NetworkName,
			&iface.SocketPath, &iface.MAC, &swIf, &attached,
			&ingress, &egress,
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
		byVM[attached] = append(byVM[attached], iface)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate vm interfaces: %w", err)
	}

	for i := range vms {
		vms[i].Interfaces = byVM[vms[i].ID]
	}
	return nil
}

const networkSelect = `
	SELECT n.id, n.name, n.bridge_domain_id,
	       (SELECT COUNT(*) FROM virtual_interfaces AS i WHERE i.network_id = n.id),
	       n.dhcp_enabled, n.dhcp_gateway, n.dhcp_range_start, n.dhcp_range_end,
	       n.dhcp_dns, n.dhcp_tap_sw_if_index, n.dhcp_tap_host_name,
	       n.bvi_host_name, n.bvi_sw_if_index, n.ipv4_gateway,
	       n.created_at, n.updated_at
	FROM networks AS n`

const vnfSelect = `
	SELECT f.id, f.name, f.network_id, n.name, f.type, f.enabled, f.config,
	       f.tap_sw_if_index, f.tap_host_name, f.memif_sw_if_index, f.memif_socket,
	       f.memif_host_name, f.created_at, f.updated_at
	FROM vnfs AS f
	JOIN networks AS n ON n.id = f.network_id`

const interfaceSelect = `
	SELECT i.id, i.name, i.network_id, n.name, i.socket_path, i.mac, i.sw_if_index,
	       i.attached_vm_id, v.name,
	       i.ingress_acl_id, ai.name, i.egress_acl_id, ae.name,
	       i.created_at, i.updated_at
	FROM virtual_interfaces AS i
	JOIN networks AS n ON n.id = i.network_id
	LEFT JOIN vms AS v ON v.id = i.attached_vm_id
	LEFT JOIN acls AS ai ON ai.id = i.ingress_acl_id
	LEFT JOIN acls AS ae ON ae.id = i.egress_acl_id`

func scanNetwork(s scanner) (Network, error) {
	var (
		network   Network
		bdID      int64
		dhcp      int64
		tapSwIf   int64
		bviSwIf   int64
		createdAt int64
		updatedAt int64
	)
	if err := s.Scan(
		&network.ID, &network.Name, &bdID, &network.InterfaceCount,
		&dhcp, &network.DHCPGateway, &network.DHCPRangeStart, &network.DHCPRangeEnd,
		&network.DHCPDNS, &tapSwIf, &network.DHCPTapHostName,
		&network.BVIHostName, &bviSwIf, &network.IPv4Gateway,
		&createdAt, &updatedAt,
	); err != nil {
		return Network{}, err
	}
	network.BridgeDomainID = uint32(bdID)
	network.DHCPEnabled = dhcp != 0
	network.DHCPTapSwIfIndex = uint32(tapSwIf)
	network.BVISwIfIndex = uint32(bviSwIf)
	network.CreatedAt = time.UnixMilli(createdAt).UTC()
	network.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return network, nil
}

func scanVNF(s scanner) (VNF, error) {
	var (
		vnf       VNF
		enabled   int64
		config    string
		tapSwIf   int64
		memifSwIf int64
		createdAt int64
		updatedAt int64
	)
	if err := s.Scan(
		&vnf.ID, &vnf.Name, &vnf.NetworkID, &vnf.NetworkName, &vnf.Type,
		&enabled, &config, &tapSwIf, &vnf.TapHostName, &memifSwIf, &vnf.MemifSocket,
		&vnf.MemifHostName, &createdAt, &updatedAt,
	); err != nil {
		return VNF{}, err
	}
	vnf.Enabled = enabled != 0
	vnf.TapSwIfIndex = uint32(tapSwIf)
	vnf.MemifSwIfIndex = uint32(memifSwIf)
	if strings.TrimSpace(config) != "" {
		if err := json.Unmarshal([]byte(config), &vnf.Config); err != nil {
			return VNF{}, fmt.Errorf("decode vnf %d config: %w", vnf.ID, err)
		}
	}
	vnf.CreatedAt = time.UnixMilli(createdAt).UTC()
	vnf.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return vnf, nil
}

func scanInterface(s scanner) (VirtualInterface, error) {
	var (
		iface     VirtualInterface
		swIf      int64
		vmID      sql.NullString
		vmName    sql.NullString
		ingressID sql.NullInt64
		ingress   sql.NullString
		egressID  sql.NullInt64
		egress    sql.NullString
		createdAt int64
		updatedAt int64
	)
	if err := s.Scan(
		&iface.ID, &iface.Name, &iface.NetworkID, &iface.NetworkName,
		&iface.SocketPath, &iface.MAC, &swIf, &vmID, &vmName,
		&ingressID, &ingress, &egressID, &egress,
		&createdAt, &updatedAt,
	); err != nil {
		return VirtualInterface{}, err
	}
	iface.SwIfIndex = uint32(swIf)
	if vmID.Valid {
		id := vmID.String
		iface.AttachedVMID = &id
	}
	if vmName.Valid {
		iface.AttachedVMName = vmName.String
	}
	if ingressID.Valid {
		id := ingressID.Int64
		iface.IngressACLID = &id
	}
	if ingress.Valid {
		iface.IngressACLName = ingress.String
	}
	if egressID.Valid {
		id := egressID.Int64
		iface.EgressACLID = &id
	}
	if egress.Valid {
		iface.EgressACLName = egress.String
	}
	iface.CreatedAt = time.UnixMilli(createdAt).UTC()
	iface.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return iface, nil
}

type scanner interface {
	Scan(dest ...any) error
}

const vmSelect = `
	SELECT v.id, v.name, v.description, v.status, v.vcpus, v.memory_mb,
	       v.disk_id, d.name, d.size_gb, d.vg, d.thin_pool,
	       v.boot_media_id, m.name, m.path, m.size_bytes,
	       v.console_enabled, v.created_at, v.updated_at
	FROM vms AS v
	LEFT JOIN disks AS d ON d.id = v.disk_id
	LEFT JOIN media AS m ON m.id = v.boot_media_id`

const diskSelect = `
	SELECT d.id, d.name, d.size_gb, d.vg, d.thin_pool,
	       d.storage_backend_id, sb.name, d.is_template, d.template_disk_id,
	       d.prepared_by_script, td.name, v.id, v.name, d.created_at, d.updated_at
	FROM disks AS d
	LEFT JOIN storage_backends AS sb ON sb.id = d.storage_backend_id
	LEFT JOIN disks AS td ON td.id = d.template_disk_id
	LEFT JOIN vm_disks AS vd ON vd.disk_id = d.id
	LEFT JOIN vms AS v ON v.id = vd.vm_id`

func scanVM(s scanner) (VM, error) {
	var (
		vm          VM
		createdAt   int64
		updatedAt   int64
		diskID      sql.NullInt64
		diskName    sql.NullString
		diskSize    sql.NullInt64
		diskVG      sql.NullString
		diskPool    sql.NullString
		bootMediaID sql.NullInt64
		mediaName   sql.NullString
		mediaPath   sql.NullString
		mediaSize   sql.NullInt64
		console     int64
	)
	if err := s.Scan(
		&vm.ID,
		&vm.Name,
		&vm.Description,
		&vm.Status,
		&vm.VCPUs,
		&vm.MemoryMB,
		&diskID,
		&diskName,
		&diskSize,
		&diskVG,
		&diskPool,
		&bootMediaID,
		&mediaName,
		&mediaPath,
		&mediaSize,
		&console,
		&createdAt,
		&updatedAt,
	); err != nil {
		return VM{}, err
	}

	vm.ConsoleEnabled = console != 0

	if diskID.Valid {
		id := diskID.Int64
		vm.DiskID = &id
	}
	if diskID.Valid && diskName.Valid && diskVG.Valid && diskPool.Valid {
		vm.Disk = &VMDisk{
			ID:       diskID.Int64,
			Name:     diskName.String,
			SizeGB:   int(diskSize.Int64),
			VG:       diskVG.String,
			ThinPool: diskPool.String,
			Device:   devicePath(diskVG.String, diskName.String),
		}
	}
	if bootMediaID.Valid {
		id := bootMediaID.Int64
		vm.BootMediaID = &id
	}
	if bootMediaID.Valid && mediaName.Valid && mediaPath.Valid {
		vm.BootMedia = &VMMedia{
			ID:        bootMediaID.Int64,
			Name:      mediaName.String,
			Path:      mediaPath.String,
			SizeBytes: mediaSize.Int64,
		}
	}

	vm.CreatedAt = time.UnixMilli(createdAt).UTC()
	vm.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return vm, nil
}

func scanDisk(s scanner) (Disk, error) {
	var (
		disk             Disk
		backendID        sql.NullInt64
		backend          sql.NullString
		isTemplate       int64
		templateID       sql.NullInt64
		preparedByScript int64
		templateName     sql.NullString
		vmID             sql.NullString
		vmName           sql.NullString
		createdAt        int64
		updatedAt        int64
	)
	if err := s.Scan(
		&disk.ID,
		&disk.Name,
		&disk.SizeGB,
		&disk.VG,
		&disk.ThinPool,
		&backendID,
		&backend,
		&isTemplate,
		&templateID,
		&preparedByScript,
		&templateName,
		&vmID,
		&vmName,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Disk{}, err
	}

	disk.Device = devicePath(disk.VG, disk.Name)
	disk.IsTemplate = isTemplate != 0
	disk.PreparedByScript = preparedByScript != 0
	if templateID.Valid {
		id := templateID.Int64
		disk.TemplateDiskID = &id
	}
	if templateName.Valid {
		disk.TemplateDiskName = templateName.String
	}
	if backendID.Valid {
		id := backendID.Int64
		disk.StorageBackendID = &id
	}
	if backend.Valid {
		disk.StorageBackendName = backend.String
	}
	if vmID.Valid {
		id := vmID.String
		disk.AttachedVMID = &id
	}
	if vmName.Valid {
		disk.AttachedVMName = vmName.String
	}
	disk.CreatedAt = time.UnixMilli(createdAt).UTC()
	disk.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return disk, nil
}

func scanMedia(s scanner) (Media, error) {
	var (
		media     Media
		createdAt int64
		updatedAt int64
	)
	if err := s.Scan(
		&media.ID,
		&media.Name,
		&media.Path,
		&media.SizeBytes,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Media{}, err
	}
	media.CreatedAt = time.UnixMilli(createdAt).UTC()
	media.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return media, nil
}

func devicePath(vg, name string) string {
	return "/dev/" + vg + "/" + name
}

func classifyVMCreateError(err error, name string) error {
	if isForeignKeyViolation(err) {
		if strings.Contains(strings.ToLower(err.Error()), "boot_media_id") {
			return ErrMediaNotFound
		}
		return ErrDiskNotFound
	}
	if !isUniqueViolation(err) {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "vms.disk_id") {
		return fmt.Errorf("%w", ErrDiskInUse)
	}
	return fmt.Errorf("%w: %q", ErrNameTaken, name)
}

func migrate(ctx context.Context, db *sql.DB) error {
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var current int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		version, err := migrationVersion(entry.Name())
		if err != nil {
			return err
		}
		if version <= current {
			continue
		}

		body, err := migrations.ReadFile(path.Join("migrations", entry.Name()))
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", entry.Name(), err)
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", entry.Name(), err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", entry.Name(), err)
		}
		current = version
	}

	return nil
}

func migrationVersion(name string) (int, error) {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok {
		return 0, fmt.Errorf("migration %q must be named <version>_<name>.sql", name)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil || version < 1 {
		return 0, fmt.Errorf("migration %q has an invalid version", name)
	}
	return version, nil
}

// SQLite extended result codes for constraint violations.
const (
	sqliteConstraintPrimaryKey = 1555
	sqliteConstraintUnique     = 2067
	sqliteConstraintForeignKey = 787
)

func isUniqueViolation(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	switch sqliteErr.Code() {
	case sqliteConstraintPrimaryKey, sqliteConstraintUnique:
		return true
	default:
		return false
	}
}

func isForeignKeyViolation(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	return sqliteErr.Code() == sqliteConstraintForeignKey
}
