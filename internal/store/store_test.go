package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/voidstarit/rookery/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()

	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return st
}

func createDisk(t *testing.T, st *store.Store, name string) store.Disk {
	t.Helper()

	disk, err := st.CreateDisk(context.Background(), store.Disk{
		Name:     name,
		SizeGB:   20,
		VG:       "vg0",
		ThinPool: "nerds-vms",
	})
	if err != nil {
		t.Fatalf("create disk %q: %v", name, err)
	}
	return disk
}

func createVM(t *testing.T, st *store.Store, name string) store.VM {
	t.Helper()

	disk := createDisk(t, st, name+"-disk")
	vm, err := st.CreateVM(context.Background(), store.VM{
		Name:        name,
		Description: "test vm",
		Status:      store.StatusStopped,
		VCPUs:       2,
		MemoryMB:    2048,
		DiskID:      &disk.ID,
	})
	if err != nil {
		t.Fatalf("create vm %q: %v", name, err)
	}
	return vm
}

func createMedia(t *testing.T, st *store.Store, name string) store.Media {
	t.Helper()

	media, err := st.CreateMedia(context.Background(), store.Media{
		Name:      name,
		Path:      "/srv/isos/" + name + ".iso",
		SizeBytes: 1024,
	})
	if err != nil {
		t.Fatalf("create media %q: %v", name, err)
	}
	return media
}

var testBridgeDomain uint32 = 100

func createNetwork(t *testing.T, st *store.Store, name string) store.Network {
	t.Helper()

	testBridgeDomain++
	network, err := st.CreateNetwork(context.Background(), store.Network{
		Name:           name,
		BridgeDomainID: testBridgeDomain,
	})
	if err != nil {
		t.Fatalf("create network %q: %v", name, err)
	}
	return network
}

func createInterface(t *testing.T, st *store.Store, name string, networkID int64) store.VirtualInterface {
	t.Helper()

	iface, err := st.CreateInterface(context.Background(), store.VirtualInterface{
		Name:       name,
		NetworkID:  networkID,
		SocketPath: "/run/vpp/" + name + ".sock",
		MAC:        "02:00:00:00:00:01",
		SwIfIndex:  200,
	})
	if err != nil {
		t.Fatalf("create interface %q: %v", name, err)
	}
	return iface
}

func TestVMCrud(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	created := createVM(t, st, "web-01")
	if _, err := uuid.Parse(created.ID); err != nil {
		t.Fatalf("created ID = %q, want UUID: %v", created.ID, err)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatal("created timestamps are zero")
	}
	if created.DiskID == nil || created.Disk == nil {
		t.Fatalf("created disk = %+v, id = %v", created.Disk, created.DiskID)
	}
	if created.Disk.Name != "web-01-disk" || created.Disk.SizeGB != 20 || created.Disk.Device != "/dev/vg0/web-01-disk" {
		t.Fatalf("created disk = %+v", created.Disk)
	}
	if created.ConsoleEnabled {
		t.Fatal("new VM has console access enabled by default")
	}

	got, err := st.GetVM(ctx, created.ID)
	if err != nil {
		t.Fatalf("get vm: %v", err)
	}
	if got.Name != "web-01" || got.VCPUs != 2 || got.MemoryMB != 2048 {
		t.Fatalf("get vm = %+v", got)
	}
	if got.Disk == nil || got.Disk.ID != created.Disk.ID {
		t.Fatalf("get vm disk = %+v", got.Disk)
	}

	updated, err := st.UpdateVM(ctx, store.VM{
		ID:             created.ID,
		Name:           "web-01",
		Description:    "running app",
		Status:         store.StatusRunning,
		VCPUs:          4,
		MemoryMB:       4096,
		ConsoleEnabled: true,
	})
	if err != nil {
		t.Fatalf("update vm: %v", err)
	}
	if updated.ID != created.ID || updated.Name != "web-01" || updated.Status != store.StatusRunning {
		t.Fatalf("updated vm = %+v", updated)
	}
	if updated.VCPUs != 4 || updated.MemoryMB != 4096 || !updated.ConsoleEnabled {
		t.Fatalf("updated resources = %+v", updated)
	}
	if updated.Disk == nil || updated.Disk.ID != created.Disk.ID {
		t.Fatalf("updated disk = %+v", updated.Disk)
	}
	if !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("created_at changed: got %v, want %v", updated.CreatedAt, created.CreatedAt)
	}

	disk, err := st.GetDisk(ctx, created.Disk.ID)
	if err != nil {
		t.Fatalf("get attached disk: %v", err)
	}
	if disk.AttachedVMID == nil || *disk.AttachedVMID != created.ID {
		t.Fatalf("attached vm = %v, want %s", disk.AttachedVMID, created.ID)
	}

	if err := st.DeleteVM(ctx, created.ID); err != nil {
		t.Fatalf("delete vm: %v", err)
	}
	if _, err := st.GetVM(ctx, created.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get deleted vm error = %v, want ErrNotFound", err)
	}

	disk, err = st.GetDisk(ctx, created.Disk.ID)
	if err != nil {
		t.Fatalf("get disk after vm delete: %v", err)
	}
	if disk.AttachedVMID != nil {
		t.Fatalf("disk still attached to %s", *disk.AttachedVMID)
	}
}

func TestVMMultipleDisksAndReassignment(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	first := createDisk(t, st, "multi-root")
	second := createDisk(t, st, "multi-data")
	third := createDisk(t, st, "multi-scratch")

	vm, err := st.CreateVM(ctx, store.VM{
		Name:     "multi",
		Status:   store.StatusStopped,
		VCPUs:    2,
		MemoryMB: 2048,
		DiskIDs:  []int64{first.ID, second.ID},
	})
	if err != nil {
		t.Fatalf("create multi-disk vm: %v", err)
	}
	if len(vm.Disks) != 2 || len(vm.DiskIDs) != 2 {
		t.Fatalf("created disks = %+v, ids = %v", vm.Disks, vm.DiskIDs)
	}
	if vm.Disks[0].ID != first.ID || vm.Disks[1].ID != second.ID {
		t.Fatalf("created disk order = %v, want [%d %d]", vm.DiskIDs, first.ID, second.ID)
	}
	if vm.DiskID == nil || *vm.DiskID != first.ID {
		t.Fatalf("primary disk = %v, want %d", vm.DiskID, first.ID)
	}
	for _, diskID := range []int64{first.ID, second.ID} {
		disk, err := st.GetDisk(ctx, diskID)
		if err != nil {
			t.Fatalf("get attached disk %d: %v", diskID, err)
		}
		if disk.AttachedVMID == nil || *disk.AttachedVMID != vm.ID {
			t.Fatalf("disk %d attached to %v, want %s", diskID, disk.AttachedVMID, vm.ID)
		}
		if err := st.DeleteDisk(ctx, diskID); !errors.Is(err, store.ErrDiskInUse) {
			t.Fatalf("delete attached disk %d error = %v, want ErrDiskInUse", diskID, err)
		}
	}

	updated, err := st.SetVMDisks(ctx, vm.ID, []int64{second.ID, third.ID})
	if err != nil {
		t.Fatalf("replace vm disks: %v", err)
	}
	if len(updated.Disks) != 2 || updated.Disks[0].ID != second.ID || updated.Disks[1].ID != third.ID {
		t.Fatalf("updated disks = %+v, want [%d %d]", updated.Disks, second.ID, third.ID)
	}
	if updated.DiskID == nil || *updated.DiskID != second.ID {
		t.Fatalf("updated primary disk = %v, want %d", updated.DiskID, second.ID)
	}
	detached, err := st.GetDisk(ctx, first.ID)
	if err != nil {
		t.Fatalf("get detached disk: %v", err)
	}
	if detached.AttachedVMID != nil {
		t.Fatalf("disk %d still attached to %s", first.ID, *detached.AttachedVMID)
	}

	other, err := st.CreateVM(ctx, store.VM{
		Name:     "other",
		Status:   store.StatusStopped,
		VCPUs:    1,
		MemoryMB: 512,
		DiskID:   &first.ID,
	})
	if err != nil {
		t.Fatalf("create other vm: %v", err)
	}
	if _, err := st.SetVMDisks(ctx, other.ID, []int64{second.ID}); !errors.Is(err, store.ErrDiskInUse) {
		t.Fatalf("attach in-use disk error = %v, want ErrDiskInUse", err)
	}

	if err := st.DeleteVM(ctx, vm.ID); err != nil {
		t.Fatalf("delete multi-disk vm: %v", err)
	}
	for _, diskID := range []int64{second.ID, third.ID} {
		disk, err := st.GetDisk(ctx, diskID)
		if err != nil {
			t.Fatalf("get released disk %d: %v", diskID, err)
		}
		if disk.AttachedVMID != nil {
			t.Fatalf("disk %d still attached to %s after VM delete", diskID, *disk.AttachedVMID)
		}
	}
}

func TestCreateRequiresAvailableDisk(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	_, err := st.CreateVM(ctx, store.VM{
		Name:     "web-01",
		Status:   store.StatusStopped,
		VCPUs:    1,
		MemoryMB: 512,
		DiskID:   ptr(int64(999)),
	})
	if !errors.Is(err, store.ErrDiskNotFound) {
		t.Fatalf("missing disk error = %v, want ErrDiskNotFound", err)
	}

	disk := createDisk(t, st, "shared-disk")
	if _, err := st.CreateVM(ctx, store.VM{
		Name:     "web-01",
		Status:   store.StatusStopped,
		VCPUs:    1,
		MemoryMB: 512,
		DiskID:   &disk.ID,
	}); err != nil {
		t.Fatalf("create first vm: %v", err)
	}

	_, err = st.CreateVM(ctx, store.VM{
		Name:     "web-02",
		Status:   store.StatusStopped,
		VCPUs:    1,
		MemoryMB: 512,
		DiskID:   &disk.ID,
	})
	if !errors.Is(err, store.ErrDiskInUse) {
		t.Fatalf("in-use disk error = %v, want ErrDiskInUse", err)
	}
}

func TestCreateDuplicateVMName(t *testing.T) {
	st := newTestStore(t)
	first := createVM(t, st, "web-01")
	secondDisk := createDisk(t, st, "web-02-disk")

	_, err := st.CreateVM(context.Background(), store.VM{
		Name:     first.Name,
		Status:   store.StatusStopped,
		VCPUs:    1,
		MemoryMB: 512,
		DiskID:   &secondDisk.ID,
	})
	if !errors.Is(err, store.ErrNameTaken) {
		t.Fatalf("create duplicate error = %v, want ErrNameTaken", err)
	}
}

func TestUpdateDuplicateName(t *testing.T) {
	st := newTestStore(t)

	first := createVM(t, st, "alpha")
	second := createVM(t, st, "beta")

	_, err := st.UpdateVM(context.Background(), store.VM{
		ID:       second.ID,
		Name:     first.Name,
		Status:   store.StatusStopped,
		VCPUs:    1,
		MemoryMB: 512,
	})
	if !errors.Is(err, store.ErrNameTaken) {
		t.Fatalf("update duplicate error = %v, want ErrNameTaken", err)
	}
}

func TestListVMsSortedCaseInsensitively(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	createVM(t, st, "zulu")
	createVM(t, st, "Alpha")
	createVM(t, st, "beta")

	vms, err := st.ListVMs(ctx)
	if err != nil {
		t.Fatalf("list vms: %v", err)
	}

	got := make([]string, len(vms))
	for i, vm := range vms {
		got[i] = vm.Name
	}
	want := []string{"Alpha", "beta", "zulu"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("names = %v, want %v", got, want)
		}
	}
}

func TestDiskCrudAndAttachment(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	disk := createDisk(t, st, "web-01-root")
	if disk.Device != "/dev/vg0/web-01-root" {
		t.Fatalf("device = %q", disk.Device)
	}

	disks, err := st.ListDisks(ctx)
	if err != nil {
		t.Fatalf("list disks: %v", err)
	}
	if len(disks) != 1 || disks[0].ID != disk.ID || disks[0].AttachedVMID != nil {
		t.Fatalf("disks = %+v", disks)
	}

	byName, err := st.GetDiskByName(ctx, "WEB-01-ROOT")
	if err != nil {
		t.Fatalf("get disk by name: %v", err)
	}
	if byName.ID != disk.ID {
		t.Fatalf("disk by name = %+v", byName)
	}

	vm, err := st.CreateVM(ctx, store.VM{
		Name:     "web-01",
		Status:   store.StatusStopped,
		VCPUs:    2,
		MemoryMB: 2048,
		DiskID:   &disk.ID,
	})
	if err != nil {
		t.Fatalf("create vm: %v", err)
	}

	attached, err := st.GetDisk(ctx, disk.ID)
	if err != nil {
		t.Fatalf("get attached disk: %v", err)
	}
	if attached.AttachedVMID == nil || *attached.AttachedVMID != vm.ID || attached.AttachedVMName != vm.Name {
		t.Fatalf("attached disk = %+v", attached)
	}

	if err := st.DeleteDisk(ctx, disk.ID); !errors.Is(err, store.ErrDiskInUse) {
		t.Fatalf("delete attached disk error = %v, want ErrDiskInUse", err)
	}

	if err := st.DeleteVM(ctx, vm.ID); err != nil {
		t.Fatalf("delete vm: %v", err)
	}
	if err := st.DeleteDisk(ctx, disk.ID); err != nil {
		t.Fatalf("delete disk: %v", err)
	}
	if _, err := st.GetDisk(ctx, disk.ID); !errors.Is(err, store.ErrDiskNotFound) {
		t.Fatalf("get deleted disk error = %v, want ErrDiskNotFound", err)
	}
}

func TestImportDisks(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	backend, err := st.CreateStorageBackend(ctx, store.StorageBackend{
		Name:   "import-pool",
		Driver: "lvm-thin",
		Config: store.StorageBackendConfig{VG: "vg0", ThinPool: "nerds-vms"},
	})
	if err != nil {
		t.Fatalf("create backend: %v", err)
	}

	imported, err := st.ImportDisks(ctx, backend.ID, "vg0", "nerds-vms", []store.ImportDisk{
		{Name: "old-root", SizeGB: 20},
		{Name: "old-data", SizeGB: 5},
	})
	if err != nil {
		t.Fatalf("import disks: %v", err)
	}
	if len(imported) != 2 {
		t.Fatalf("imported = %+v, want two", imported)
	}
	if imported[0].Name != "old-root" || imported[0].SizeGB != 20 || imported[0].StorageBackendName != "import-pool" {
		t.Fatalf("first imported disk = %+v", imported[0])
	}
	if imported[0].VG != "vg0" || imported[0].ThinPool != "nerds-vms" || imported[0].Device != "/dev/vg0/old-root" {
		t.Fatalf("first imported location = %+v", imported[0])
	}

	_, err = st.ImportDisks(ctx, backend.ID, "vg0", "nerds-vms", []store.ImportDisk{
		{Name: "new-disk", SizeGB: 10},
		{Name: "OLD-ROOT", SizeGB: 20},
	})
	if !errors.Is(err, store.ErrDiskNameTaken) {
		t.Fatalf("duplicate import error = %v, want ErrDiskNameTaken", err)
	}
	if _, err := st.GetDiskByName(ctx, "new-disk"); !errors.Is(err, store.ErrDiskNotFound) {
		t.Fatalf("partial import left new-disk: %v", err)
	}
}

func TestCreateDuplicateDiskName(t *testing.T) {
	st := newTestStore(t)
	createDisk(t, st, "web-01-root")

	_, err := st.CreateDisk(context.Background(), store.Disk{
		Name:     "WEB-01-ROOT",
		SizeGB:   40,
		VG:       "vg0",
		ThinPool: "nerds-vms",
	})
	if !errors.Is(err, store.ErrDiskNameTaken) {
		t.Fatalf("create duplicate disk error = %v, want ErrDiskNameTaken", err)
	}
}

func TestMediaCrud(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	created := createMedia(t, st, "debian-13")
	if created.ID < 1 || created.Name != "debian-13" || created.SizeBytes != 1024 {
		t.Fatalf("created media = %+v", created)
	}

	got, err := st.GetMedia(ctx, created.ID)
	if err != nil {
		t.Fatalf("get media: %v", err)
	}
	if got.Path != created.Path || got.SizeBytes != created.SizeBytes {
		t.Fatalf("get media = %+v, want %+v", got, created)
	}

	media, err := st.ListMedia(ctx)
	if err != nil {
		t.Fatalf("list media: %v", err)
	}
	if len(media) != 1 || media[0].ID != created.ID {
		t.Fatalf("list media = %+v", media)
	}

	if err := st.DeleteMedia(ctx, created.ID); err != nil {
		t.Fatalf("delete media: %v", err)
	}
	if _, err := st.GetMedia(ctx, created.ID); !errors.Is(err, store.ErrMediaNotFound) {
		t.Fatalf("get deleted media error = %v, want ErrMediaNotFound", err)
	}
	if err := st.DeleteMedia(ctx, created.ID); !errors.Is(err, store.ErrMediaNotFound) {
		t.Fatalf("delete missing media error = %v, want ErrMediaNotFound", err)
	}
}

func TestMediaNamesAreUnique(t *testing.T) {
	st := newTestStore(t)
	createMedia(t, st, "debian-13")

	_, err := st.CreateMedia(context.Background(), store.Media{
		Name:      "DEBIAN-13",
		Path:      "/srv/isos/other.iso",
		SizeBytes: 2048,
	})
	if !errors.Is(err, store.ErrMediaNameTaken) {
		t.Fatalf("create duplicate media error = %v, want ErrMediaNameTaken", err)
	}
}

func TestVMBootMedia(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	disk := createDisk(t, st, "boot-test-root")
	first := createMedia(t, st, "debian-13")
	second := createMedia(t, st, "ubuntu-24.04")

	vm, err := st.CreateVM(ctx, store.VM{
		Name:        "boot-test",
		Status:      store.StatusStopped,
		VCPUs:       1,
		MemoryMB:    512,
		DiskID:      &disk.ID,
		BootMediaID: &first.ID,
	})
	if err != nil {
		t.Fatalf("create vm with boot media: %v", err)
	}
	if vm.BootMediaID == nil || *vm.BootMediaID != first.ID {
		t.Fatalf("vm boot media id = %v, want %d", vm.BootMediaID, first.ID)
	}
	if vm.BootMedia == nil || vm.BootMedia.Name != first.Name || vm.BootMedia.Path != first.Path {
		t.Fatalf("vm boot media = %+v", vm.BootMedia)
	}

	if err := st.DeleteMedia(ctx, first.ID); !errors.Is(err, store.ErrMediaInUse) {
		t.Fatalf("delete boot media error = %v, want ErrMediaInUse", err)
	}

	updated, err := st.UpdateVM(ctx, store.VM{
		ID:          vm.ID,
		Name:        vm.Name,
		Status:      vm.Status,
		VCPUs:       vm.VCPUs,
		MemoryMB:    vm.MemoryMB,
		BootMediaID: &second.ID,
	})
	if err != nil {
		t.Fatalf("change boot media: %v", err)
	}
	if updated.BootMedia == nil || updated.BootMedia.ID != second.ID {
		t.Fatalf("updated boot media = %+v", updated.BootMedia)
	}

	if err := st.DeleteMedia(ctx, first.ID); err != nil {
		t.Fatalf("delete detached media: %v", err)
	}

	cleared, err := st.UpdateVM(ctx, store.VM{
		ID:       vm.ID,
		Name:     vm.Name,
		Status:   vm.Status,
		VCPUs:    vm.VCPUs,
		MemoryMB: vm.MemoryMB,
	})
	if err != nil {
		t.Fatalf("clear boot media: %v", err)
	}
	if cleared.BootMediaID != nil || cleared.BootMedia != nil {
		t.Fatalf("cleared boot media = %+v, id = %v", cleared.BootMedia, cleared.BootMediaID)
	}

	if err := st.DeleteMedia(ctx, second.ID); err != nil {
		t.Fatalf("delete cleared media: %v", err)
	}
}

func TestCreateVMRequiresExistingBootMedia(t *testing.T) {
	st := newTestStore(t)
	disk := createDisk(t, st, "missing-media-root")
	missing := int64(999)

	_, err := st.CreateVM(context.Background(), store.VM{
		Name:        "missing-media",
		Status:      store.StatusStopped,
		VCPUs:       1,
		MemoryMB:    512,
		DiskID:      &disk.ID,
		BootMediaID: &missing,
	})
	if !errors.Is(err, store.ErrMediaNotFound) {
		t.Fatalf("create vm error = %v, want ErrMediaNotFound", err)
	}
}

func TestNetworkInterfaceCrud(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	network := createNetwork(t, st, "lab")
	if network.ID < 1 || network.BridgeDomainID == 0 || network.InterfaceCount != 0 {
		t.Fatalf("created network = %+v", network)
	}

	gotNetwork, err := st.GetNetwork(ctx, network.ID)
	if err != nil {
		t.Fatalf("get network: %v", err)
	}
	if gotNetwork.Name != "lab" || gotNetwork.BridgeDomainID != network.BridgeDomainID {
		t.Fatalf("get network = %+v", gotNetwork)
	}

	iface := createInterface(t, st, "vm01-eth0", network.ID)
	if iface.SocketPath != "/run/vpp/vm01-eth0.sock" || iface.SwIfIndex != 200 {
		t.Fatalf("created interface = %+v", iface)
	}

	networks, err := st.ListNetworks(ctx)
	if err != nil {
		t.Fatalf("list networks: %v", err)
	}
	if len(networks) != 1 || networks[0].InterfaceCount != 1 {
		t.Fatalf("networks = %+v", networks)
	}

	interfaces, err := st.ListInterfaces(ctx)
	if err != nil {
		t.Fatalf("list interfaces: %v", err)
	}
	if len(interfaces) != 1 || interfaces[0].NetworkName != "lab" || interfaces[0].AttachedVMID != nil {
		t.Fatalf("interfaces = %+v", interfaces)
	}

	vm := createVM(t, st, "vm01")
	attached, err := st.SetInterfaceAttachment(ctx, iface.ID, &vm.ID)
	if err != nil {
		t.Fatalf("attach interface: %v", err)
	}
	if attached.AttachedVMID == nil || *attached.AttachedVMID != vm.ID || attached.AttachedVMName != vm.Name {
		t.Fatalf("attached interface = %+v", attached)
	}

	gotVM, err := st.GetVM(ctx, vm.ID)
	if err != nil {
		t.Fatalf("get vm: %v", err)
	}
	if len(gotVM.Interfaces) != 1 || gotVM.Interfaces[0].ID != iface.ID || gotVM.Interfaces[0].NetworkName != "lab" {
		t.Fatalf("vm interfaces = %+v", gotVM.Interfaces)
	}

	if err := st.DeleteNetwork(ctx, network.ID); !errors.Is(err, store.ErrNetworkInUse) {
		t.Fatalf("delete network in use error = %v, want ErrNetworkInUse", err)
	}
	if err := st.DeleteInterface(ctx, iface.ID); !errors.Is(err, store.ErrInterfaceInUse) {
		t.Fatalf("delete attached interface error = %v, want ErrInterfaceInUse", err)
	}

	detached, err := st.SetInterfaceAttachment(ctx, iface.ID, nil)
	if err != nil {
		t.Fatalf("detach interface: %v", err)
	}
	if detached.AttachedVMID != nil {
		t.Fatalf("detached interface = %+v", detached)
	}

	if err := st.DeleteInterface(ctx, iface.ID); err != nil {
		t.Fatalf("delete interface: %v", err)
	}
	if err := st.DeleteNetwork(ctx, network.ID); err != nil {
		t.Fatalf("delete network: %v", err)
	}
	if _, err := st.GetNetwork(ctx, network.ID); !errors.Is(err, store.ErrNetworkNotFound) {
		t.Fatalf("get deleted network error = %v", err)
	}
}

func TestNetworkDHCP(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	network := createNetwork(t, st, "lab")

	updated, err := st.SetNetworkDHCP(ctx, network.ID, store.Network{
		DHCPEnabled:      true,
		DHCPGateway:      "10.20.0.1/24",
		DHCPRangeStart:   "10.20.0.100",
		DHCPRangeEnd:     "10.20.0.200",
		DHCPDNS:          "1.1.1.1,8.8.8.8",
		DHCPTapSwIfIndex: 7,
		DHCPTapHostName:  "ndn1",
	})
	if err != nil {
		t.Fatalf("set network dhcp: %v", err)
	}
	if !updated.DHCPEnabled || updated.DHCPGateway != "10.20.0.1/24" ||
		updated.DHCPRangeStart != "10.20.0.100" || updated.DHCPRangeEnd != "10.20.0.200" ||
		updated.DHCPDNS != "1.1.1.1,8.8.8.8" || updated.DHCPTapSwIfIndex != 7 ||
		updated.DHCPTapHostName != "ndn1" {
		t.Fatalf("updated network = %+v", updated)
	}

	cleared, err := st.SetNetworkDHCP(ctx, network.ID, store.Network{})
	if err != nil {
		t.Fatalf("clear network dhcp: %v", err)
	}
	if cleared.DHCPEnabled || cleared.DHCPGateway != "" || cleared.DHCPTapSwIfIndex != 0 {
		t.Fatalf("cleared network = %+v", cleared)
	}
}

func TestVNFCrud(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	network := createNetwork(t, st, "lab")
	other := createNetwork(t, st, "other")

	created, err := st.CreateVNF(ctx, store.VNF{
		Name:      "lab-dhcpv4",
		NetworkID: network.ID,
		Type:      store.VNFTypeDHCPv4,
		Enabled:   true,
		Config: store.VNFConfig{
			Gateway:    "10.20.0.1/24",
			RangeStart: "10.20.0.100",
			RangeEnd:   "10.20.0.200",
			DNS:        "1.1.1.1",
			LeaseTime:  "12h",
		},
		TapSwIfIndex: 7,
		TapHostName:  "vnf1",
	})
	if err != nil {
		t.Fatalf("create vnf: %v", err)
	}
	if created.ID < 1 || created.NetworkName != "lab" || !created.Enabled ||
		created.Config.Gateway != "10.20.0.1/24" || created.TapSwIfIndex != 7 {
		t.Fatalf("created vnf = %+v", created)
	}

	got, err := st.GetVNF(ctx, created.ID)
	if err != nil || got.Name != created.Name || got.Config.RangeEnd != "10.20.0.200" {
		t.Fatalf("get vnf = %+v, err = %v", got, err)
	}

	vnfs, err := st.ListVNFsByNetwork(ctx, network.ID)
	if err != nil || len(vnfs) != 1 {
		t.Fatalf("list vnfs = %+v, err = %v", vnfs, err)
	}
	if vnfsOther, err := st.ListVNFsByNetwork(ctx, other.ID); err != nil || len(vnfsOther) != 0 {
		t.Fatalf("other vnfs = %+v, err = %v", vnfsOther, err)
	}

	updated, err := st.UpdateVNF(ctx, created.ID, store.VNF{
		Name:         "lab-dhcpv4-renamed",
		Enabled:      false,
		Config:       created.Config,
		TapSwIfIndex: 0,
		TapHostName:  "",
	})
	if err != nil {
		t.Fatalf("update vnf: %v", err)
	}
	if updated.Name != "lab-dhcpv4-renamed" || updated.Enabled || updated.TapSwIfIndex != 0 {
		t.Fatalf("updated vnf = %+v", updated)
	}

	if _, err := st.CreateVNF(ctx, store.VNF{
		Name:      updated.Name,
		NetworkID: other.ID,
		Type:      store.VNFTypeDHCPv6,
		Config:    created.Config,
	}); !errors.Is(err, store.ErrVNFNameTaken) {
		t.Fatalf("duplicate name error = %v, want ErrVNFNameTaken", err)
	}

	if err := st.DeleteVNF(ctx, created.ID); err != nil {
		t.Fatalf("delete vnf: %v", err)
	}
	if _, err := st.GetVNF(ctx, created.ID); !errors.Is(err, store.ErrVNFNotFound) {
		t.Fatalf("get deleted vnf error = %v", err)
	}
}

func TestVNFsCascadeWithNetwork(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	network := createNetwork(t, st, "lab")
	vnf, err := st.CreateVNF(ctx, store.VNF{
		Name:      "lab-dhcpv4",
		NetworkID: network.ID,
		Type:      store.VNFTypeDHCPv4,
		Config:    store.VNFConfig{Gateway: "10.20.0.1/24", RangeStart: "10.20.0.100", RangeEnd: "10.20.0.200"},
	})
	if err != nil {
		t.Fatalf("create vnf: %v", err)
	}
	if err := st.DeleteNetwork(ctx, network.ID); err != nil {
		t.Fatalf("delete network: %v", err)
	}
	if _, err := st.GetVNF(ctx, vnf.ID); !errors.Is(err, store.ErrVNFNotFound) {
		t.Fatalf("vnf survived network delete: %v", err)
	}
}

func TestNetworkInterfaceNamesAreUnique(t *testing.T) {
	st := newTestStore(t)
	network := createNetwork(t, st, "lab")

	testBridgeDomain++
	if _, err := st.CreateNetwork(context.Background(), store.Network{
		Name:           "LAB",
		BridgeDomainID: testBridgeDomain,
	}); !errors.Is(err, store.ErrNetworkNameTaken) {
		t.Fatalf("duplicate network error = %v, want ErrNetworkNameTaken", err)
	}

	createInterface(t, st, "eth0", network.ID)
	if _, err := st.CreateInterface(context.Background(), store.VirtualInterface{
		Name:       "ETH0",
		NetworkID:  network.ID,
		SocketPath: "/run/vpp/other.sock",
		MAC:        "02:00:00:00:00:02",
		SwIfIndex:  201,
	}); !errors.Is(err, store.ErrInterfaceNameTaken) {
		t.Fatalf("duplicate interface error = %v, want ErrInterfaceNameTaken", err)
	}
}

func TestDeleteVMDetachesInterfaces(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	network := createNetwork(t, st, "lab")
	vm := createVM(t, st, "vm01")
	iface := createInterface(t, st, "vm01-eth0", network.ID)
	if _, err := st.SetInterfaceAttachment(ctx, iface.ID, &vm.ID); err != nil {
		t.Fatalf("attach interface: %v", err)
	}

	if err := st.DeleteVM(ctx, vm.ID); err != nil {
		t.Fatalf("delete vm: %v", err)
	}
	got, err := st.GetInterface(ctx, iface.ID)
	if err != nil {
		t.Fatalf("get interface: %v", err)
	}
	if got.AttachedVMID != nil {
		t.Fatalf("interface still attached after VM delete: %+v", got)
	}
	if err := st.DeleteInterface(ctx, iface.ID); err != nil {
		t.Fatalf("delete interface: %v", err)
	}
}

func TestInterfaceRequiresKnownVM(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	network := createNetwork(t, st, "lab")
	missing := "00000000-0000-4000-8000-000000000000"

	_, err := st.SetInterfaceAttachment(ctx, 999, &missing)
	if !errors.Is(err, store.ErrInterfaceNotFound) {
		t.Fatalf("attach missing interface error = %v, want ErrInterfaceNotFound", err)
	}

	iface := createInterface(t, st, "eth0", network.ID)
	if _, err := st.SetInterfaceAttachment(ctx, iface.ID, &missing); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("attach missing vm error = %v, want ErrNotFound", err)
	}

	if _, err := st.CreateInterface(ctx, store.VirtualInterface{
		Name:         "eth1",
		NetworkID:    network.ID,
		SocketPath:   "/run/vpp/eth1.sock",
		MAC:          "02:00:00:00:00:03",
		SwIfIndex:    202,
		AttachedVMID: &missing,
	}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("create interface with missing vm error = %v, want ErrNotFound", err)
	}
}

func TestOpenCreatesParentDirectory(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "nested", "dir", "test.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	if _, err := st.ListVMs(context.Background()); err != nil {
		t.Fatalf("list vms: %v", err)
	}
	if _, err := st.ListDisks(context.Background()); err != nil {
		t.Fatalf("list disks: %v", err)
	}
	if _, err := st.ListMedia(context.Background()); err != nil {
		t.Fatalf("list media: %v", err)
	}
}

func TestReopenKeepsDataAndSkipsAppliedMigrations(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "state.db")

	first, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open first store: %v", err)
	}
	created := createVM(t, first, "persistent")
	if err := first.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	second, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer second.Close()

	got, err := second.GetVM(ctx, created.ID)
	if err != nil {
		t.Fatalf("get vm after reopen: %v", err)
	}
	if got.Name != "persistent" || got.Disk == nil || got.Disk.Name != "persistent-disk" {
		t.Fatalf("vm after reopen = %+v", got)
	}
}

func ptr[T any](v T) *T {
	return &v
}
