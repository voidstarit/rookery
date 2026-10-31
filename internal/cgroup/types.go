// Package cgroup manages the cgroup v2 hierarchy used to constrain QEMU
// processes. Host settings are applied to a parent cgroup and reusable class
// definitions form a tree below it; VMs run directly in leaf class cgroups or
// the shared cgroup.
package cgroup

import "time"

const (
	// DefaultRoot is the Rookery cgroup below the cgroup2 mount point.
	DefaultRoot = "/sys/fs/cgroup/rookery-cgroup"
	// sharedName is the child cgroup used by VMs without a class.
	sharedName = "shared"
)

// IOLimit is a per-block-device I/O constraint. Zero values mean "no limit"
// (or inherit the host setting when used in a class override).
type IOLimit struct {
	Device    string `json:"device"`
	ReadBPS   int64  `json:"read_bps,omitempty"`
	WriteBPS  int64  `json:"write_bps,omitempty"`
	ReadIOPS  int64  `json:"read_iops,omitempty"`
	WriteIOPS int64  `json:"write_iops,omitempty"`
	Weight    int    `json:"weight,omitempty"`
}

// Class is a named, hierarchical cgroup profile. Name uses slash-separated
// path segments such as "production/web"; parent cgroups are created
// automatically. Zero values inherit the host setting or the parent class.
type Class struct {
	Name           string    `json:"name"`
	CPUs           string    `json:"cpus,omitempty"`
	MemoryMaxBytes int64     `json:"memory_max_bytes,omitempty"`
	CPUWeight      int       `json:"cpu_weight,omitempty"`
	IOLimits       []IOLimit `json:"io_limits,omitempty"`
}

// Settings are the host-wide cgroup limits and class definitions configured
// from the Settings tab.
type Settings struct {
	Enabled        bool      `json:"enabled"`
	CPUs           string    `json:"cpus,omitempty"`
	Mems           string    `json:"mems,omitempty"`
	MemoryMaxBytes int64     `json:"memory_max_bytes,omitempty"`
	CPUWeight      int       `json:"cpu_weight,omitempty"`
	IOLimits       []IOLimit `json:"io_limits,omitempty"`
	Classes        []Class   `json:"classes,omitempty"`
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
