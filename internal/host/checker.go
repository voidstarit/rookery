package host

import (
	"context"
	"fmt"
	"strings"
)

const (
	// StateReady means the dependency is usable.
	StateReady = "ready"
	// StateNotConfigured means no configuration was supplied.
	StateNotConfigured = "not_configured"
	// StateMissing means a configured resource does not exist.
	StateMissing = "missing"
	// StateUnreachable means a configured endpoint could not be contacted.
	StateUnreachable = "unreachable"
	// StateError means the check could not be completed.
	StateError = "error"
	// StateChecking is used before the first check completes.
	StateChecking = "checking"
)

// Result is the outcome of inspecting one host dependency.
type Result struct {
	Ready  bool
	State  string
	Detail string
}

// Checker inspects the local host prerequisites.
type Checker struct {
	LVM *LVM
	VPP *VPP
}

// NewChecker returns a checker backed by the local LVM tools and VPP socket.
func NewChecker() *Checker {
	return &Checker{
		LVM: NewLVM(),
		VPP: NewVPP(),
	}
}

// InspectThinPool reports whether the configured thin pool exists.
func (c *Checker) InspectThinPool(ctx context.Context, vg, name string) Result {
	vg = strings.TrimSpace(vg)
	name = strings.TrimSpace(name)

	switch {
	case vg == "":
		return Result{
			State:  StateNotConfigured,
			Detail: "volume group is not configured; pass -vg or use -thin-pool <vg>/<name>",
		}
	case name == "":
		return Result{
			State:  StateNotConfigured,
			Detail: "thin pool is not configured; pass -thin-pool <name>",
		}
	}

	if err := ValidateLVMName("volume group", vg); err != nil {
		return Result{State: StateError, Detail: err.Error()}
	}
	if err := ValidateLVMName("thin pool", name); err != nil {
		return Result{State: StateError, Detail: err.Error()}
	}

	exists, err := c.LVM.VolumeGroupExists(ctx, vg)
	if err != nil {
		return Result{
			State:  StateError,
			Detail: fmt.Sprintf("inspect volume group %q: %v", vg, err),
		}
	}
	if !exists {
		return Result{
			State:  StateMissing,
			Detail: fmt.Sprintf("volume group %q does not exist", vg),
		}
	}

	info, err := c.LVM.ThinPool(ctx, vg, name)
	if err != nil {
		return Result{
			State:  StateError,
			Detail: fmt.Sprintf("inspect thin pool %s/%s: %v", vg, name, err),
		}
	}
	if !info.Exists {
		return Result{
			State:  StateMissing,
			Detail: fmt.Sprintf("thin pool %s/%s does not exist; run `nerds-vms-api init -vg %s -thin-pool %s`", vg, name, vg, name),
		}
	}
	if !info.IsThinPool {
		return Result{
			State:  StateError,
			Detail: fmt.Sprintf("logical volume %s/%s exists but is not a thin pool", vg, name),
		}
	}

	return Result{
		Ready:  true,
		State:  StateReady,
		Detail: "volume group and thin pool found",
	}
}

// InspectVPP reports whether the configured VPP API socket answers a ping.
func (c *Checker) InspectVPP(ctx context.Context, socket string) Result {
	socket = strings.TrimSpace(socket)
	if socket == "" {
		return Result{
			State:  StateNotConfigured,
			Detail: "VPP API socket is not configured; pass -vpp-socket",
		}
	}

	if err := c.VPP.Ping(ctx, socket); err != nil {
		return Result{
			State:  StateUnreachable,
			Detail: err.Error(),
		}
	}

	return Result{
		Ready:  true,
		State:  StateReady,
		Detail: "control ping succeeded",
	}
}
