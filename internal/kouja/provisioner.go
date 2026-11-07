package kouja

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	koujacli "github.com/voidstarit/kouja/gen/client"
	koujav1 "github.com/voidstarit/kouja/gen/kouja/v1"
	"google.golang.org/protobuf/proto"

	"github.com/voidstarit/rookery/internal/blockdev"
)

// CreateRequest identifies a volume creation request.
type CreateRequest struct {
	VolumeID  string
	RequestID string
	SizeBytes int64
}

// CreatedVolume is the secret-free result of a kouja volume create.
type CreatedVolume struct {
	Reference       blockdev.Reference
	Owner           string
	ResourceID      string
	ActualSizeBytes uint64
}

// Provisioner creates and exports kouja volumes.
type Provisioner struct {
	API API
}

// Create creates an exported volume and returns its persisted reference.
func (p *Provisioner) Create(ctx context.Context, cfg Config, request CreateRequest) (CreatedVolume, error) {
	if p == nil || p.API == nil {
		return CreatedVolume{}, errors.New("kouja client is not configured")
	}
	if err := validateProtocol(cfg.Protocol); err != nil {
		return CreatedVolume{}, err
	}
	request.VolumeID = strings.TrimSpace(request.VolumeID)
	if request.VolumeID == "" {
		return CreatedVolume{}, errors.New("kouja volume ID is required")
	}
	if request.SizeBytes < 1 {
		return CreatedVolume{}, errors.New("kouja volume size must be positive")
	}
	if strings.TrimSpace(request.RequestID) == "" {
		request.RequestID = uuid.NewString()
	}
	ctx = koujacli.WithRequestID(ctx, request.RequestID)
	initiator := cfg.Initiator
	if cfg.Protocol == blockdev.ProtocolNVMeTCP {
		initiator = cfg.HostNQN
	}
	response, err := p.API.CreateVolume(ctx, &koujav1.CreateVolumeRequest{
		VolumeId:          request.VolumeID,
		SizeBytes:         request.SizeBytes,
		Export:            proto.Bool(true),
		Protocol:          toKoujaProtocol(cfg.Protocol),
		Initiator:         initiator,
		Wipe:              false,
		RequestId:         request.RequestID,
		IncludeCredential: proto.Bool(false),
		StorageClass:      cfg.StorageClass,
	})
	if err != nil {
		return CreatedVolume{}, err
	}
	volume := response.GetVolume()
	export := response.GetExport()
	if volume == nil || export == nil {
		return CreatedVolume{}, errors.New("kouja create returned no volume or export")
	}
	if volume.GetVolumeId() != request.VolumeID {
		return CreatedVolume{}, fmt.Errorf("kouja created volume %q, want %q", volume.GetVolumeId(), request.VolumeID)
	}
	if strings.TrimSpace(volume.GetResourceUuid()) == "" {
		return CreatedVolume{}, errors.New("kouja volume has no resource UUID")
	}
	if volume.GetState() != koujav1.VolumeState_VOLUME_STATE_AVAILABLE &&
		volume.GetState() != koujav1.VolumeState_VOLUME_STATE_EXPORTED {
		return CreatedVolume{}, fmt.Errorf("kouja volume state is %s", volume.GetState())
	}
	if export.GetState() != koujav1.ExportState_EXPORT_STATE_EXPORTED {
		return CreatedVolume{}, fmt.Errorf("kouja export state is %s", export.GetState())
	}
	if export.GetProtocol() != toKoujaProtocol(cfg.Protocol) {
		return CreatedVolume{}, fmt.Errorf("kouja export protocol is %s", export.GetProtocol())
	}
	if strings.TrimSpace(export.GetTargetName()) == "" || strings.TrimSpace(export.GetPortal()) == "" {
		return CreatedVolume{}, errors.New("kouja export has no target or portal")
	}
	if export.GetLun() < 0 {
		return CreatedVolume{}, errors.New("kouja export has a negative LUN")
	}
	if cfg.Protocol == blockdev.ProtocolNVMeTCP && export.GetNsid() < 0 {
		return CreatedVolume{}, errors.New("kouja export has a negative NSID")
	}
	if strings.TrimSpace(volume.GetOwner()) == "" {
		return CreatedVolume{}, errors.New("kouja volume has no owner")
	}
	effectivePortal := cfg.Portal
	if effectivePortal == "" {
		effectivePortal = export.GetPortal()
		if err := validatePortal(effectivePortal); err != nil {
			return CreatedVolume{}, err
		}
	}
	reference, err := referenceFromExport(cfg, volume, export, effectivePortal)
	if err != nil {
		return CreatedVolume{}, err
	}
	if err := reference.ValidateComplete(); err != nil {
		return CreatedVolume{}, err
	}
	return CreatedVolume{
		Reference:       reference,
		Owner:           volume.GetOwner(),
		ResourceID:      volume.GetResourceUuid(),
		ActualSizeBytes: uint64(maxInt64(volume.GetActualSizeBytes(), 0)),
	}, nil
}

func referenceFromExport(cfg Config, volume *koujav1.Volume, export *koujav1.Export, effectivePortal string) (blockdev.Reference, error) {
	switch cfg.Protocol {
	case blockdev.ProtocolISCSI:
		return blockdev.Reference{
			Protocol: blockdev.ProtocolISCSI,
			ISCSI: &blockdev.ISCSIReference{
				Portal:       effectivePortal,
				ExportPortal: export.GetPortal(),
				Target:       export.GetTargetName(),
				LUN:          int(export.GetLun()),
				Initiator:    cfg.Initiator,
			},
		}, nil
	case blockdev.ProtocolNVMeTCP:
		return blockdev.Reference{
			Protocol: blockdev.ProtocolNVMeTCP,
			NVMeTCP: &blockdev.NVMeTCPReference{
				Address:       effectivePortal,
				ExportAddress: export.GetPortal(),
				NQN:           export.GetTargetName(),
				HostNQN:       cfg.HostNQN,
				NSID:          int(export.GetNsid()),
			},
		}, nil
	default:
		return blockdev.Reference{}, fmt.Errorf("%w: %s", ErrUnsupportedProtocol, cfg.Protocol)
	}
}

func validateProtocol(protocol blockdev.Protocol) error {
	switch protocol {
	case blockdev.ProtocolISCSI, blockdev.ProtocolNVMeTCP:
		return nil
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedProtocol, protocol)
	}
}

func toKoujaProtocol(protocol blockdev.Protocol) koujav1.Protocol {
	switch protocol {
	case blockdev.ProtocolNVMeTCP:
		return koujav1.Protocol_PROTOCOL_NVME_TCP
	default:
		return koujav1.Protocol_PROTOCOL_ISCSI
	}
}

func maxInt64(value, floor int64) int64 {
	if value < floor {
		return floor
	}
	return value
}
