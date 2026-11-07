package kouja

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	koujav1 "github.com/voidstarit/kouja/gen/kouja/v1"

	"github.com/voidstarit/rookery/internal/blockdev"
	"github.com/voidstarit/rookery/internal/store"
)

var (
	// ErrExportChanged means the live export no longer matches the stored reference.
	ErrExportChanged = errors.New("kouja export changed")
	// ErrStaleObservation means a fresh observation shows a missing target or ACL.
	ErrStaleObservation = errors.New("kouja export observation is stale or incomplete")
	// ErrSessionActive means another initiator currently owns the export.
	ErrSessionActive = errors.New("kouja export has an active session")
	// ErrMissingCredential means kouja returned no credential.
	ErrMissingCredential = errors.New("kouja returned an empty credential")
)

// ObservationFreshness is the maximum age for an observation to be trusted.
const ObservationFreshness = 2 * time.Minute

// Preparer fetches the live export and credential for a VM start.
type Preparer struct {
	API API
	Now func() time.Time
}

// Prepare returns a runtime attachment for disk.
func (p *Preparer) Prepare(ctx context.Context, cfg Config, disk store.Disk) (blockdev.Attachment, error) {
	if p == nil || p.API == nil {
		return blockdev.Attachment{}, errors.New("kouja client is not configured")
	}
	protocol := blockdev.Protocol(disk.Protocol)
	if protocol == "" {
		protocol = cfg.Protocol
	}
	if err := validateProtocol(protocol); err != nil {
		return blockdev.Attachment{}, err
	}
	volumeID := strings.TrimSpace(disk.BackendMetadata.Volume.VolumeID)
	if volumeID == "" {
		return blockdev.Attachment{}, errors.New("disk has no kouja volume ID")
	}
	response, err := p.API.GetVolume(ctx, &koujav1.GetVolumeRequest{VolumeId: volumeID})
	if err != nil {
		return blockdev.Attachment{}, err
	}
	if err := p.validateLiveExport(cfg, protocol, disk, response); err != nil {
		return blockdev.Attachment{}, err
	}
	credential, err := p.API.GetCredential(ctx, &koujav1.GetCredentialRequest{VolumeId: volumeID})
	if err != nil {
		return blockdev.Attachment{}, err
	}
	if credential.GetProtocol() != toKoujaProtocol(protocol) {
		return blockdev.Attachment{}, fmt.Errorf("kouja credential protocol is %s", credential.GetProtocol())
	}
	if credential.GetCredential() == "" {
		return blockdev.Attachment{}, ErrMissingCredential
	}
	resourceID := strings.TrimSpace(disk.BackendMetadata.Volume.ResourceID)
	if resourceID == "" {
		return blockdev.Attachment{}, errors.New("disk has no kouja resource ID")
	}
	reference := disk.BackendMetadata.Attachment
	attachment := blockdev.Attachment{Protocol: protocol}
	switch protocol {
	case blockdev.ProtocolISCSI:
		if reference.ISCSI == nil {
			return blockdev.Attachment{}, errors.New("disk has no iSCSI reference")
		}
		value := *reference.ISCSI
		attachment.ISCSI = &blockdev.ISCSIAttachment{
			ISCSIReference: value,
			Username:       "kouja-" + resourceID,
			Credential:     credential.GetCredential(),
		}
	case blockdev.ProtocolNVMeTCP:
		if reference.NVMeTCP == nil {
			return blockdev.Attachment{}, errors.New("disk has no NVMe/TCP reference")
		}
		value := *reference.NVMeTCP
		attachment.NVMeTCP = &blockdev.NVMeTCPAttachment{
			NVMeTCPReference: value,
			PSK:              credential.GetCredential(),
		}
	}
	if err := attachment.ValidateComplete(); err != nil {
		return blockdev.Attachment{}, err
	}
	return attachment, nil
}

func (p *Preparer) validateLiveExport(cfg Config, protocol blockdev.Protocol, disk store.Disk, response *koujav1.GetVolumeResponse) error {
	desired := response.GetDesired()
	export := response.GetExport()
	if desired == nil || export == nil {
		return errors.New("kouja get volume returned no desired volume or export")
	}
	if desired.GetState() != koujav1.VolumeState_VOLUME_STATE_AVAILABLE &&
		desired.GetState() != koujav1.VolumeState_VOLUME_STATE_EXPORTED {
		return fmt.Errorf("kouja volume state is %s", desired.GetState())
	}
	if export.GetState() != koujav1.ExportState_EXPORT_STATE_EXPORTED {
		return fmt.Errorf("kouja export state is %s", export.GetState())
	}
	if export.GetProtocol() != toKoujaProtocol(protocol) {
		return fmt.Errorf("kouja export protocol is %s", export.GetProtocol())
	}
	observed := response.GetObserved()
	observeAt := time.Time{}
	if observed != nil && observed.GetObservedAt() != nil {
		observeAt = observed.GetObservedAt().AsTime()
	}
	now := p.now()
	if !observeAt.IsZero() && now.Sub(observeAt) <= ObservationFreshness {
		if !observed.GetTargetPresent() || !observed.GetAclPresent() {
			return ErrStaleObservation
		}
	}
	if export.GetSessionState() == koujav1.SessionState_SESSION_STATE_ACTIVE {
		return ErrSessionActive
	}
	activeSessions := int32(0)
	if observed != nil {
		activeSessions = observed.GetActiveSessions()
	}
	if (export.GetSessionState() == koujav1.SessionState_SESSION_STATE_UNKNOWN ||
		export.GetSessionState() == koujav1.SessionState_SESSION_STATE_UNSPECIFIED) && activeSessions > 0 {
		return ErrSessionActive
	}
	if owner := strings.TrimSpace(disk.BackendMetadata.Volume.Owner); owner != "" &&
		desired.GetOwner() != "" && owner != desired.GetOwner() {
		return fmt.Errorf("kouja volume owner changed from %q to %q", owner, desired.GetOwner())
	}
	reference := disk.BackendMetadata.Attachment
	switch protocol {
	case blockdev.ProtocolISCSI:
		if reference.ISCSI == nil {
			return ErrExportChanged
		}
		exportPortal := reference.ISCSI.ExportPortal
		if exportPortal == "" {
			exportPortal = reference.ISCSI.Portal
		}
		if export.GetPortal() != exportPortal ||
			export.GetTargetName() != reference.ISCSI.Target ||
			int(export.GetLun()) != reference.ISCSI.LUN ||
			(cfg.Initiator != "" && export.GetInitiator() != "" && export.GetInitiator() != cfg.Initiator) {
			return ErrExportChanged
		}
	case blockdev.ProtocolNVMeTCP:
		if reference.NVMeTCP == nil {
			return ErrExportChanged
		}
		exportAddress := reference.NVMeTCP.ExportAddress
		if exportAddress == "" {
			exportAddress = reference.NVMeTCP.Address
		}
		if export.GetPortal() != exportAddress ||
			export.GetTargetName() != reference.NVMeTCP.NQN ||
			int(export.GetNsid()) != reference.NVMeTCP.NSID ||
			(reference.NVMeTCP.HostNQN != "" && export.GetInitiator() != "" &&
				export.GetInitiator() != reference.NVMeTCP.HostNQN) {
			return ErrExportChanged
		}
	}
	return nil
}

func (p *Preparer) now() time.Time {
	if p != nil && p.Now != nil {
		return p.Now().UTC()
	}
	return time.Now().UTC()
}
