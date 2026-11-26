// Package store persists VM and disk records in SQLite.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"modernc.org/sqlite"

	"github.com/voidstarit/rookery/internal/blockdev"
	"github.com/voidstarit/rookery/internal/diskthrottle"
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
	// ErrDiskDeviceTaken is returned when a host block device is already
	// registered as a passthrough disk.
	ErrDiskDeviceTaken = errors.New("host block device is already registered")
	// ErrDiskInUse is returned when a disk is attached to a workload.
	ErrDiskInUse = errors.New("disk is attached to a workload")
	// ErrDiskIsTemplate is returned when a template disk is attached to a workload.
	ErrDiskIsTemplate = errors.New("template disks cannot be attached to a workload")
	// ErrDiskReadOnlyTemplate is returned when a template is marked read-only.
	ErrDiskReadOnlyTemplate = errors.New("template disks cannot be read-only")
	// ErrDiskDeleting is returned when a disk is provisioning or being deleted.
	ErrDiskDeleting = errors.New("disk is provisioning or being deleted")
	// ErrDiskDeletionNotQueued is returned when a forced delete is requested for
	// a disk that has no pending or failed asynchronous deletion.
	ErrDiskDeletionNotQueued = errors.New("disk deletion is not queued")
	// ErrInvalidDiskIDs is returned when disk IDs are malformed.
	ErrInvalidDiskIDs = errors.New("invalid disk ids")
	// ErrInvalidDiskSerials is returned when a per-disk serial is malformed.
	ErrInvalidDiskSerials = errors.New("invalid disk serials")
	// ErrLightVMRootRequired is returned when a LightVM has neither a container
	// image nor an attached disk to boot from.
	ErrLightVMRootRequired = errors.New("lightvm container image or disk is required")
	// ErrLightVMDeviceLimit is returned when a LightVM exceeds the combined
	// disk, interface, and virtiofs-share device limit.
	ErrLightVMDeviceLimit = errors.New("lightvm device limit exceeded")
	// ErrInvalidVirtioFSShare is returned for malformed host shares.
	ErrInvalidVirtioFSShare = errors.New("invalid virtiofs share")
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
	// ErrNetworkHostTapNotFound is returned when a host tap does not exist.
	ErrNetworkHostTapNotFound = errors.New("network host tap not found")
	// ErrNetworkHostTapNameTaken is returned when a host interface name is already in use.
	ErrNetworkHostTapNameTaken = errors.New("network host tap name already exists")
	// ErrACLNotFound is returned when an ACL does not exist.
	ErrACLNotFound = errors.New("acl not found")
	// ErrACLNameTaken is returned when an ACL name is already in use.
	ErrACLNameTaken = errors.New("acl name already exists")
	// ErrACLInUse is returned when an ACL is assigned to an interface.
	ErrACLInUse = errors.New("acl is assigned to an interface")
)

// MaxWorkloadDisks bounds a single workload's disk attachment list.
const MaxWorkloadDisks = 32

// MaxLightVMDevices bounds the combined disks and interfaces of a LightVM.
const MaxLightVMDevices = 24

// DiskInUseError names the workload that owns a disk.
type DiskInUseError struct {
	OwnerKind       string
	OwnerID         string
	OwnerName       string
	AttachmentCount int
}

// Error renders the owning workload details, or the bare ErrDiskInUse text when the receiver is nil
// or carries no owner information; multiple attachments are reported as a count.
func (e *DiskInUseError) Error() string {
	if e == nil {
		return ErrDiskInUse.Error()
	}
	if e.AttachmentCount > 1 {
		return fmt.Sprintf("disk is attached to %d workloads", e.AttachmentCount)
	}
	switch {
	case e.OwnerName != "" && e.OwnerKind != "":
		return fmt.Sprintf("disk is attached to %s %q", e.OwnerKind, e.OwnerName)
	case e.OwnerID != "" && e.OwnerKind != "":
		return fmt.Sprintf("disk is attached to %s %s", e.OwnerKind, e.OwnerID)
	case e.OwnerKind != "":
		return fmt.Sprintf("disk is attached to a %s", e.OwnerKind)
	default:
		return ErrDiskInUse.Error()
	}
}

// Unwrap exposes ErrDiskInUse so errors.Is reports any DiskInUseError as a disk-in-use failure.
func (e *DiskInUseError) Unwrap() error { return ErrDiskInUse }

const (
	StatusStopped  = "stopped"
	StatusStarting = "starting"
	StatusRunning  = "running"
	StatusStopping = "stopping"
	StatusPaused   = "paused"
	StatusError    = "error"
	// StatusTerminated marks an ephemeral LightVM whose guest exited and whose
	// runtime resources were reaped automatically.
	StatusTerminated = "terminated"
)

var validStatuses = map[string]struct{}{
	StatusStopped:    {},
	StatusStarting:   {},
	StatusRunning:    {},
	StatusStopping:   {},
	StatusPaused:     {},
	StatusError:      {},
	StatusTerminated: {},
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
		StatusTerminated,
	}
}

const (
	// DiskSourceLVMThin is a disk backed by a Rookery-managed thin-LVM volume.
	DiskSourceLVMThin = "lvm-thin"
	// DiskSourceHostDevice passes an existing host block device (a disk,
	// partition, or logical volume) through to one VM.
	DiskSourceHostDevice = "host-device"
	// DiskSourceKouja is a disk provisioned by a kouja storage backend.
	DiskSourceKouja = "kouja"
	// DiskSourceCeph is reserved for the future Ceph RBD driver.
	DiskSourceCeph = "ceph"
)

// Disk deletion lifecycle states. An empty state is a ready disk.
const (
	DiskDeletionProvisioning = "provisioning"
	DiskDeletionDeleting     = "deleting"
	DiskDeletionFailed       = "delete_failed"
)

const (
	// DesiredRunning means the VM should be running, including after an API or
	// host restart.
	DesiredRunning = "running"
	// DesiredStopped means the VM was stopped explicitly and stays down.
	DesiredStopped = "stopped"
)

// ValidDesiredState reports whether s is a desired power state.
func ValidDesiredState(s string) bool {
	return s == DesiredRunning || s == DesiredStopped
}

// VM is a virtual machine record.
type VM struct {
	ID             string           `json:"id"`
	Name           string           `json:"name"`
	Description    string           `json:"description"`
	Status         string           `json:"status"`
	DesiredState   string           `json:"desired_state"`
	VCPUs          int              `json:"vcpus"`
	MemoryMB       int              `json:"memory_mb"`
	DiskID         *int64           `json:"disk_id,omitempty"`
	Disk           *VMDisk          `json:"disk,omitempty"`
	DiskIDs        []int64          `json:"disk_ids,omitempty"`
	DiskSerials    map[int64]string `json:"-"`
	Disks          []VMDisk         `json:"disks,omitempty"`
	BootMediaID    *int64           `json:"boot_media_id,omitempty"`
	BootMedia      *VMMedia         `json:"boot_media,omitempty"`
	Interfaces     []VMInterface    `json:"interfaces,omitempty"`
	ConsoleEnabled bool             `json:"console_enabled"`
	CgroupClass    string           `json:"cgroup_class,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
}

// VMDisk is the disk summary returned with a VM.
type VMDisk struct {
	ID                 int64                `json:"id"`
	Name               string               `json:"name"`
	SizeGB             int                  `json:"size_gb"`
	SizeBytes          uint64               `json:"size_bytes,omitempty"`
	StorageBackendID   *int64               `json:"storage_backend_id,omitempty"`
	StorageBackendName string               `json:"storage_backend_name,omitempty"`
	VG                 string               `json:"vg"`
	ThinPool           string               `json:"thin_pool"`
	Device             string               `json:"device"`
	Source             string               `json:"source"`
	Protocol           string               `json:"protocol"`
	Serial             string               `json:"serial,omitempty"`
	ReadOnly           bool                 `json:"read_only,omitempty"`
	ThrottleClassID    *int64               `json:"throttle_class_id,omitempty"`
	ThrottleClassName  string               `json:"throttle_class_name,omitempty"`
	Throttle           *diskthrottle.Limits `json:"-"`
	ThrottleError      string               `json:"-"`
}

// VMMedia is the boot media summary returned with a VM.
type VMMedia struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
}

// Network is a VPP bridge domain presented as a Rookery Network.
type Network struct {
	ID                int64     `json:"id"`
	Name              string    `json:"name"`
	BridgeDomainID    uint32    `json:"bridge_domain_id"`
	InterfaceCount    int       `json:"interface_count"`
	DHCPEnabled       bool      `json:"-"`
	DHCPGateway       string    `json:"-"`
	DHCPRangeStart    string    `json:"-"`
	DHCPRangeEnd      string    `json:"-"`
	DHCPDNS           string    `json:"-"`
	DHCPTapSwIfIndex  uint32    `json:"-"`
	DHCPTapHostName   string    `json:"-"`
	BVIHostName       string    `json:"bvi_host_name,omitempty"`
	BVISwIfIndex      uint32    `json:"bvi_sw_if_index,omitempty"`
	IPv4Gateway       string    `json:"ipv4_gateway,omitempty"`
	VNFTransport      string    `json:"vnf_transport,omitempty"`
	VNFMemifSocket    string    `json:"vnf_memif_socket,omitempty"`
	VNFMemifSwIfIndex uint32    `json:"vnf_memif_sw_if_index,omitempty"`
	VNFMemifHostName  string    `json:"vnf_memif_host_name,omitempty"`
	IPv4CIDR          string    `json:"ipv4_cidr"`
	IPv6CIDR          string    `json:"ipv6_cidr"`
	RouteTableID      uint32    `json:"route_table_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// StartupScript is the singleton server-level script run after API startup.
type StartupScript struct {
	Enabled   bool       `json:"enabled"`
	Script    string     `json:"script,omitempty"`
	Status    string     `json:"status"`
	ExitCode  int        `json:"exit_code"`
	Output    string     `json:"output,omitempty"`
	RanAt     *time.Time `json:"ran_at,omitempty"`
	UpdatedAt time.Time  `json:"updated_at"`
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

type InterfaceACL struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// VirtualInterface is a VPP vhost-user interface belonging to a network.
type VirtualInterface struct {
	ID                  int64          `json:"id"`
	Name                string         `json:"name"`
	NetworkID           int64          `json:"network_id"`
	NetworkName         string         `json:"network_name"`
	SocketPath          string         `json:"socket_path"`
	MAC                 string         `json:"mac"`
	SwIfIndex           uint32         `json:"sw_if_index"`
	AttachedVMID        *string        `json:"attached_vm_id,omitempty"`
	AttachedVMName      string         `json:"attached_vm_name,omitempty"`
	AttachedLightVMID   *string        `json:"attached_lightvm_id,omitempty"`
	AttachedLightVMName string         `json:"attached_lightvm_name,omitempty"`
	IngressACLID        *int64         `json:"ingress_acl_id,omitempty"`
	IngressACLName      string         `json:"ingress_acl_name,omitempty"`
	EgressACLID         *int64         `json:"egress_acl_id,omitempty"`
	EgressACLName       string         `json:"egress_acl_name,omitempty"`
	IngressACLs         []InterfaceACL `json:"-"`
	EgressACLs          []InterfaceACL `json:"-"`
	IPv4TableID         uint32         `json:"ipv4_table_id"`
	IPv6TableID         uint32         `json:"ipv6_table_id"`
	IPv4TableName       string         `json:"ipv4_table_name,omitempty"`
	IPv6TableName       string         `json:"ipv6_table_name,omitempty"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
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

// DiskAttachment names one workload that has a disk attached.
type DiskAttachment struct {
	WorkloadKind string `json:"workload_kind"`
	WorkloadID   string `json:"workload_id"`
	WorkloadName string `json:"workload_name"`
	Position     int    `json:"position"`
	ReadOnly     bool   `json:"read_only"`
}

// Disk is a provisioned or passthrough disk that can be attached to a VM.
type Disk struct {
	ID                    int64               `json:"id"`
	Name                  string              `json:"name"`
	SizeGB                int                 `json:"size_gb"`
	SizeBytes             uint64              `json:"size_bytes,omitempty"`
	StorageBackendID      *int64              `json:"storage_backend_id,omitempty"`
	StorageBackendName    string              `json:"storage_backend_name,omitempty"`
	VG                    string              `json:"vg"`
	ThinPool              string              `json:"thin_pool"`
	Device                string              `json:"device"`
	Source                string              `json:"source"`
	Protocol              string              `json:"protocol"`
	ReadOnly              bool                `json:"read_only,omitempty"`
	Attachments           []DiskAttachment    `json:"attachments,omitempty"`
	ThrottleClassID       *int64              `json:"throttle_class_id,omitempty"`
	ThrottleClassName     string              `json:"throttle_class_name,omitempty"`
	BackendMetadata       DiskBackendMetadata `json:"backend_metadata,omitempty"`
	IsTemplate            bool                `json:"is_template"`
	TemplateDiskID        *int64              `json:"template_disk_id,omitempty"`
	TemplateDiskName      string              `json:"template_disk_name,omitempty"`
	ProvisioningScript    string              `json:"provisioning_script,omitempty"`
	ProvisioningTimeout   string              `json:"provisioning_timeout,omitempty"`
	PreparedByScript      bool                `json:"-"`
	AttachedVMID          *string             `json:"attached_vm_id,omitempty"`
	AttachedVMName        string              `json:"attached_vm_name,omitempty"`
	AttachedLightVMID     *string             `json:"attached_lightvm_id,omitempty"`
	AttachedLightVMName   string              `json:"attached_lightvm_name,omitempty"`
	DeletionState         string              `json:"deletion_state,omitempty"`
	DeletionError         string              `json:"deletion_error,omitempty"`
	DeletionAttempts      int                 `json:"deletion_attempts,omitempty"`
	DeletionNextAttemptAt time.Time           `json:"deletion_next_attempt_at,omitempty"`
	DeletionClaimedAt     time.Time           `json:"deletion_claimed_at,omitempty"`
	CreatedAt             time.Time           `json:"created_at"`
	UpdatedAt             time.Time           `json:"updated_at"`
}

// ImportDisk describes an existing volume that should be added to inventory.
type ImportDisk struct {
	Name   string
	SizeGB int
	// SizeBytes is the exact volume size; it is zero when unknown.
	SizeBytes uint64
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

	if err := reportLegacyDiskConflicts(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	if err := preflightVNFMigration(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	if err := ensureStartupScriptSchema(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	if err := ensureVPPInterfaceAddressesSchema(ctx, db); err != nil {
		db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

// ensureVPPInterfaceAddressesSchema repairs development databases that recorded
// a higher schema version before the address table was added.
func ensureVPPInterfaceAddressesSchema(ctx context.Context, db *sql.DB) error {
	var exists int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name = 'vpp_interface_addresses'`).Scan(&exists); err != nil {
		return fmt.Errorf("inspect vpp_interface_addresses schema: %w", err)
	}
	if exists != 0 {
		_, err := db.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS vpp_interface_dhcp_clients (
			  interface_name TEXT PRIMARY KEY,
			  enabled INTEGER NOT NULL DEFAULT 0,
			  updated_at INTEGER NOT NULL
			)`)
		if err != nil {
			return fmt.Errorf("create vpp_interface_dhcp_clients table: %w", err)
		}
		return nil
	}
	body, err := migrations.ReadFile("migrations/0028_vpp_interface_addresses.sql")
	if err != nil {
		return fmt.Errorf("read vpp_interface_addresses migration: %w", err)
	}
	if _, err := db.ExecContext(ctx, string(body)); err != nil {
		return fmt.Errorf("create vpp_interface_addresses table: %w", err)
	}
	return nil
}

// ensureStartupScriptSchema recreates the startup-script singleton if a
// development database is missing the table or has the pre-split schema. This
// is intentionally limited to this server setting; normal schema changes stay
// in the numbered migrations.
func ensureStartupScriptSchema(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info('startup_script')`)
	if err != nil {
		return fmt.Errorf("inspect startup_script schema: %w", err)
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return fmt.Errorf("inspect startup_script column: %w", err)
		}
		columns[name] = true
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close startup_script schema rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect startup_script schema: %w", err)
	}

	required := []string{
		"id", "enabled", "script", "status", "exit_code", "output", "ran_at", "updated_at",
		"vpp_enabled", "vpp_script", "vpp_status", "vpp_exit_code", "vpp_output", "vpp_ran_at", "vpp_updated_at",
	}
	for _, name := range required {
		if !columns[name] {
			body, err := migrations.ReadFile("migrations/0027_startup_script.sql")
			if err != nil {
				return fmt.Errorf("read startup_script migration: %w", err)
			}
			if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS startup_script`); err != nil {
				return fmt.Errorf("replace startup_script table: %w", err)
			}
			if _, err := db.ExecContext(ctx, string(body)); err != nil {
				return fmt.Errorf("create startup_script table: %w", err)
			}
			return nil
		}
	}
	return nil
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
	serials, err := normalizeDiskSerials(diskIDs, vm.DiskSerials)
	if err != nil {
		return VM{}, err
	}
	for _, diskID := range diskIDs {
		if err := s.checkDiskAvailableForAttach(ctx, diskID, "vm", vm.ID); err != nil {
			return VM{}, err
		}
	}
	if err := s.checkBootMedia(ctx, vm.BootMediaID); err != nil {
		return VM{}, err
	}
	vm.DiskIDs = diskIDs
	vm.DiskSerials = serials
	if vm.DesiredState == "" {
		vm.DesiredState = DesiredStopped
	}

	now := time.Now().UTC().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return VM{}, fmt.Errorf("begin create vm: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO vms (id, name, description, status, desired_state, vcpus, memory_mb, boot_media_id, console_enabled, cgroup_class, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		vm.ID, vm.Name, vm.Description, vm.Status, vm.DesiredState, vm.VCPUs, vm.MemoryMB, vm.BootMediaID, vm.ConsoleEnabled, vm.CgroupClass, now, now,
	); err != nil {
		if constraintErr := classifyVMCreateError(err, vm.Name); constraintErr != nil {
			return VM{}, constraintErr
		}
		return VM{}, fmt.Errorf("insert vm: %w", err)
	}
	for position, diskID := range diskIDs {
		if err := s.insertWorkloadDisk(ctx, tx, vm.ID, nil, diskID, position, serials[diskID]); err != nil {
			return VM{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return VM{}, fmt.Errorf("commit create vm: %w", err)
	}
	return s.GetVM(ctx, vm.ID)
}

// SetVMDisks replaces the disks attached to a stopped VM.
func (s *Store) SetVMDisks(ctx context.Context, vmID string, diskIDs []int64) (VM, error) {
	return s.SetVMDisksWithSerials(ctx, vmID, diskIDs, nil)
}

// SetVMDisksWithSerials replaces the disks attached to a stopped VM and sets
// the guest-visible serial for each attachment.
func (s *Store) SetVMDisksWithSerials(ctx context.Context, vmID string, diskIDs []int64, serials map[int64]string) (VM, error) {
	if _, err := s.GetVM(ctx, vmID); err != nil {
		return VM{}, err
	}
	diskIDs, err := normalizeDiskIDs(diskIDs)
	if err != nil {
		return VM{}, err
	}
	serials, err = normalizeDiskSerials(diskIDs, serials)
	if err != nil {
		return VM{}, err
	}
	for _, diskID := range diskIDs {
		if err := s.checkDiskAvailableForAttach(ctx, diskID, "vm", vmID); err != nil {
			return VM{}, err
		}
	}
	now := time.Now().UTC().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return VM{}, fmt.Errorf("begin update vm disks: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM workload_disks WHERE vm_id = ?`, vmID); err != nil {
		return VM{}, fmt.Errorf("clear vm disks: %w", err)
	}
	for position, diskID := range diskIDs {
		if err := s.insertWorkloadDisk(ctx, tx, vmID, nil, diskID, position, serials[diskID]); err != nil {
			return VM{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE vms SET updated_at = ? WHERE id = ?`,
		now, vmID,
	); err != nil {
		return VM{}, fmt.Errorf("update vm updated_at: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return VM{}, fmt.Errorf("commit update vm disks: %w", err)
	}
	return s.GetVM(ctx, vmID)
}

// MaxDiskSerialLength is the virtio-blk serial field size. QEMU truncates
// longer values, so reject them instead of silently changing the guest-visible
// serial.
const MaxDiskSerialLength = 20

var diskSerialPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]*$`)

// NormalizeDiskSerial trims and validates a guest-visible disk serial. An
// empty serial means the disk has no explicit serial.
func NormalizeDiskSerial(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > MaxDiskSerialLength {
		return "", fmt.Errorf("%w: serial %q is longer than %d bytes", ErrInvalidDiskSerials, value, MaxDiskSerialLength)
	}
	if !diskSerialPattern.MatchString(value) {
		return "", fmt.Errorf("%w: serial %q may contain only letters, digits, '.', '_', ':', and '-'", ErrInvalidDiskSerials, value)
	}
	return value, nil
}

// normalizeDiskSerials validates a disk ID to serial mapping and returns only
// the non-empty serials. Every key must reference a disk in diskIDs, and
// serials must be unique within the workload.
func normalizeDiskSerials(diskIDs []int64, serials map[int64]string) (map[int64]string, error) {
	if len(serials) == 0 {
		return nil, nil
	}
	allowed := make(map[int64]struct{}, len(diskIDs))
	for _, diskID := range diskIDs {
		allowed[diskID] = struct{}{}
	}
	normalized := make(map[int64]string, len(serials))
	owners := make(map[string]int64, len(serials))
	for diskID, raw := range serials {
		if _, ok := allowed[diskID]; !ok {
			return nil, fmt.Errorf("%w: disk %d is not attached to this workload", ErrInvalidDiskSerials, diskID)
		}
		serial, err := NormalizeDiskSerial(raw)
		if err != nil {
			return nil, err
		}
		if serial == "" {
			continue
		}
		if owner, ok := owners[serial]; ok {
			return nil, fmt.Errorf("%w: serial %q is used by disks %d and %d", ErrInvalidDiskSerials, serial, owner, diskID)
		}
		owners[serial] = diskID
		normalized[diskID] = serial
	}
	if len(normalized) == 0 {
		return nil, nil
	}
	return normalized, nil
}

// normalizeDiskIDs requires at least one positive disk ID, removes duplicates in first-seen order,
// and caps the list at MaxWorkloadDisks, wrapping ErrInvalidDiskIDs when the cap is exceeded.
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
	if len(out) > MaxWorkloadDisks {
		return nil, fmt.Errorf("%w: at most %d disks are allowed", ErrInvalidDiskIDs, MaxWorkloadDisks)
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
		SET name = ?, description = ?, status = ?, vcpus = ?, memory_mb = ?, boot_media_id = ?, console_enabled = ?, cgroup_class = ?, updated_at = ?
		WHERE id = ?`,
		vm.Name, vm.Description, vm.Status, vm.VCPUs, vm.MemoryMB, vm.BootMediaID, vm.ConsoleEnabled, vm.CgroupClass, now, vm.ID,
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

// SetVMDesiredState records the power state the VM should have after an API or
// host restart. Starting a VM sets DesiredRunning; stopping it sets
// DesiredStopped so reconciliation leaves it down.
func (s *Store) SetVMDesiredState(ctx context.Context, id, state string) (VM, error) {
	if !ValidDesiredState(state) {
		return VM{}, fmt.Errorf("invalid desired state %q", state)
	}
	now := time.Now().UTC().UnixMilli()

	res, err := s.db.ExecContext(ctx, `
		UPDATE vms SET desired_state = ?, updated_at = ? WHERE id = ?`,
		state, now, id,
	)
	if err != nil {
		return VM{}, fmt.Errorf("update vm desired state: %w", err)
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
	if disk.ThrottleClassID != nil {
		if *disk.ThrottleClassID < 0 {
			return Disk{}, ErrDiskThrottleClassInvalid
		}
		if *disk.ThrottleClassID == 0 {
			disk.ThrottleClassID = nil
		} else {
			if disk.IsTemplate {
				return Disk{}, ErrDiskThrottleClassOnTemplate
			}
			class, err := s.GetDiskThrottleClass(ctx, *disk.ThrottleClassID)
			if err != nil {
				if errors.Is(err, ErrDiskThrottleClassNotFound) {
					return Disk{}, ErrDiskThrottleClassNotFound
				}
				return Disk{}, err
			}
			if class.ValidationError != "" {
				return Disk{}, fmt.Errorf("%w: %s", ErrDiskThrottleClassInvalid, class.ValidationError)
			}
		}
	}
	if disk.Source == "" {
		disk.Source = DiskSourceLVMThin
	}
	if disk.Protocol == "" {
		switch disk.Source {
		case DiskSourceKouja:
			disk.Protocol = string(blockdev.ProtocolISCSI)
		case DiskSourceCeph:
			disk.Protocol = string(blockdev.ProtocolRBD)
		default:
			disk.Protocol = string(blockdev.ProtocolLocal)
		}
	}
	switch disk.Source {
	case DiskSourceLVMThin:
		disk.Device = ""
		disk.SizeBytes = 0
		disk.Protocol = string(blockdev.ProtocolLocal)
	case DiskSourceHostDevice:
		if strings.TrimSpace(disk.Device) == "" {
			return Disk{}, errors.New("host device path is required")
		}
		if disk.IsTemplate || disk.TemplateDiskID != nil || disk.PreparedByScript ||
			disk.ProvisioningScript != "" || disk.ProvisioningTimeout != "" {
			return Disk{}, errors.New("host devices cannot be templates or template clones")
		}
		disk.Protocol = string(blockdev.ProtocolLocal)
	case DiskSourceKouja, DiskSourceCeph:
		if disk.Device != "" || disk.VG != "" || disk.ThinPool != "" {
			return Disk{}, fmt.Errorf("%s disks cannot have a local device, VG, or thin pool", disk.Source)
		}
		if disk.IsTemplate || disk.TemplateDiskID != nil || disk.PreparedByScript ||
			disk.ProvisioningScript != "" || disk.ProvisioningTimeout != "" {
			return Disk{}, fmt.Errorf("%s disks cannot be templates or template clones", disk.Source)
		}
	default:
		return Disk{}, fmt.Errorf("unsupported disk source %q", disk.Source)
	}
	if disk.Source == DiskSourceKouja && blockdev.Protocol(disk.Protocol).Normalize() == blockdev.ProtocolLocal {
		return Disk{}, errors.New("kouja disks require a remote attachment protocol")
	}
	if disk.Source == DiskSourceCeph && blockdev.Protocol(disk.Protocol) != blockdev.ProtocolRBD {
		return Disk{}, errors.New("ceph disks require the rbd attachment protocol")
	}
	if !disk.IsTemplate && (disk.ProvisioningScript != "" || disk.ProvisioningTimeout != "") {
		return Disk{}, errors.New("provisioning settings require a template disk")
	}
	if disk.ReadOnly && disk.IsTemplate {
		return Disk{}, ErrDiskReadOnlyTemplate
	}
	backendMetadata, err := EncodeDiskBackendMetadata(disk.BackendMetadata)
	if err != nil {
		return Disk{}, err
	}
	if disk.DeletionState == "" {
		attachment := disk.BackendMetadata.Attachment
		if attachment.Protocol == "" {
			attachment.Protocol = blockdev.Protocol(disk.Protocol)
		}
		if attachment.Protocol.Normalize() != blockdev.ProtocolLocal {
			if err := attachment.ValidateComplete(); err != nil {
				return Disk{}, fmt.Errorf("create disk attachment: %w", err)
			}
		}
	}
	now := time.Now().UTC().UnixMilli()
	nextAttempt := int64(0)
	if !disk.DeletionNextAttemptAt.IsZero() {
		nextAttempt = disk.DeletionNextAttemptAt.UTC().UnixMilli()
	}
	claimedAt := int64(0)
	if !disk.DeletionClaimedAt.IsZero() {
		claimedAt = disk.DeletionClaimedAt.UTC().UnixMilli()
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO disks (name, size_gb, vg, thin_pool, storage_backend_id, source, device, size_bytes,
			attachment_protocol, backend_metadata, throttle_class_id,
			deletion_state, deletion_error, deletion_attempts, deletion_next_attempt_at, deletion_claimed_at,
			is_template, template_disk_id, provisioning_script, provisioning_timeout, prepared_by_script,
			read_only, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		disk.Name, disk.SizeGB, disk.VG, disk.ThinPool, disk.StorageBackendID, disk.Source,
		disk.Device, disk.SizeBytes, disk.Protocol, backendMetadata, disk.ThrottleClassID,
		disk.DeletionState, disk.DeletionError, disk.DeletionAttempts, nextAttempt, claimedAt,
		disk.IsTemplate, disk.TemplateDiskID,
		disk.ProvisioningScript, disk.ProvisioningTimeout, disk.PreparedByScript, disk.ReadOnly, now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			if _, nameErr := s.GetDiskByName(ctx, disk.Name); nameErr == nil {
				return Disk{}, fmt.Errorf("%w: %q", ErrDiskNameTaken, disk.Name)
			}
			return Disk{}, fmt.Errorf("%w: %q", ErrDiskDeviceTaken, disk.Device)
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
			INSERT INTO disks (name, size_gb, vg, thin_pool, storage_backend_id, size_bytes, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			item.Name, item.SizeGB, vg, thinPool, backendID, item.SizeBytes, now, now,
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
	ids := make([]int64, 0)
	for rows.Next() {
		disk, err := scanDisk(rows)
		if err != nil {
			return nil, err
		}
		disks = append(disks, disk)
		ids = append(ids, disk.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate disks: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close disks: %w", err)
	}

	attachments, err := s.loadDiskAttachments(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range disks {
		applyDiskAttachments(&disks[i], attachments[disks[i].ID])
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
	if err := s.attachDiskAttachments(ctx, &disk); err != nil {
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
	if err := s.attachDiskAttachments(ctx, &disk); err != nil {
		return Disk{}, err
	}
	return disk, nil
}

// loadDiskAttachments returns the attachments for a set of disks in one
// query, keyed by disk ID.
func (s *Store) loadDiskAttachments(ctx context.Context, diskIDs []int64) (map[int64][]DiskAttachment, error) {
	result := make(map[int64][]DiskAttachment, len(diskIDs))
	if len(diskIDs) == 0 {
		return result, nil
	}
	placeholders := make([]string, len(diskIDs))
	args := make([]any, len(diskIDs))
	for i, id := range diskIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT wd.disk_id, wd.vm_id, v.name, wd.lightvm_id, lv.name, wd.position, wd.read_only
		FROM workload_disks AS wd
		LEFT JOIN vms AS v ON v.id = wd.vm_id
		LEFT JOIN lightvms AS lv ON lv.id = wd.lightvm_id
		WHERE wd.disk_id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY wd.disk_id, wd.position, wd.vm_id, wd.lightvm_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("query disk attachments: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			diskID      int64
			vmID        sql.NullString
			vmName      sql.NullString
			lightVMID   sql.NullString
			lightVMName sql.NullString
			position    int
			readOnly    int64
		)
		if err := rows.Scan(&diskID, &vmID, &vmName, &lightVMID, &lightVMName, &position, &readOnly); err != nil {
			return nil, err
		}
		attachment := DiskAttachment{Position: position, ReadOnly: readOnly != 0}
		if vmID.Valid {
			attachment.WorkloadKind = "vm"
			attachment.WorkloadID = vmID.String
			attachment.WorkloadName = vmName.String
		} else if lightVMID.Valid {
			attachment.WorkloadKind = "lightvm"
			attachment.WorkloadID = lightVMID.String
			attachment.WorkloadName = lightVMName.String
		} else {
			continue
		}
		result[diskID] = append(result[diskID], attachment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate disk attachments: %w", err)
	}
	return result, nil
}

// attachDiskAttachments loads the workload attachments for disk and writes them onto it in place,
// leaving the attachment list empty when no workload references the disk.
func (s *Store) attachDiskAttachments(ctx context.Context, disk *Disk) error {
	attachments, err := s.loadDiskAttachments(ctx, []int64{disk.ID})
	if err != nil {
		return err
	}
	applyDiskAttachments(disk, attachments[disk.ID])
	return nil
}

// applyDiskAttachments stores the full attachment list and keeps the legacy
// single-owner fields populated only when exactly one workload owns the disk.
func applyDiskAttachments(disk *Disk, attachments []DiskAttachment) {
	disk.Attachments = attachments
	disk.AttachedVMID = nil
	disk.AttachedVMName = ""
	disk.AttachedLightVMID = nil
	disk.AttachedLightVMName = ""
	if len(attachments) != 1 {
		return
	}
	attachment := attachments[0]
	id := attachment.WorkloadID
	switch attachment.WorkloadKind {
	case "vm":
		disk.AttachedVMID = &id
		disk.AttachedVMName = attachment.WorkloadName
	case "lightvm":
		disk.AttachedLightVMID = &id
		disk.AttachedLightVMName = attachment.WorkloadName
	}
}

// SetDiskTemplate marks or unmarks an unattached disk as a template.
func (s *Store) SetDiskTemplate(ctx context.Context, id int64, isTemplate bool) (Disk, error) {
	disk, err := s.GetDisk(ctx, id)
	if err != nil {
		return Disk{}, err
	}
	if disk.Source != DiskSourceLVMThin {
		return Disk{}, errors.New("only LVM thin disks can be templates")
	}
	if isTemplate && disk.ReadOnly {
		return Disk{}, ErrDiskReadOnlyTemplate
	}
	if err := s.checkDiskOwnership(ctx, disk, "", ""); err != nil {
		return Disk{}, err
	}
	if isTemplate {
		if err := checkDiskNotDeleting(disk); err != nil {
			return Disk{}, err
		}
	}
	now := time.Now().UTC().UnixMilli()
	if isTemplate {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE disks
			SET is_template = 1,
			    throttle_class_id = NULL,
			    updated_at = ?
			WHERE id = ?`, now, id); err != nil {
			return Disk{}, fmt.Errorf("update disk template flag: %w", err)
		}
	} else {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE disks
			SET is_template = 0,
			    provisioning_script = '',
			    provisioning_timeout = '',
			    updated_at = ?
			WHERE id = ?`, now, id); err != nil {
			return Disk{}, fmt.Errorf("update disk template flag: %w", err)
		}
	}
	return s.GetDisk(ctx, id)
}

// SetDiskReadOnly changes the disk's read-only flag. The flag may only change
// while the disk is detached, so a running read-write QEMU cannot become a
// shared read-only image.
func (s *Store) SetDiskReadOnly(ctx context.Context, id int64, readOnly bool) (Disk, error) {
	disk, err := s.GetDisk(ctx, id)
	if err != nil {
		return Disk{}, err
	}
	if disk.ReadOnly == readOnly {
		return disk, nil
	}
	if readOnly && disk.IsTemplate {
		return Disk{}, ErrDiskReadOnlyTemplate
	}
	if err := s.checkDiskOwnership(ctx, disk, "", ""); err != nil {
		return Disk{}, err
	}
	now := time.Now().UTC().UnixMilli()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE disks SET read_only = ?, updated_at = ? WHERE id = ?`,
		readOnly, now, id); err != nil {
		return Disk{}, fmt.Errorf("update disk read-only flag: %w", err)
	}
	return s.GetDisk(ctx, id)
}

// SetDiskProvisioning stores the provisioning script and timeout for a template disk.
func (s *Store) SetDiskProvisioning(ctx context.Context, id int64, script, timeout string) (Disk, error) {
	disk, err := s.GetDisk(ctx, id)
	if err != nil {
		return Disk{}, err
	}
	if disk.Source != DiskSourceLVMThin {
		return Disk{}, errors.New("only LVM thin disks can be templates")
	}
	if err := s.checkDiskOwnership(ctx, disk, "", ""); err != nil {
		return Disk{}, err
	}
	if err := checkDiskNotDeleting(disk); err != nil {
		return Disk{}, err
	}
	if !disk.IsTemplate {
		return Disk{}, errors.New("provisioning settings require a template disk")
	}
	now := time.Now().UTC().UnixMilli()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE disks SET provisioning_script = ?, provisioning_timeout = ?, updated_at = ? WHERE id = ?`,
		script, timeout, now, id); err != nil {
		return Disk{}, fmt.Errorf("update disk provisioning settings: %w", err)
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
	if err := s.checkDiskOwnership(ctx, disk, "", ""); err != nil {
		return err
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

// diskAttachments returns the attachment list, falling back to the legacy
// single-owner fields for Disk values built without a query.
func diskAttachments(disk Disk) []DiskAttachment {
	if len(disk.Attachments) > 0 {
		return disk.Attachments
	}
	if disk.AttachedVMID != nil {
		return []DiskAttachment{{
			WorkloadKind: "vm", WorkloadID: *disk.AttachedVMID,
			WorkloadName: disk.AttachedVMName, ReadOnly: disk.ReadOnly,
		}}
	}
	if disk.AttachedLightVMID != nil {
		return []DiskAttachment{{
			WorkloadKind: "lightvm", WorkloadID: *disk.AttachedLightVMID,
			WorkloadName: disk.AttachedLightVMName, ReadOnly: disk.ReadOnly,
		}}
	}
	return nil
}

// diskHasAttachment reports whether a specific workload owns the disk.
func diskHasAttachment(disk Disk, kind, id string) bool {
	for _, attachment := range diskAttachments(disk) {
		if attachment.WorkloadKind == kind && attachment.WorkloadID == id {
			return true
		}
	}
	return false
}

// DiskAttachedToAnyWorkload reports whether the disk has any workload owner.
func DiskAttachedToAnyWorkload(disk Disk) bool {
	return len(diskAttachments(disk)) > 0
}

// insertWorkloadDisk inserts one attachment. The read-only snapshot comes
// from the disk row in the same statement so it cannot drift from the flag.
func (s *Store) insertWorkloadDisk(ctx context.Context, tx *sql.Tx, vmID any, lightVMID any, diskID int64, position int, serial string) error {
	res, err := tx.ExecContext(ctx, `
		INSERT INTO workload_disks (vm_id, lightvm_id, disk_id, position, serial, read_only)
		SELECT ?, ?, id, ?, ?, read_only FROM disks WHERE id = ?`,
		vmID, lightVMID, position, serial, diskID,
	)
	if err != nil {
		return s.workloadDiskAttachError(ctx, tx, err, diskID)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read inserted workload disk rows: %w", err)
	}
	if affected == 0 {
		return ErrDiskNotFound
	}
	return nil
}

// workloadDiskAttachError translates a failed workload-disk insert: it rolls the transaction back
// and re-reads the disk to return the conflicting owner as a DiskInUseError, ErrDiskNotFound for a
// vanished disk or foreign-key failure, and a wrapped error for unrelated failures.
func (s *Store) workloadDiskAttachError(ctx context.Context, tx *sql.Tx, err error, diskID int64) error {
	if !isUniqueViolation(err) && !isForeignKeyViolation(err) {
		return fmt.Errorf("attach workload disk: %w", err)
	}
	_ = tx.Rollback()
	disk, getErr := s.GetDisk(ctx, diskID)
	if errors.Is(getErr, ErrDiskNotFound) {
		return ErrDiskNotFound
	}
	if getErr != nil {
		return getErr
	}
	if ownerErr := s.checkDiskOwnership(ctx, disk, "", ""); ownerErr != nil {
		return ownerErr
	}
	if isForeignKeyViolation(err) {
		return ErrDiskNotFound
	}
	return &DiskInUseError{}
}

// checkDiskOwnership returns nil when the disk has no attachments or every attachment matches the
// supplied owner; otherwise it returns a DiskInUseError naming another owning workload.
func (s *Store) checkDiskOwnership(_ context.Context, disk Disk, ownerKind, ownerID string) error {
	attachments := diskAttachments(disk)
	for _, attachment := range attachments {
		if ownerKind != "" && attachment.WorkloadKind == ownerKind && attachment.WorkloadID == ownerID {
			continue
		}
		return &DiskInUseError{
			OwnerKind:       attachment.WorkloadKind,
			OwnerID:         attachment.WorkloadID,
			OwnerName:       attachment.WorkloadName,
			AttachmentCount: len(attachments),
		}
	}
	return nil
}

// checkDiskNotDeleting returns ErrDiskDeleting when the disk has entered any deletion state and nil
// otherwise, so callers refuse operations on a disk being reclaimed.
func checkDiskNotDeleting(disk Disk) error {
	if disk.DeletionState != "" {
		return ErrDiskDeleting
	}
	return nil
}

// checkDiskAvailableForAttach rejects a missing or deleting disk and templates, permits read-only
// disks to be shared, and otherwise requires the disk to be unattached or owned only by ownerKind
// and ownerID.
func (s *Store) checkDiskAvailableForAttach(ctx context.Context, diskID int64, ownerKind, ownerID string) error {
	disk, err := s.GetDisk(ctx, diskID)
	if err != nil {
		return err
	}
	if err := checkDiskNotDeleting(disk); err != nil {
		return err
	}
	if disk.IsTemplate {
		return ErrDiskIsTemplate
	}
	if disk.ReadOnly {
		return nil
	}
	return s.checkDiskOwnership(ctx, disk, ownerKind, ownerID)
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

// checkBootMedia accepts a nil boot media, reports ErrMediaNotFound for non-positive IDs, and
// otherwise confirms the referenced media row exists.
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

	transport := strings.TrimSpace(network.VNFTransport)
	if transport == "" {
		transport = "shared"
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO networks (name, bridge_domain_id, vnf_transport, ipv4_cidr, ipv6_cidr, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		network.Name, network.BridgeDomainID, transport, network.IPv4CIDR, network.IPv6CIDR, now, now,
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

// GetStartupScript returns the singleton Rookery startup script.
func (s *Store) GetStartupScript(ctx context.Context) (StartupScript, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT enabled, script, status, exit_code, output, ran_at, updated_at
		FROM startup_script WHERE id = 1`)
	var (
		script    StartupScript
		enabled   int64
		ranAt     sql.NullInt64
		updatedAt int64
	)
	if err := row.Scan(&enabled, &script.Script, &script.Status, &script.ExitCode,
		&script.Output, &ranAt, &updatedAt); err != nil {
		return StartupScript{}, fmt.Errorf("get startup script: %w", err)
	}
	script.Enabled = enabled != 0
	if ranAt.Valid {
		value := time.UnixMilli(ranAt.Int64).UTC()
		script.RanAt = &value
	}
	script.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return script, nil
}

// SetStartupScript stores the startup script configuration. Saving a new
// configuration resets the previous execution result; an enabled script is
// pending until the next API startup.
func (s *Store) SetStartupScript(ctx context.Context, enabled bool, script string) (StartupScript, error) {
	status := "disabled"
	if enabled {
		status = "pending"
	}
	enabledValue := 0
	if enabled {
		enabledValue = 1
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE startup_script
		SET enabled = ?, script = ?, status = ?, exit_code = -1, output = '',
		    ran_at = NULL, updated_at = ?
		WHERE id = 1`,
		enabledValue, script, status, time.Now().UTC().UnixMilli(),
	); err != nil {
		return StartupScript{}, fmt.Errorf("update startup script: %w", err)
	}
	return s.GetStartupScript(ctx)
}

// SetStartupScriptResult stores the outcome of a startup script run.
func (s *Store) SetStartupScriptResult(ctx context.Context, status string, exitCode int, output string, ranAt time.Time) (StartupScript, error) {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE startup_script
		SET status = ?, exit_code = ?, output = ?, ran_at = ?, updated_at = ?
		WHERE id = 1`,
		status, exitCode, output, ranAt.UTC().UnixMilli(), time.Now().UTC().UnixMilli(),
	); err != nil {
		return StartupScript{}, fmt.Errorf("update startup script result: %w", err)
	}
	return s.GetStartupScript(ctx)
}

// GetVPPStartupScript returns the VPP startup script and its last execution
// result.
func (s *Store) GetVPPStartupScript(ctx context.Context) (StartupScript, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT vpp_enabled, vpp_script, vpp_status, vpp_exit_code, vpp_output,
		       vpp_ran_at, vpp_updated_at
		FROM startup_script WHERE id = 1`)
	var (
		script    StartupScript
		enabled   int64
		ranAt     sql.NullInt64
		updatedAt int64
	)
	if err := row.Scan(&enabled, &script.Script, &script.Status, &script.ExitCode,
		&script.Output, &ranAt, &updatedAt); err != nil {
		return StartupScript{}, fmt.Errorf("get VPP startup script: %w", err)
	}
	script.Enabled = enabled != 0
	if ranAt.Valid {
		value := time.UnixMilli(ranAt.Int64).UTC()
		script.RanAt = &value
	}
	script.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return script, nil
}

// SetVPPStartupScript stores the VPP reconnect script configuration.
func (s *Store) SetVPPStartupScript(ctx context.Context, enabled bool, script string) (StartupScript, error) {
	status := "disabled"
	if enabled {
		status = "pending"
	}
	enabledValue := 0
	if enabled {
		enabledValue = 1
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE startup_script
		SET vpp_enabled = ?, vpp_script = ?, vpp_status = ?, vpp_exit_code = -1,
		    vpp_output = '', vpp_ran_at = NULL, vpp_updated_at = ?
		WHERE id = 1`,
		enabledValue, script, status, time.Now().UTC().UnixMilli(),
	); err != nil {
		return StartupScript{}, fmt.Errorf("update VPP startup script: %w", err)
	}
	return s.GetVPPStartupScript(ctx)
}

// SetVPPStartupScriptResult stores the outcome of a VPP startup script run.
func (s *Store) SetVPPStartupScriptResult(ctx context.Context, status string, exitCode int, output string, ranAt time.Time) (StartupScript, error) {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE startup_script
		SET vpp_status = ?, vpp_exit_code = ?, vpp_output = ?, vpp_ran_at = ?,
		    vpp_updated_at = ?
		WHERE id = 1`,
		status, exitCode, output, ranAt.UTC().UnixMilli(), time.Now().UTC().UnixMilli(),
	); err != nil {
		return StartupScript{}, fmt.Errorf("update VPP startup script result: %w", err)
	}
	return s.GetVPPStartupScript(ctx)
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

// SetNetworkVNFTransport updates the network's default VNF transport.
func (s *Store) SetNetworkVNFTransport(ctx context.Context, id int64, transport string) (Network, error) {
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		UPDATE networks SET vnf_transport = ?, updated_at = ? WHERE id = ?`,
		transport, now, id,
	)
	if err != nil {
		return Network{}, fmt.Errorf("update network VNF transport: %w", err)
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

// SetNetworkRouteTable stores the VPP VRF table assigned to a network.
func (s *Store) SetNetworkRouteTable(ctx context.Context, id int64, tableID uint32) (Network, error) {
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		UPDATE networks SET route_table_id = ?, updated_at = ? WHERE id = ?`,
		tableID, now, id,
	)
	if err != nil {
		return Network{}, fmt.Errorf("update network route table: %w", err)
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

// SetNetworkCIDRs stores the network address ranges.
func (s *Store) SetNetworkCIDRs(ctx context.Context, id int64, ipv4CIDR, ipv6CIDR string) (Network, error) {
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		UPDATE networks SET ipv4_cidr = ?, ipv6_cidr = ?, updated_at = ? WHERE id = ?`,
		ipv4CIDR, ipv6CIDR, now, id,
	)
	if err != nil {
		return Network{}, fmt.Errorf("update network CIDRs: %w", err)
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

// SetNetworkVNFMemif stores the shared VNF memif metadata for a network.
func (s *Store) SetNetworkVNFMemif(ctx context.Context, id int64, hostName, socketPath string, swIfIndex uint32) (Network, error) {
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		UPDATE networks
		SET vnf_memif_host_name = ?, vnf_memif_socket = ?, vnf_memif_sw_if_index = ?, updated_at = ?
		WHERE id = ?`,
		hostName, socketPath, swIfIndex, now, id,
	)
	if err != nil {
		return Network{}, fmt.Errorf("update network VNF memif: %w", err)
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
	var pipeID int64
	err = s.db.QueryRowContext(ctx,
		`SELECT id FROM network_pipes WHERE network_a_id = ? OR network_b_id = ? LIMIT 1`, id, id,
	).Scan(&pipeID)
	switch {
	case err == nil:
		return ErrNetworkInUse
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("check network pipe usage: %w", err)
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
	network, err := s.GetNetwork(ctx, iface.NetworkID)
	if err != nil {
		return VirtualInterface{}, err
	}
	if iface.IPv4TableID == 0 && iface.IPv6TableID == 0 && network.RouteTableID != 0 {
		iface.IPv4TableID = network.RouteTableID
		iface.IPv6TableID = network.RouteTableID
	}
	if iface.AttachedVMID != nil {
		if _, err := s.GetVM(ctx, *iface.AttachedVMID); err != nil {
			return VirtualInterface{}, err
		}
	}

	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO virtual_interfaces
			(name, network_id, socket_path, mac, sw_if_index, attached_vm_id,
			 ipv4_table_id, ipv6_table_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		iface.Name, iface.NetworkID, iface.SocketPath, iface.MAC, iface.SwIfIndex, iface.AttachedVMID,
		iface.IPv4TableID, iface.IPv6TableID, now, now,
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
	if err := s.loadInterfaceACLs(ctx, interfaces); err != nil {
		return nil, err
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
	list := []VirtualInterface{iface}
	if err := s.loadInterfaceACLs(ctx, list); err != nil {
		return VirtualInterface{}, err
	}
	return list[0], nil
}

// loadInterfaceACLs fills the ordered ACL lists for the given interfaces.
func (s *Store) loadInterfaceACLs(ctx context.Context, interfaces []VirtualInterface) error {
	if len(interfaces) == 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT ia.interface_id, ia.direction, ia.acl_id, a.name
		FROM interface_acls AS ia
		JOIN acls AS a ON a.id = ia.acl_id
		ORDER BY ia.interface_id, ia.direction, ia.position`)
	if err != nil {
		return fmt.Errorf("query interface ACLs: %w", err)
	}
	defer rows.Close()

	byID := make(map[int64]*VirtualInterface, len(interfaces))
	for i := range interfaces {
		byID[interfaces[i].ID] = &interfaces[i]
	}
	for rows.Next() {
		var (
			interfaceID int64
			direction   string
			aclID       int64
			name        string
		)
		if err := rows.Scan(&interfaceID, &direction, &aclID, &name); err != nil {
			return fmt.Errorf("scan interface ACL: %w", err)
		}
		iface := byID[interfaceID]
		if iface == nil {
			continue
		}
		ref := InterfaceACL{ID: aclID, Name: name}
		if direction == "ingress" {
			iface.IngressACLs = append(iface.IngressACLs, ref)
		} else {
			iface.EgressACLs = append(iface.EgressACLs, ref)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate interface ACLs: %w", err)
	}
	for i := range interfaces {
		iface := &interfaces[i]
		if len(iface.IngressACLs) > 0 {
			iface.IngressACLID = &iface.IngressACLs[0].ID
			iface.IngressACLName = iface.IngressACLs[0].Name
		}
		if len(iface.EgressACLs) > 0 {
			iface.EgressACLID = &iface.EgressACLs[0].ID
			iface.EgressACLName = iface.EgressACLs[0].Name
		}
	}
	return nil
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
		if err := s.checkLightVMInterfaceUse(ctx, id, ""); err != nil {
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
		if err := s.checkLightVMInterfaceUse(ctx, id, ""); err != nil {
			return VM{}, err
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
	if err := s.checkLightVMInterfaceUse(ctx, id, ""); err != nil {
		return err
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
	ids := make([]string, 0, len(vms))
	for _, vm := range vms {
		ids = append(ids, vm.ID)
	}
	byOwner, err := s.loadWorkloadDisks(ctx, "vm", ids)
	if err != nil {
		return err
	}
	for i := range vms {
		disks := byOwner[vms[i].ID]
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

// loadWorkloadDisks loads ordered disk attachments for one workload kind.
func (s *Store) loadWorkloadDisks(ctx context.Context, ownerKind string, ownerIDs []string) (map[string][]VMDisk, error) {
	result := make(map[string][]VMDisk)
	if len(ownerIDs) == 0 {
		return result, nil
	}
	ownerColumn := "wd.vm_id"
	switch ownerKind {
	case "vm":
	case "lightvm":
		ownerColumn = "wd.lightvm_id"
	default:
		return nil, fmt.Errorf("unsupported workload disk owner kind %q", ownerKind)
	}
	placeholders := make([]string, len(ownerIDs))
	args := make([]any, len(ownerIDs))
	for i, id := range ownerIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	query := `
		SELECT ` + ownerColumn + `, d.id, d.name, d.size_gb, d.vg, d.thin_pool,
		       d.storage_backend_id, sb.name, d.source, d.device, d.size_bytes,
		       d.attachment_protocol, wd.serial, wd.read_only,
		       d.throttle_class_id, dtc.name, dtc.limits
		FROM workload_disks AS wd
		JOIN disks AS d ON d.id = wd.disk_id
		LEFT JOIN storage_backends AS sb ON sb.id = d.storage_backend_id
		LEFT JOIN disk_throttle_classes AS dtc ON dtc.id = d.throttle_class_id
		WHERE ` + ownerColumn + ` IN (` + strings.Join(placeholders, ",") + `)
		ORDER BY ` + ownerColumn + `, wd.position, d.name COLLATE NOCASE`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query workload disks: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			vmID        string
			disk        VMDisk
			backend     sql.NullInt64
			backendName sql.NullString
			vg          string
			pool        string
			device      string
			sizeBytes   int64
			classID     sql.NullInt64
			className   sql.NullString
			classLimits sql.NullString
			readOnly    int64
		)
		if err := rows.Scan(&vmID, &disk.ID, &disk.Name, &disk.SizeGB, &vg, &pool,
			&backend, &backendName, &disk.Source, &device, &sizeBytes, &disk.Protocol, &disk.Serial, &readOnly,
			&classID, &className, &classLimits); err != nil {
			return nil, err
		}
		disk.ReadOnly = readOnly != 0
		disk.VG = vg
		disk.ThinPool = pool
		disk.SizeBytes = uint64(sizeBytes)
		disk.Device = device
		if disk.Protocol == "" {
			disk.Protocol = string(blockdev.ProtocolLocal)
		}
		if disk.Source == DiskSourceLVMThin && disk.Device == "" {
			disk.Device = devicePath(vg, disk.Name)
		}
		if backend.Valid {
			id := backend.Int64
			disk.StorageBackendID = &id
		}
		if backendName.Valid {
			disk.StorageBackendName = backendName.String
		}
		applyVMDiskThrottle(&disk, classID, className, classLimits)
		result[vmID] = append(result[vmID], disk)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workload disks: %w", err)
	}
	return result, nil
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
	       n.vnf_transport, n.vnf_memif_socket, n.vnf_memif_sw_if_index,
	       n.vnf_memif_host_name, n.ipv4_cidr, n.ipv6_cidr,
	       n.route_table_id, n.created_at, n.updated_at
	FROM networks AS n`

const interfaceSelect = `
	SELECT i.id, i.name, i.network_id, n.name, i.socket_path, i.mac, i.sw_if_index,
	       i.attached_vm_id, v.name, li.lightvm_id, lv.name,
	       i.ingress_acl_id, ai.name, i.egress_acl_id, ae.name,
	       i.ipv4_table_id, COALESCE(r4.name, 'default'),
	       i.ipv6_table_id, COALESCE(r6.name, 'default'),
	       i.created_at, i.updated_at
	FROM virtual_interfaces AS i
	JOIN networks AS n ON n.id = i.network_id
	LEFT JOIN vms AS v ON v.id = i.attached_vm_id
	LEFT JOIN lightvm_interfaces AS li ON li.interface_id = i.id
	LEFT JOIN lightvms AS lv ON lv.id = li.lightvm_id
	LEFT JOIN acls AS ai ON ai.id = i.ingress_acl_id
	LEFT JOIN acls AS ae ON ae.id = i.egress_acl_id
	LEFT JOIN route_tables AS r4 ON r4.table_id = i.ipv4_table_id AND r4.is_ipv6 = 0
	LEFT JOIN route_tables AS r6 ON r6.table_id = i.ipv6_table_id AND r6.is_ipv6 = 1`

// scanNetwork decodes one joined network row, narrowing bridge-domain, tap, BVI, memif, and
// route-table indexes to uint32, mapping the DHCP flag to a bool, and converting timestamps to UTC.
func scanNetwork(s scanner) (Network, error) {
	var (
		network      Network
		bdID         int64
		dhcp         int64
		tapSwIf      int64
		bviSwIf      int64
		vnfMemif     int64
		routeTableID int64
		createdAt    int64
		updatedAt    int64
	)
	if err := s.Scan(
		&network.ID, &network.Name, &bdID, &network.InterfaceCount,
		&dhcp, &network.DHCPGateway, &network.DHCPRangeStart, &network.DHCPRangeEnd,
		&network.DHCPDNS, &tapSwIf, &network.DHCPTapHostName,
		&network.BVIHostName, &bviSwIf, &network.IPv4Gateway,
		&network.VNFTransport, &network.VNFMemifSocket, &vnfMemif,
		&network.VNFMemifHostName, &network.IPv4CIDR, &network.IPv6CIDR,
		&routeTableID, &createdAt, &updatedAt,
	); err != nil {
		return Network{}, err
	}
	network.BridgeDomainID = uint32(bdID)
	network.DHCPEnabled = dhcp != 0
	network.DHCPTapSwIfIndex = uint32(tapSwIf)
	network.BVISwIfIndex = uint32(bviSwIf)
	network.VNFMemifSwIfIndex = uint32(vnfMemif)
	network.RouteTableID = uint32(routeTableID)
	network.CreatedAt = time.UnixMilli(createdAt).UTC()
	network.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return network, nil
}

// scanInterface decodes one joined interface row, filling nullable VM, LightVM, and ACL columns as
// pointers/names, narrowing table and sw_if_index values, and converting timestamps to UTC.
func scanInterface(s scanner) (VirtualInterface, error) {
	var (
		iface       VirtualInterface
		swIf        int64
		vmID        sql.NullString
		vmName      sql.NullString
		lightVMID   sql.NullString
		lightVMName sql.NullString
		ingressID   sql.NullInt64
		ingress     sql.NullString
		egressID    sql.NullInt64
		egress      sql.NullString
		ipv4Table   int64
		ipv6Table   int64
		createdAt   int64
		updatedAt   int64
	)
	if err := s.Scan(
		&iface.ID, &iface.Name, &iface.NetworkID, &iface.NetworkName,
		&iface.SocketPath, &iface.MAC, &swIf, &vmID, &vmName,
		&lightVMID, &lightVMName,
		&ingressID, &ingress, &egressID, &egress,
		&ipv4Table, &iface.IPv4TableName, &ipv6Table, &iface.IPv6TableName,
		&createdAt, &updatedAt,
	); err != nil {
		return VirtualInterface{}, err
	}
	iface.SwIfIndex = uint32(swIf)
	iface.IPv4TableID = uint32(ipv4Table)
	iface.IPv6TableID = uint32(ipv6Table)
	if vmID.Valid {
		id := vmID.String
		iface.AttachedVMID = &id
	}
	if vmName.Valid {
		iface.AttachedVMName = vmName.String
	}
	if lightVMID.Valid {
		id := lightVMID.String
		iface.AttachedLightVMID = &id
	}
	if lightVMName.Valid {
		iface.AttachedLightVMName = lightVMName.String
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
	SELECT v.id, v.name, v.description, v.status, v.desired_state, v.vcpus, v.memory_mb,
	       v.boot_media_id, m.name, m.path, m.size_bytes,
	       v.console_enabled, v.cgroup_class, v.created_at, v.updated_at
	FROM vms AS v
	LEFT JOIN media AS m ON m.id = v.boot_media_id`

const diskSelect = `
	SELECT d.id, d.name, d.size_gb, d.vg, d.thin_pool,
	       d.storage_backend_id, sb.name, d.source, d.device, d.size_bytes,
	       d.attachment_protocol, d.backend_metadata,
	       d.throttle_class_id, dtc.name,
	       d.deletion_state, d.deletion_error, d.deletion_attempts,
	       d.deletion_next_attempt_at, d.deletion_claimed_at,
	       d.is_template, d.template_disk_id,
	       d.provisioning_script, d.provisioning_timeout, d.prepared_by_script,
	       td.name, d.read_only, d.created_at, d.updated_at
	FROM disks AS d
	LEFT JOIN storage_backends AS sb ON sb.id = d.storage_backend_id
	LEFT JOIN disk_throttle_classes AS dtc ON dtc.id = d.throttle_class_id
	LEFT JOIN disks AS td ON td.id = d.template_disk_id`

// scanVM decodes one boot-media-joined VM row, mapping the console flag, trimming the cgroup class,
// and populating BootMedia only when the joined media columns are present; timestamps become UTC.
func scanVM(s scanner) (VM, error) {
	var (
		vm          VM
		createdAt   int64
		updatedAt   int64
		bootMediaID sql.NullInt64
		mediaName   sql.NullString
		mediaPath   sql.NullString
		mediaSize   sql.NullInt64
		console     int64
		cgroupClass string
	)
	if err := s.Scan(
		&vm.ID,
		&vm.Name,
		&vm.Description,
		&vm.Status,
		&vm.DesiredState,
		&vm.VCPUs,
		&vm.MemoryMB,
		&bootMediaID,
		&mediaName,
		&mediaPath,
		&mediaSize,
		&console,
		&cgroupClass,
		&createdAt,
		&updatedAt,
	); err != nil {
		return VM{}, err
	}

	vm.ConsoleEnabled = console != 0
	vm.CgroupClass = strings.TrimSpace(cgroupClass)

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

// applyVMDiskThrottle fills a disk's throttle class ID, name, and decoded limits from joined columns,
// recording ThrottleError when the class row is missing or its stored limits are invalid; it is a
// no-op for a nil disk or an unset class ID.
func applyVMDiskThrottle(disk *VMDisk, classID sql.NullInt64, className, classLimits sql.NullString) {
	if disk == nil || !classID.Valid {
		return
	}
	id := classID.Int64
	disk.ThrottleClassID = &id
	disk.ThrottleClassName = className.String
	if !classLimits.Valid {
		disk.ThrottleError = fmt.Sprintf("throttle class %d not found", id)
		return
	}
	limits, validationError := decodeDiskThrottleLimits(classLimits.String)
	if validationError != "" {
		disk.ThrottleError = fmt.Sprintf("throttle class %d: %s", id, validationError)
		return
	}
	disk.Throttle = &limits
}

// scanDisk decodes one joined disk row, defaulting a blank source to LVM thin and deriving the
// device path for thin volumes, decoding backend metadata (failing on malformed JSON), defaulting
// the protocol, and converting optional deletion times and timestamps to UTC.
func scanDisk(s scanner) (Disk, error) {
	var (
		disk                  Disk
		backendID             sql.NullInt64
		backend               sql.NullString
		sizeBytes             int64
		backendMetadata       string
		classID               sql.NullInt64
		className             sql.NullString
		deletionNextAttemptAt int64
		deletionClaimedAt     int64
		isTemplate            int64
		templateID            sql.NullInt64
		provisioningScript    string
		provisioningTimeout   string
		preparedByScript      int64
		templateName          sql.NullString
		readOnly              int64
		createdAt             int64
		updatedAt             int64
	)
	if err := s.Scan(
		&disk.ID,
		&disk.Name,
		&disk.SizeGB,
		&disk.VG,
		&disk.ThinPool,
		&backendID,
		&backend,
		&disk.Source,
		&disk.Device,
		&sizeBytes,
		&disk.Protocol,
		&backendMetadata,
		&classID,
		&className,
		&disk.DeletionState,
		&disk.DeletionError,
		&disk.DeletionAttempts,
		&deletionNextAttemptAt,
		&deletionClaimedAt,
		&isTemplate,
		&templateID,
		&provisioningScript,
		&provisioningTimeout,
		&preparedByScript,
		&templateName,
		&readOnly,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Disk{}, err
	}

	disk.SizeBytes = uint64(sizeBytes)
	if classID.Valid {
		id := classID.Int64
		disk.ThrottleClassID = &id
	}
	if className.Valid {
		disk.ThrottleClassName = className.String
	}
	if disk.Source == "" {
		disk.Source = DiskSourceLVMThin
	}
	if disk.Source == DiskSourceLVMThin {
		if disk.Device == "" {
			disk.Device = devicePath(disk.VG, disk.Name)
		}
	}
	metadata, err := DecodeDiskBackendMetadata(backendMetadata)
	if err != nil {
		return Disk{}, err
	}
	disk.BackendMetadata = metadata
	if disk.Protocol == "" {
		if metadata.Attachment.Protocol != "" {
			disk.Protocol = string(metadata.Attachment.Protocol)
		} else {
			disk.Protocol = string(blockdev.ProtocolLocal)
		}
	}
	if deletionNextAttemptAt > 0 {
		disk.DeletionNextAttemptAt = time.UnixMilli(deletionNextAttemptAt).UTC()
	}
	if deletionClaimedAt > 0 {
		disk.DeletionClaimedAt = time.UnixMilli(deletionClaimedAt).UTC()
	}
	disk.IsTemplate = isTemplate != 0
	disk.ReadOnly = readOnly != 0
	disk.ProvisioningScript = provisioningScript
	disk.ProvisioningTimeout = provisioningTimeout
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
	disk.CreatedAt = time.UnixMilli(createdAt).UTC()
	disk.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return disk, nil
}

// scanMedia decodes one media row, converting its millisecond timestamps to UTC.
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

// devicePath returns the conventional LVM device path /dev/<vg>/<name> for a thin volume.
func devicePath(vg, name string) string {
	return "/dev/" + vg + "/" + name
}

// classifyVMCreateError maps a constraint failure during VM creation to ErrMediaNotFound or
// ErrDiskNotFound for foreign keys (by referenced column) and to a quoted ErrNameTaken for a
// uniqueness clash, returning nil when err is unrelated so callers can keep their own wrapping.
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
	return fmt.Errorf("%w: %q", ErrNameTaken, name)
}

// reportLegacyDiskConflicts warns about pre-version-53 vms.disk_id pointers that migration will not
// carry over, skipping VMs that already have vm_disks rows, reference a missing disk, or point at a
// disk owned by another VM; it only reads the database and logs.
func reportLegacyDiskConflicts(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version for legacy disk report: %w", err)
	}
	if version >= 53 {
		return nil
	}
	var hasVMDisks, hasDiskID int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name = 'vm_disks'`).Scan(&hasVMDisks); err != nil {
		return fmt.Errorf("inspect vm_disks table: %w", err)
	}
	if hasVMDisks == 0 {
		return nil
	}
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pragma_table_info('vms') WHERE name = 'disk_id'`).Scan(&hasDiskID); err != nil {
		return fmt.Errorf("inspect vms.disk_id column: %w", err)
	}
	if hasDiskID == 0 {
		return nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT v.id, v.name, v.disk_id, COALESCE(d.name, ''),
		       EXISTS (SELECT 1 FROM vm_disks vd WHERE vd.vm_id = v.id),
		       EXISTS (SELECT 1 FROM vm_disks vd2
		               WHERE vd2.disk_id = v.disk_id AND vd2.vm_id <> v.id)
		FROM vms AS v
		LEFT JOIN disks AS d ON d.id = v.disk_id
		WHERE v.disk_id IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("query legacy vm disk pointers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			vmID, vmName string
			diskID       int64
			diskName     string
			hasRows      int
			ownedElse    int
		)
		if err := rows.Scan(&vmID, &vmName, &diskID, &diskName, &hasRows, &ownedElse); err != nil {
			return fmt.Errorf("scan legacy vm disk pointer: %w", err)
		}
		reason := ""
		switch {
		case hasRows != 0:
			reason = "ignored because the VM already has vm_disks rows"
		case diskName == "":
			reason = "skipped because the disk row is missing"
		case ownedElse != 0:
			reason = "skipped because the disk is owned by another VM"
		}
		if reason == "" {
			continue
		}
		log.Warn().
			Str("vm_id", vmID).
			Str("vm_name", vmName).
			Int64("disk_id", diskID).
			Str("disk_name", diskName).
			Str("reason", reason).
			Msg("legacy vms.disk_id pointer will not be migrated")
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate legacy vm disk pointers: %w", err)
	}
	return nil
}

// migrate applies every embedded .sql migration whose numeric version exceeds PRAGMA user_version,
// in filename order and each inside its own transaction that also records the new version.
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

// migrationVersion extracts the positive numeric version prefix before the first underscore of a
// migration filename, rejecting names without a separator or with a non-numeric version.
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

// isUniqueViolation reports whether err is a SQLite primary-key or unique-constraint violation.
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

// isForeignKeyViolation reports whether err is a SQLite foreign-key constraint violation.
func isForeignKeyViolation(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	return sqliteErr.Code() == sqliteConstraintForeignKey
}
