// Package readiness tracks whether the host prerequisites for VM creation are
// ready: a usable LVM thin pool and a reachable FD.io VPP API.
package readiness

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/voidstarit/rookery/internal/host"
)

// Config identifies the host resources the server depends on.
type Config struct {
	VG        string
	ThinPool  string
	VPPSocket string
}

// Inspector performs the individual host dependency checks.
type Inspector interface {
	InspectThinPool(ctx context.Context, vg, name string) host.Result
	InspectVPP(ctx context.Context, socket string) host.Result
}

// ComponentStatus is the status of one prerequisite.
type ComponentStatus struct {
	Ready     bool       `json:"ready"`
	State     string     `json:"state"`
	Detail    string     `json:"detail,omitempty"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
}

// ThinPoolStatus describes the configured thin pool.
type ThinPoolStatus struct {
	ComponentStatus
	VG   string `json:"vg,omitempty"`
	Name string `json:"name,omitempty"`
}

// VPPStatus describes the configured VPP API connection.
type VPPStatus struct {
	ComponentStatus
	Socket string `json:"socket,omitempty"`
}

// Status is the overall readiness state exposed by the API and UI.
type Status struct {
	Ready     bool           `json:"ready"`
	CheckedAt *time.Time     `json:"checked_at,omitempty"`
	ThinPool  ThinPoolStatus `json:"thin_pool"`
	VPP       VPPStatus      `json:"vpp"`
}

// Manager checks dependencies periodically and caches the latest status.
type Manager struct {
	cfg       Config
	inspector Inspector
	logger    zerolog.Logger
	interval  time.Duration

	mu     sync.RWMutex
	status Status
}

// NewManager returns a readiness manager. The initial status is "checking";
// call Run or Refresh to populate it.
func NewManager(cfg Config, inspector Inspector, logger zerolog.Logger, interval time.Duration) *Manager {
	if interval <= 0 {
		interval = 10 * time.Second
	}

	return &Manager{
		cfg:       cfg,
		inspector: inspector,
		logger:    logger,
		interval:  interval,
		status: Status{
			ThinPool: ThinPoolStatus{
				ComponentStatus: ComponentStatus{State: host.StateChecking},
				VG:              cfg.VG,
				Name:            cfg.ThinPool,
			},
			VPP: VPPStatus{
				ComponentStatus: ComponentStatus{State: host.StateChecking},
				Socket:          cfg.VPPSocket,
			},
		},
	}
}

// Run refreshes immediately and then on the configured interval until ctx is
// canceled.
func (m *Manager) Run(ctx context.Context) {
	m.Refresh(ctx)

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.Refresh(ctx)
		}
	}
}

// Refresh performs the checks now and updates the cached status.
func (m *Manager) Refresh(ctx context.Context) Status {
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	thinPool := m.inspector.InspectThinPool(checkCtx, cfg.VG, cfg.ThinPool)
	vpp := m.inspector.InspectVPP(checkCtx, cfg.VPPSocket)

	now := time.Now().UTC()
	status := Status{
		Ready:     thinPool.Ready && vpp.Ready,
		CheckedAt: &now,
		ThinPool: ThinPoolStatus{
			ComponentStatus: ComponentStatus{
				Ready:     thinPool.Ready,
				State:     thinPool.State,
				Detail:    thinPool.Detail,
				CheckedAt: &now,
			},
			VG:   cfg.VG,
			Name: cfg.ThinPool,
		},
		VPP: VPPStatus{
			ComponentStatus: ComponentStatus{
				Ready:     vpp.Ready,
				State:     vpp.State,
				Detail:    vpp.Detail,
				CheckedAt: &now,
			},
			Socket: cfg.VPPSocket,
		},
	}

	m.mu.Lock()
	previous := m.status
	m.status = status
	m.mu.Unlock()

	m.logTransition(previous, status)
	return status
}

// StorageConfig returns the thin pool currently targeted by readiness checks.
func (m *Manager) StorageConfig() (vg, thinPool string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.VG, m.cfg.ThinPool
}

// SetStorage changes the thin pool targeted by subsequent checks. It is used
// when the API has no bootstrap -vg/-thin-pool flags and a storage backend is
// registered through the UI.
func (m *Manager) SetStorage(vg, thinPool string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg.VG = vg
	m.cfg.ThinPool = thinPool
	m.status.ThinPool.VG = vg
	m.status.ThinPool.Name = thinPool
	m.status.ThinPool.State = host.StateChecking
	m.status.ThinPool.Detail = ""
	m.status.ThinPool.Ready = false
	m.status.ThinPool.CheckedAt = nil
	m.status.Ready = false
}

// Ready reports the latest cached readiness.
func (m *Manager) Ready() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status.Ready
}

// Status returns the latest cached status.
func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

func (m *Manager) logTransition(previous, next Status) {
	if previous.Ready == next.Ready &&
		previous.ThinPool.State == next.ThinPool.State &&
		previous.VPP.State == next.VPP.State {
		return
	}

	if next.Ready {
		m.logger.Info().
			Bool("ready", next.Ready).
			Str("thin_pool_state", next.ThinPool.State).
			Str("vpp_state", next.VPP.State).
			Msg("VM prerequisites ready")
		return
	}
	m.logger.Warn().
		Bool("ready", next.Ready).
		Str("thin_pool_state", next.ThinPool.State).
		Str("vpp_state", next.VPP.State).
		Str("thin_pool_detail", next.ThinPool.Detail).
		Str("vpp_detail", next.VPP.Detail).
		Msg("VM prerequisites not ready")
}
