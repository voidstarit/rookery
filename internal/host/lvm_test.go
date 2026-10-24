package host

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type fakeLVM struct {
	vgExists         bool
	poolExists       bool
	poolAttr         string
	failCreate       bool
	failVolumeCreate bool
	failVolumeRemove bool
	createCalls      int
	volumes          map[string]string
	volumeSizes      map[string]float64
	volumePools      map[string]string
	calls            []string
}

func (f *fakeLVM) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))

	switch name {
	case "vgs":
		if f.vgExists {
			return []byte("  vg0\n  other\n"), nil
		}
		return []byte("  other\n"), nil
	case "lvs":
		var output strings.Builder
		if argValue(args, "-o") == "lv_name,lv_attr,lv_size,pool_lv" {
			for volumeName, attribute := range f.volumes {
				size := f.volumeSizes[volumeName]
				if size == 0 {
					size = 20
				}
				pool := f.volumePools[volumeName]
				if pool == "" {
					pool = "rookery"
				}
				fmt.Fprintf(&output, "  %s|%s|%.2f|%s\n", volumeName, attribute, size, pool)
			}
			return []byte(output.String()), nil
		}
		if f.poolExists {
			attribute := f.poolAttr
			if attribute == "" {
				attribute = "twi-aotz--"
			}
			output.WriteString("  rookery|" + attribute + "\n")
		}
		for volumeName, attribute := range f.volumes {
			output.WriteString("  " + volumeName + "|" + attribute + "\n")
		}
		return []byte(output.String()), nil
	case "lvcreate":
		f.createCalls++
		if argValue(args, "--type") == "thin-pool" || !hasArg(args, "--virtualsize") {
			if f.failCreate {
				return nil, errors.New("lvcreate failed")
			}
			f.poolExists = true
			return nil, nil
		}
		if f.failVolumeCreate {
			return nil, errors.New("lvcreate failed")
		}
		if f.volumes == nil {
			f.volumes = make(map[string]string)
		}
		f.volumes[argValue(args, "--name")] = "Vwi-a-tz--"
		return nil, nil
	case "lvremove":
		if f.failVolumeRemove {
			return nil, errors.New("lvremove failed")
		}
		target := args[len(args)-1]
		_, lvName, ok := strings.Cut(target, "/")
		if !ok {
			return nil, errors.New("invalid lvremove target")
		}
		if _, exists := f.volumes[lvName]; !exists {
			return nil, errors.New("logical volume not found")
		}
		delete(f.volumes, lvName)
		return nil, nil
	default:
		return nil, errors.New("unexpected command " + name)
	}
}

func argValue(args []string, flag string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

func hasArg(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}

func (f *fakeLVM) called(substring string) bool {
	for _, call := range f.calls {
		if strings.Contains(call, substring) {
			return true
		}
	}
	return false
}

func TestEnsureThinPoolCreatesWhenMissing(t *testing.T) {
	runner := &fakeLVM{vgExists: true}
	lvm := &LVM{Runner: runner}

	created, err := lvm.EnsureThinPool(context.Background(), "vg0", "rookery", "100%FREE")
	if err != nil {
		t.Fatalf("EnsureThinPool: %v", err)
	}
	if !created {
		t.Fatal("created = false, want true")
	}
	if !runner.poolExists {
		t.Fatal("pool was not marked as created")
	}
	if !runner.called("lvcreate --yes --type thin-pool --name rookery --extents 100%FREE vg0") {
		t.Fatalf("unexpected calls: %v", runner.calls)
	}
}

func TestEnsureThinPoolUsesSizeForAbsoluteSize(t *testing.T) {
	runner := &fakeLVM{vgExists: true}
	lvm := &LVM{Runner: runner}

	if _, err := lvm.EnsureThinPool(context.Background(), "vg0", "rookery", "200G"); err != nil {
		t.Fatalf("EnsureThinPool: %v", err)
	}
	if !runner.called("--size 200G") {
		t.Fatalf("unexpected calls: %v", runner.calls)
	}
}

func TestEnsureThinPoolIsIdempotent(t *testing.T) {
	runner := &fakeLVM{vgExists: true, poolExists: true}
	lvm := &LVM{Runner: runner}

	created, err := lvm.EnsureThinPool(context.Background(), "vg0", "rookery", "100%FREE")
	if err != nil {
		t.Fatalf("EnsureThinPool: %v", err)
	}
	if created {
		t.Fatal("created = true, want false")
	}
	if runner.createCalls != 0 {
		t.Fatalf("lvcreate was called %d times", runner.createCalls)
	}
}

func TestEnsureThinPoolRejectsExistingNonThinLV(t *testing.T) {
	runner := &fakeLVM{vgExists: true, poolExists: true, poolAttr: "-wi-a-----"}
	lvm := &LVM{Runner: runner}

	_, err := lvm.EnsureThinPool(context.Background(), "vg0", "rookery", "100%FREE")
	if err == nil || !strings.Contains(err.Error(), "not a thin pool") {
		t.Fatalf("error = %v, want non-thin-pool error", err)
	}
	if runner.createCalls != 0 {
		t.Fatalf("lvcreate was called %d times", runner.createCalls)
	}
}

func TestEnsureThinPoolRequiresVolumeGroup(t *testing.T) {
	runner := &fakeLVM{}
	lvm := &LVM{Runner: runner}

	_, err := lvm.EnsureThinPool(context.Background(), "vg0", "rookery", "100%FREE")
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("error = %v, want missing volume group error", err)
	}
	if runner.createCalls != 0 {
		t.Fatalf("lvcreate was called %d times", runner.createCalls)
	}
}

func TestEnsureThinPoolRecoversFromConcurrentCreate(t *testing.T) {
	runner := &fakeLVM{vgExists: true}
	lvm := &LVM{Runner: &raceRunner{fakeLVM: runner}}

	created, err := lvm.EnsureThinPool(context.Background(), "vg0", "rookery", "100%FREE")
	if err != nil {
		t.Fatalf("EnsureThinPool: %v", err)
	}
	if created {
		t.Fatal("created = true, want false")
	}
}

// raceRunner makes lvcreate fail after marking the pool as existing.
type raceRunner struct {
	*fakeLVM
}

func (r *raceRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name == "lvcreate" {
		r.fakeLVM.poolExists = true
		r.fakeLVM.createCalls++
		return nil, errors.New("already exists")
	}
	return r.fakeLVM.Run(ctx, name, args...)
}

func TestValidateLVMName(t *testing.T) {
	valid := []string{"vg0", "rookery", "pool_1", "A.b+c"}
	for _, name := range valid {
		if err := ValidateLVMName("name", name); err != nil {
			t.Errorf("ValidateLVMName(%q) = %v", name, err)
		}
	}

	invalid := []string{"", "-pool", "pool name", "../pool", strings.Repeat("a", 128)}
	for _, name := range invalid {
		if err := ValidateLVMName("name", name); err == nil {
			t.Errorf("ValidateLVMName(%q) = nil, want error", name)
		}
	}
}

func TestListThinVolumes(t *testing.T) {
	runner := &fakeLVM{
		vgExists:   true,
		poolExists: true,
		volumes: map[string]string{
			"zdata":     "Vwi-a-tz--",
			"root":      "Vwi-a-tz--",
			"plain":     "-wi-a-----",
			"otherpool": "Vwi-a-tz--",
		},
		volumeSizes: map[string]float64{
			"zdata":     5.2,
			"root":      10,
			"plain":     10,
			"otherpool": 1,
		},
		volumePools: map[string]string{"otherpool": "other-pool"},
	}
	lvm := &LVM{Runner: runner}

	volumes, err := lvm.ListThinVolumes(context.Background(), "vg0", "rookery")
	if err != nil {
		t.Fatalf("ListThinVolumes: %v", err)
	}
	if len(volumes) != 2 {
		t.Fatalf("volumes = %+v, want two", volumes)
	}
	if volumes[0].Name != "root" || volumes[0].SizeGB != 10 {
		t.Fatalf("first volume = %+v", volumes[0])
	}
	if volumes[1].Name != "zdata" || volumes[1].SizeGB != 6 {
		t.Fatalf("second volume = %+v", volumes[1])
	}
	if !runner.called("lvs --noheadings --separator | -o lv_name,lv_attr,lv_size,pool_lv --units g --nosuffix vg0") {
		t.Fatalf("unexpected calls: %v", runner.calls)
	}
}

func TestListThinVolumesRequiresThinPool(t *testing.T) {
	runner := &fakeLVM{vgExists: true}
	lvm := &LVM{Runner: runner}

	_, err := lvm.ListThinVolumes(context.Background(), "vg0", "rookery")
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("error = %v, want missing thin pool", err)
	}
}

func TestCreateThinVolume(t *testing.T) {
	runner := &fakeLVM{vgExists: true, poolExists: true}
	lvm := &LVM{Runner: runner}

	if err := lvm.CreateThinVolume(context.Background(), "vg0", "rookery", "web-01-root", 20); err != nil {
		t.Fatalf("CreateThinVolume: %v", err)
	}
	if !runner.called("lvcreate --yes --virtualsize 20G --name web-01-root --thinpool vg0/rookery") {
		t.Fatalf("unexpected calls: %v", runner.calls)
	}
	if attr := runner.volumes["web-01-root"]; !strings.HasPrefix(attr, "V") {
		t.Fatalf("volume attr = %q", attr)
	}
}

func TestCreateThinVolumeRequiresThinPool(t *testing.T) {
	runner := &fakeLVM{vgExists: true}
	lvm := &LVM{Runner: runner}

	err := lvm.CreateThinVolume(context.Background(), "vg0", "rookery", "web-01-root", 20)
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("error = %v, want missing thin pool", err)
	}
	if runner.called("lvcreate") {
		t.Fatalf("lvcreate was called: %v", runner.calls)
	}
}

func TestCreateThinVolumeRejectsExistingLV(t *testing.T) {
	runner := &fakeLVM{
		vgExists:   true,
		poolExists: true,
		volumes:    map[string]string{"web-01-root": "Vwi-a-tz--"},
	}
	lvm := &LVM{Runner: runner}

	err := lvm.CreateThinVolume(context.Background(), "vg0", "rookery", "web-01-root", 20)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %v, want existing LV error", err)
	}
}

func TestDeleteThinVolume(t *testing.T) {
	runner := &fakeLVM{
		vgExists:   true,
		poolExists: true,
		volumes:    map[string]string{"web-01-root": "Vwi-a-tz--"},
	}
	lvm := &LVM{Runner: runner}

	if err := lvm.DeleteThinVolume(context.Background(), "vg0", "web-01-root"); err != nil {
		t.Fatalf("DeleteThinVolume: %v", err)
	}
	if !runner.called("lvremove --yes vg0/web-01-root") {
		t.Fatalf("unexpected calls: %v", runner.calls)
	}
	if _, exists := runner.volumes["web-01-root"]; exists {
		t.Fatal("volume still exists")
	}
}

func TestDeleteThinVolumeMissingIsIdempotent(t *testing.T) {
	runner := &fakeLVM{vgExists: true, poolExists: true}
	lvm := &LVM{Runner: runner}

	if err := lvm.DeleteThinVolume(context.Background(), "vg0", "missing"); err != nil {
		t.Fatalf("DeleteThinVolume: %v", err)
	}
	if runner.called("lvremove") {
		t.Fatalf("lvremove was called: %v", runner.calls)
	}
}

func TestDeleteThinVolumeRefusesNonThinLV(t *testing.T) {
	runner := &fakeLVM{
		vgExists:   true,
		poolExists: true,
		volumes:    map[string]string{"web-01-root": "-wi-a-----"},
	}
	lvm := &LVM{Runner: runner}

	err := lvm.DeleteThinVolume(context.Background(), "vg0", "web-01-root")
	if err == nil || !strings.Contains(err.Error(), "not a thin volume") {
		t.Fatalf("error = %v, want non-thin-volume error", err)
	}
	if runner.called("lvremove") {
		t.Fatalf("lvremove was called: %v", runner.calls)
	}
}
