package kouja

import (
	"context"
	"fmt"
	"strings"
	"time"

	koujav1 "github.com/voidstarit/kouja/gen/kouja/v1"

	"github.com/voidstarit/rookery/internal/blockdev"
	"github.com/voidstarit/rookery/internal/store"
)

// Health is the control-plane health of a kouja backend.
type Health struct {
	Reachable bool
	Ready     bool
	Detail    string
	Protocols []string
	Classes   []ClassCapacity
	Owner     string
	CheckedAt time.Time
}

// ClassCapacity describes one kouja storage class.
type ClassCapacity struct {
	Name             string
	VGName           string
	State            string
	TotalBytes       int64
	FreeBytes        int64
	AllocatableBytes int64
	MaxVolumeBytes   int64
}

// Checker performs on-demand kouja health checks.
type Checker struct {
	API API
	Now func() time.Time
}

// Check probes the kouja endpoint, configured class, and optionally an
// existing disk to detect owner rotation.
func (c *Checker) Check(ctx context.Context, cfg Config, sample *store.Disk) (Health, error) {
	if c == nil || c.API == nil {
		return Health{}, fmt.Errorf("kouja client is not configured")
	}
	now := c.now()
	health := Health{CheckedAt: now}
	info, err := c.API.GetInfo(ctx, &koujav1.GetInfoRequest{})
	if err != nil {
		health.Detail = err.Error()
		return health, nil
	}
	health.Reachable = true
	health.Owner = cfg.Owner
	health.Protocols = protocolNames(info.GetProtocols())
	if !info.GetReady() {
		health.Detail = "kouja agent is not ready"
		return health, nil
	}
	if !containsProtocol(info.GetProtocols(), toKoujaProtocol(cfg.Protocol)) {
		health.Detail = fmt.Sprintf("kouja agent does not advertise %s", cfg.Protocol)
		return health, nil
	}
	classes, err := c.API.ListStorageClasses(ctx, &koujav1.ListStorageClassesRequest{})
	if err != nil {
		health.Detail = err.Error()
		return health, nil
	}
	var selected *koujav1.StorageClass
	for _, class := range classes.GetClasses() {
		if class.GetName() == cfg.StorageClass {
			selected = class
			break
		}
	}
	if selected == nil {
		health.Detail = fmt.Sprintf("kouja storage class %q does not exist", cfg.StorageClass)
		return health, nil
	}
	health.Classes = append(health.Classes, ClassCapacity{
		Name:             selected.GetName(),
		VGName:           selected.GetVgName(),
		State:            selected.GetVgState(),
		TotalBytes:       selected.GetTotalBytes(),
		FreeBytes:        selected.GetFreeBytes(),
		AllocatableBytes: selected.GetAllocatableBytes(),
		MaxVolumeBytes:   selected.GetMaxVolumeBytes(),
	})
	if sample != nil {
		volumeID := strings.TrimSpace(sample.BackendMetadata.Volume.VolumeID)
		if volumeID == "" {
			health.Detail = "sampled disk has no kouja volume ID"
			return health, nil
		}
		volume, err := c.API.GetVolume(ctx, &koujav1.GetVolumeRequest{VolumeId: volumeID})
		if err != nil {
			health.Detail = err.Error()
			return health, nil
		}
		if volume.GetDesired() == nil {
			health.Detail = "sampled disk volume is missing"
			return health, nil
		}
		if owner := strings.TrimSpace(sample.BackendMetadata.Volume.Owner); owner != "" &&
			volume.GetDesired() != nil && volume.GetDesired().GetOwner() != "" &&
			owner != volume.GetDesired().GetOwner() {
			health.Detail = fmt.Sprintf("kouja volume owner changed from %q to %q", owner, volume.GetDesired().GetOwner())
			return health, nil
		}
		credential, err := c.API.GetCredential(ctx, &koujav1.GetCredentialRequest{VolumeId: volumeID})
		if err != nil {
			health.Detail = err.Error()
			return health, nil
		}
		if credential.GetProtocol() != toKoujaProtocol(cfg.Protocol) || credential.GetCredential() == "" {
			health.Detail = "sampled disk credential is unavailable"
			return health, nil
		}
	}
	health.Ready = true
	return health, nil
}

func (c *Checker) now() time.Time {
	if c != nil && c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

func protocolNames(protocols []koujav1.Protocol) []string {
	names := make([]string, 0, len(protocols))
	for _, value := range protocols {
		switch value {
		case koujav1.Protocol_PROTOCOL_ISCSI:
			names = append(names, string(blockdev.ProtocolISCSI))
		case koujav1.Protocol_PROTOCOL_NVME_TCP:
			names = append(names, string(blockdev.ProtocolNVMeTCP))
		default:
			names = append(names, value.String())
		}
	}
	return names
}

func containsProtocol(protocols []koujav1.Protocol, want koujav1.Protocol) bool {
	for _, value := range protocols {
		if value == want {
			return true
		}
	}
	return false
}
