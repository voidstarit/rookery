// Package cgroup manages the cgroup v2 hierarchy used to constrain QEMU
// processes. The host-level settings are applied to a parent cgroup while each
// VM can optionally receive its own child cgroup.
package cgroup

import (
	"slices"
	"time"
)

const (
	// DefaultRoot is the Rookery cgroup below the cgroup2 mount point.
	DefaultRoot = "/sys/fs/cgroup/rookery"
	// sharedName is the child cgroup used by VMs without per-VM limits.
	sharedName = "shared"
	// vmPrefix prefixes per-VM child cgroup names.
	vmPrefix = "vm-"
)

// IOLimit is a per-block-device I/O constraint. Zero values mean "no limit"
// (or inherit the host setting when used in a VM override).
type IOLimit struct {
	Device    string `json:"device"`
	ReadBPS   int64  `json:"read_bps,omitempty"`
	WriteBPS  int64  `json:"write_bps,omitempty"`
	ReadIOPS  int64  `json:"read_iops,omitempty"`
	WriteIOPS int64  `json:"write_iops,omitempty"`
	Weight    int    `json:"weight,omitempty"`
}

// Settings are the host-wide cgroup limits configured from the Settings tab.
type Settings struct {
	Enabled        bool      `json:"enabled"`
	CPUs           string    `json:"cpus,omitempty"`
	Mems           string    `json:"mems,omitempty"`
	MemoryMaxBytes int64     `json:"memory_max_bytes,omitempty"`
	CPUWeight      int       `json:"cpu_weight,omitempty"`
	IOLimits       []IOLimit `json:"io_limits,omitempty"`
}

// VMLimits are the optional per-VM overrides. When Enabled is false the VM
// runs in the shared cgroup and only inherits host limits.
type VMLimits struct {
	Enabled        bool      `json:"enabled,omitempty"`
	CPUs           string    `json:"cpus,omitempty"`
	MemoryMaxBytes int64     `json:"memory_max_bytes,omitempty"`
	CPUWeight      int       `json:"cpu_weight,omitempty"`
	IOLimits       []IOLimit `json:"io_limits,omitempty"`
}

// Placement describes the cgroup a QEMU process should be placed in.
type Placement struct {
	Path    string
	Enabled bool
}

// Status reports cgroup v2 availability and the effective configuration path.
type Status struct {
	Available   bool      `json:"available"`
	Configured  bool      `json:"configured"`
	Enabled     bool      `json:"enabled"`
	Reason      string    `json:"reason,omitempty"`
	Mount       string    `json:"mount,omitempty"`
	Root        string    `json:"root,omitempty"`
	Controllers []string  `json:"controllers,omitempty"`
	OnlineCPUs  []int     `json:"online_cpus,omitempty"`
	CPUModel    string    `json:"cpu_model,omitempty"`
	CheckedAt   time.Time `json:"checked_at"`
	LastError   string    `json:"last_error,omitempty"`
}

// CgroupSettings pairs stored settings with their modification time.
type CgroupSettings struct {
	Settings
	UpdatedAt time.Time `json:"updated_at"`
}

// Empty reports whether the VM has any explicit cgroup configuration.
func (v VMLimits) Empty() bool {
	return !v.Enabled && v.CPUs == "" && v.MemoryMaxBytes == 0 && v.CPUWeight == 0 && len(v.IOLimits) == 0
}

// Equal reports whether two per-VM limit sets are identical.
func (v VMLimits) Equal(other VMLimits) bool {
	return v.Enabled == other.Enabled &&
		v.CPUs == other.CPUs &&
		v.MemoryMaxBytes == other.MemoryMaxBytes &&
		v.CPUWeight == other.CPUWeight &&
		slices.Equal(v.IOLimits, other.IOLimits)
}
