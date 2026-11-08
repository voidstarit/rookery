package store

import (
	"encoding/json"
	"fmt"

	"github.com/voidstarit/rookery/internal/blockdev"
)

// DiskBackendMetadata is the persisted, versioned envelope for a disk's
// backend identity and secret-free attachment reference.
type DiskBackendMetadata struct {
	Version    int                `json:"version"`
	Driver     string             `json:"driver"`
	Volume     VolumeMetadata     `json:"volume"`
	Attachment blockdev.Reference `json:"attachment"`
}

// VolumeMetadata identifies a volume within its backend. Fields that do not
// apply to a driver stay empty.
type VolumeMetadata struct {
	VolumeID     string `json:"volume_id,omitempty"`
	RequestID    string `json:"request_id,omitempty"`
	ResourceID   string `json:"resource_id,omitempty"`
	Owner        string `json:"owner,omitempty"`
	StorageClass string `json:"storage_class,omitempty"`
}

// DiskBackendMetadataVersion is the current metadata envelope version.
const DiskBackendMetadataVersion = 1

// DecodeDiskBackendMetadata parses backend metadata, accepting an empty value
// as an empty envelope.
func DecodeDiskBackendMetadata(raw string) (DiskBackendMetadata, error) {
	if raw == "" || raw == "{}" || raw == "null" {
		return DiskBackendMetadata{}, nil
	}
	var metadata DiskBackendMetadata
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return DiskBackendMetadata{}, fmt.Errorf("parse disk backend metadata: %w", err)
	}
	if metadata.Attachment.Protocol != "" {
		if err := metadata.Attachment.Validate(); err != nil {
			return DiskBackendMetadata{}, fmt.Errorf("validate disk backend metadata: %w", err)
		}
	}
	return metadata, nil
}

// EncodeDiskBackendMetadata serializes metadata, defaulting the envelope
// version when a driver supplies content but no version.
func EncodeDiskBackendMetadata(metadata DiskBackendMetadata) (string, error) {
	if metadata.Driver == "" && metadata.Volume == (VolumeMetadata{}) && metadata.Attachment.Protocol == "" {
		return "{}", nil
	}
	if metadata.Version == 0 {
		metadata.Version = DiskBackendMetadataVersion
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("encode disk backend metadata: %w", err)
	}
	return string(encoded), nil
}
