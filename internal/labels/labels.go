// Package labels defines the free-form labels attached to inventory entities.
package labels

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxLabels is the maximum number of labels stored on one entity.
	MaxLabels = 64
	// MaxLabelLength is the maximum label length in Unicode code points.
	MaxLabelLength = 64
)

const (
	EntityVM               = "vm"
	EntityDisk             = "disk"
	EntityMedia            = "media"
	EntityNetwork          = "network"
	EntityInterface        = "interface"
	EntityVNF              = "vnf"
	EntityACL              = "acl"
	EntityRouteTable       = "route_table"
	EntityRoute            = "route"
	EntityNetworkPipe      = "network_pipe"
	EntityStorageBackend   = "storage_backend"
	EntityHostTap          = "host_tap"
	EntityHostInterface    = "host_interface"
	EntityLightVMKernel    = "lightvm_kernel"
	EntityLightVMInitramfs = "lightvm_initramfs"
	EntityLightVM          = "lightvm"
)

var entityTypes = []string{
	EntityVM,
	EntityDisk,
	EntityMedia,
	EntityNetwork,
	EntityInterface,
	EntityVNF,
	EntityACL,
	EntityRouteTable,
	EntityRoute,
	EntityNetworkPipe,
	EntityStorageBackend,
	EntityHostTap,
	EntityHostInterface,
	EntityLightVMKernel,
	EntityLightVMInitramfs,
	EntityLightVM,
}

// EntityTypes returns every entity type that accepts labels.
func EntityTypes() []string {
	return append([]string(nil), entityTypes...)
}

// ValidEntityType reports whether entityType is labelable.
func ValidEntityType(entityType string) bool {
	entityType = strings.TrimSpace(entityType)
	for _, candidate := range entityTypes {
		if candidate == entityType {
			return true
		}
	}
	return false
}

// Normalize trims, validates, deduplicates, and sorts labels. Empty entries
// are ignored so comma-separated UI input can be normalized safely.
func Normalize(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if utf8.RuneCountInString(value) > MaxLabelLength {
			return nil, fmt.Errorf("label %q exceeds %d characters", value, MaxLabelLength)
		}
		for _, char := range value {
			if unicode.IsControl(char) || char == ',' {
				return nil, errors.New("labels cannot contain control characters or commas")
			}
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	if len(result) > MaxLabels {
		return nil, fmt.Errorf("an entity can have at most %d labels", MaxLabels)
	}
	sort.Strings(result)
	return result, nil
}
