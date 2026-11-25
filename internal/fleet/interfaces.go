// Package fleet defines the small contract between the API worker and the
// gateway fleet controller. It deliberately has no dependency on either
// package, so both can implement it without an import cycle.
package fleet

import "context"

// Guard reports whether the VPP network phase is ready for provisioning work.
type Guard interface {
	// Epoch returns the current VPP-network epoch.
	Epoch() uint64
	// CanProvision reports whether epoch still matches the current epoch and
	// the VPP network phase is ready.
	CanProvision(epoch uint64) bool
}

// Reconciler is implemented by the gateway fleet controller and driven by the
// API worker.
type Reconciler interface {
	ReconcileFleet(ctx context.Context, guard Guard) error
	// WakeFleet coalesces a wakeup without blocking.
	WakeFleet()
	// FleetWake is the worker receive side.
	FleetWake() <-chan struct{}
}
