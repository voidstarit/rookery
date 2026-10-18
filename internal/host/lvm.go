package host

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// DefaultThinPoolSize is used by init when no size is given.
const DefaultThinPoolSize = "100%FREE"

var (
	lvmNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+_.-]{0,126}$`)
	lvmSizePattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?(?:%[A-Za-z]+|[A-Za-z]*)$`)
)

// LVM wraps the local LVM command-line tools.
type LVM struct {
	Runner Runner
}

// NewLVM returns an LVM implementation backed by the local command-line tools.
func NewLVM() *LVM {
	return &LVM{Runner: ExecRunner{}}
}

// ValidateLVMName validates a volume group or logical volume name.
func ValidateLVMName(kind, name string) error {
	if name == "" {
		return fmt.Errorf("%s name is required", kind)
	}
	if !lvmNamePattern.MatchString(name) {
		return fmt.Errorf("%s name %q must start with a letter or digit and contain only letters, digits, '+', '_', '.', and '-'", kind, name)
	}
	return nil
}

// ThinPoolInfo describes whether an LVM logical volume exists and is a thin pool.
type ThinPoolInfo struct {
	Exists     bool
	IsThinPool bool
}

// VolumeGroupExists reports whether the volume group exists.
func (l *LVM) VolumeGroupExists(ctx context.Context, vg string) (bool, error) {
	if err := ValidateLVMName("volume group", vg); err != nil {
		return false, err
	}

	output, err := l.Runner.Run(ctx, "vgs", "--noheadings", "-o", "vg_name")
	if err != nil {
		return false, fmt.Errorf("list volume groups: %w", err)
	}

	for _, line := range strings.Split(string(output), "\n") {
		if strings.TrimSpace(line) == vg {
			return true, nil
		}
	}
	return false, nil
}

// ThinPool inspects a logical volume in a volume group. It returns
// ThinPoolInfo{Exists: false} when the LV does not exist.
func (l *LVM) ThinPool(ctx context.Context, vg, name string) (ThinPoolInfo, error) {
	attribute, exists, err := l.lvAttribute(ctx, vg, name)
	if err != nil {
		return ThinPoolInfo{}, err
	}
	return ThinPoolInfo{
		Exists:     exists,
		IsThinPool: exists && strings.HasPrefix(attribute, "t"),
	}, nil
}

// CreateThinVolume creates a thin logical volume in an existing thin pool.
func (l *LVM) CreateThinVolume(ctx context.Context, vg, thinPool, name string, sizeGB int) error {
	if sizeGB < 1 {
		return fmt.Errorf("disk size must be at least 1 GB")
	}

	poolInfo, err := l.ThinPool(ctx, vg, thinPool)
	if err != nil {
		return err
	}
	if !poolInfo.Exists {
		return fmt.Errorf("thin pool %s/%s does not exist", vg, thinPool)
	}
	if !poolInfo.IsThinPool {
		return fmt.Errorf("logical volume %s/%s is not a thin pool", vg, thinPool)
	}

	_, exists, err := l.lvAttribute(ctx, vg, name)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("logical volume %s/%s already exists", vg, name)
	}

	size := fmt.Sprintf("%dG", sizeGB)
	if _, err := l.Runner.Run(ctx, "lvcreate",
		"--yes",
		"--virtualsize", size,
		"--name", name,
		"--thinpool", vg+"/"+thinPool,
	); err != nil {
		return fmt.Errorf("create thin volume %s/%s: %w", vg, name, err)
	}

	attribute, exists, err := l.lvAttribute(ctx, vg, name)
	if err != nil {
		return fmt.Errorf("verify thin volume %s/%s: %w", vg, name, err)
	}
	if !exists || !strings.HasPrefix(attribute, "V") {
		return fmt.Errorf("thin volume %s/%s was not created correctly", vg, name)
	}
	return nil
}

// DeleteThinVolume removes a thin logical volume. A missing LV is treated as
// already removed; non-thin LVs are never removed.
func (l *LVM) DeleteThinVolume(ctx context.Context, vg, name string) error {
	attribute, exists, err := l.lvAttribute(ctx, vg, name)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if !strings.HasPrefix(attribute, "V") {
		return fmt.Errorf("logical volume %s/%s is not a thin volume; refusing to remove it", vg, name)
	}

	if _, err := l.Runner.Run(ctx, "lvremove", "--yes", vg+"/"+name); err != nil {
		return fmt.Errorf("remove thin volume %s/%s: %w", vg, name, err)
	}

	_, exists, err = l.lvAttribute(ctx, vg, name)
	if err != nil {
		return fmt.Errorf("verify removal of thin volume %s/%s: %w", vg, name, err)
	}
	if exists {
		return fmt.Errorf("thin volume %s/%s still exists after removal", vg, name)
	}
	return nil
}

func (l *LVM) lvAttribute(ctx context.Context, vg, name string) (attribute string, exists bool, err error) {
	if err := ValidateLVMName("volume group", vg); err != nil {
		return "", false, err
	}
	if err := ValidateLVMName("logical volume", name); err != nil {
		return "", false, err
	}

	output, err := l.Runner.Run(ctx, "lvs", "--noheadings", "--separator", "|", "-o", "lv_name,lv_attr", vg)
	if err != nil {
		return "", false, fmt.Errorf("inspect volume group %q: %w", vg, err)
	}

	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.TrimSpace(parts[0]) != name {
			continue
		}
		return strings.TrimSpace(parts[1]), true, nil
	}

	return "", false, nil
}

// EnsureThinPool creates the thin pool if it does not exist. It is safe to
// call repeatedly: an existing thin pool is left untouched.
func (l *LVM) EnsureThinPool(ctx context.Context, vg, name, size string) (created bool, err error) {
	if err := ValidateLVMName("volume group", vg); err != nil {
		return false, err
	}
	if err := ValidateLVMName("thin pool", name); err != nil {
		return false, err
	}
	if size == "" {
		size = DefaultThinPoolSize
	}
	if !lvmSizePattern.MatchString(size) {
		return false, fmt.Errorf("invalid thin pool size %q", size)
	}

	exists, err := l.VolumeGroupExists(ctx, vg)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, fmt.Errorf("volume group %q does not exist", vg)
	}

	info, err := l.ThinPool(ctx, vg, name)
	if err != nil {
		return false, err
	}
	if info.Exists {
		if !info.IsThinPool {
			return false, fmt.Errorf("logical volume %s/%s exists but is not a thin pool", vg, name)
		}
		return false, nil
	}

	sizeFlag := "--size"
	if strings.Contains(size, "%") {
		sizeFlag = "--extents"
	}
	args := []string{
		"--yes",
		"--type", "thin-pool",
		"--name", name,
		sizeFlag, size,
		vg,
	}

	if _, err := l.Runner.Run(ctx, "lvcreate", args...); err != nil {
		// Another init may have created the pool between our check and
		// lvcreate. Treat that as success if the requested pool now exists.
		info, verifyErr := l.ThinPool(ctx, vg, name)
		if verifyErr == nil && info.Exists && info.IsThinPool {
			return false, nil
		}
		return false, fmt.Errorf("create thin pool %s/%s: %w", vg, name, err)
	}

	info, err = l.ThinPool(ctx, vg, name)
	if err != nil {
		return true, fmt.Errorf("verify thin pool %s/%s: %w", vg, name, err)
	}
	if !info.Exists || !info.IsThinPool {
		return true, fmt.Errorf("thin pool %s/%s was not created correctly", vg, name)
	}

	return true, nil
}
