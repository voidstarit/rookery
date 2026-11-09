package gateway_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/voidstarit/rookery/internal/diskthrottle"
	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/readiness"
	"github.com/voidstarit/rookery/internal/store"
)

func TestVMCreateWithHostDeviceDoesNotNeedThinPool(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "host-vm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	disk, err := st.CreateDisk(context.Background(), store.Disk{
		Name: "host-data", SizeGB: 10, Device: "/dev/mapper/data", Source: store.DiskSourceHostDevice,
	})
	if err != nil {
		t.Fatal(err)
	}
	gw, err := gateway.New(st, fakeReadiness{status: readiness.Status{
		VPP: readiness.VPPStatus{ComponentStatus: readiness.ComponentStatus{Ready: true}},
	}}, gateway.WithVMController(&fakeVMController{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	response, err := http.Post(server.URL+"/api/vms", "application/json",
		strings.NewReader(`{"name":"host-vm","disk_ids":[`+strconv.FormatInt(disk.ID, 10)+`]}`))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create host-device VM status = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	var vm map[string]any
	decodeBody(t, response, &vm)
	if vm["status"] != store.StatusRunning {
		t.Fatalf("created VM = %+v", vm)
	}
}

func TestHostDeviceDiskRoutes(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "host-devices.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	devicePath := filepath.Join(t.TempDir(), "host-lv")
	provisioner := &fakeDiskProvisioner{}
	gw, err := gateway.New(st, fakeReadiness{status: readiness.Status{
		Ready: true,
		ThinPool: readiness.ThinPoolStatus{
			ComponentStatus: readiness.ComponentStatus{Ready: true},
			VG:              "vg0",
			Name:            "pool",
		},
	}}, gateway.WithDiskProvisioner(provisioner), gateway.WithHostDeviceValidator(
		func(path string) (host.AttachableBlockDevice, error) {
			if path != devicePath {
				return host.AttachableBlockDevice{}, errors.New("unexpected device path")
			}
			return host.AttachableBlockDevice{
				Name: "host-lv", Path: path, Kind: host.BlockDeviceLV,
				SizeBytes: (5 << 30) + 1, Available: true,
			}, nil
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	post := func(body string) *http.Response {
		t.Helper()
		response, err := http.Post(server.URL+"/api/disks", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	created := post(`{"name":"host-data","host_device":"` + devicePath + `"}`)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create host device disk status = %d, body = %s", created.StatusCode, readBody(t, created))
	}
	var disk map[string]any
	decodeBody(t, created, &disk)
	if disk["source"] != store.DiskSourceHostDevice || disk["device"] != devicePath {
		t.Fatalf("host device disk = %+v", disk)
	}
	if disk["size_gb"].(float64) != 6 || disk["size_bytes"].(float64) != float64((5<<30)+1) {
		t.Fatalf("host device size = %+v", disk)
	}
	id := jsonID(disk["id"])

	duplicate := post(`{"name":"host-data-2","host_device":"` + devicePath + `"}`)
	if duplicate.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate host device status = %d, body = %s", duplicate.StatusCode, readBody(t, duplicate))
	}
	_ = duplicate.Body.Close()

	withSize := post(`{"name":"host-data-3","host_device":"` + devicePath + `","size_gb":10}`)
	if withSize.StatusCode != http.StatusBadRequest {
		t.Fatalf("host device with size status = %d, body = %s", withSize.StatusCode, readBody(t, withSize))
	}
	_ = withSize.Body.Close()

	asTemplate := post(`{"name":"host-data-4","host_device":"` + devicePath + `","is_template":true}`)
	if asTemplate.StatusCode != http.StatusBadRequest {
		t.Fatalf("host device template status = %d, body = %s", asTemplate.StatusCode, readBody(t, asTemplate))
	}
	_ = asTemplate.Body.Close()

	class, err := st.CreateDiskThrottleClass(context.Background(), store.DiskThrottleClass{
		Name: "host-fast", Limits: diskthrottle.Limits{BPSRead: 1_000_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	assignRequest, err := http.NewRequest(http.MethodPut, server.URL+"/api/disks/"+id,
		strings.NewReader(fmt.Sprintf(`{"throttle_class_id":%d}`, class.ID)))
	if err != nil {
		t.Fatal(err)
	}
	assignRequest.Header.Set("Content-Type", "application/json")
	assigned, err := http.DefaultClient.Do(assignRequest)
	if err != nil {
		t.Fatal(err)
	}
	if assigned.StatusCode != http.StatusOK {
		t.Fatalf("assign host class status = %d, body = %s", assigned.StatusCode, readBody(t, assigned))
	}
	var assignedDisk map[string]any
	decodeBody(t, assigned, &assignedDisk)
	if assignedDisk["throttle_class_name"] != "host-fast" {
		t.Fatalf("assigned host disk = %+v", assignedDisk)
	}
	clearRequest, err := http.NewRequest(http.MethodPut, server.URL+"/api/disks/"+id,
		strings.NewReader(`{"throttle_class_id":0}`))
	if err != nil {
		t.Fatal(err)
	}
	clearRequest.Header.Set("Content-Type", "application/json")
	cleared, err := http.DefaultClient.Do(clearRequest)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.StatusCode != http.StatusOK {
		t.Fatalf("clear host class status = %d, body = %s", cleared.StatusCode, readBody(t, cleared))
	}
	_ = cleared.Body.Close()

	request, err := http.NewRequest(http.MethodDelete, server.URL+"/api/disks/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.StatusCode != http.StatusNoContent {
		t.Fatalf("delete host device disk status = %d, body = %s", deleted.StatusCode, readBody(t, deleted))
	}
	_ = deleted.Body.Close()
	if len(provisioner.deleted) != 0 || len(provisioner.created) != 0 {
		t.Fatalf("provisioner calls: created=%v deleted=%v", provisioner.created, provisioner.deleted)
	}
}

func TestListAttachableBlockDevices(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "attachable.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	dir := t.TempDir()
	paths := make([]string, 4)
	for i := range paths {
		paths[i] = filepath.Join(dir, fmt.Sprintf("device-%d", i))
		if err := os.WriteFile(paths[i], []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	unattached, err := st.CreateDisk(context.Background(), store.Disk{
		Name: "host-a", SizeGB: 1, Device: paths[0], Source: store.DiskSourceHostDevice,
	})
	if err != nil {
		t.Fatal(err)
	}
	attached, err := st.CreateDisk(context.Background(), store.Disk{
		Name: "host-b", SizeGB: 1, Device: paths[1], Source: store.DiskSourceHostDevice,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateVM(context.Background(), store.VM{
		Name: "attached-vm", Status: store.StatusStopped, VCPUs: 1, MemoryMB: 512,
		DiskIDs: []int64{attached.ID},
	}); err != nil {
		t.Fatal(err)
	}

	devices := []host.AttachableBlockDevice{
		{Name: "a", Path: paths[0], Kind: host.BlockDeviceLV, SizeBytes: 1, Available: true},
		{Name: "b", Path: paths[1], Kind: host.BlockDeviceDisk, SizeBytes: 1, Available: true},
		{Name: "c", Path: paths[2], Kind: host.BlockDevicePartition, SizeBytes: 1, Available: true},
		{Name: "d", Path: paths[3], Kind: host.BlockDeviceRAID, SizeBytes: 1, Available: false, Reason: "mounted at /mnt"},
	}
	gw, err := gateway.New(st, fakeReadiness{status: readyStatus()}, gateway.WithAttachableBlockDeviceLister(
		func() ([]host.AttachableBlockDevice, error) { return devices, nil },
	))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/api/host/block-devices/attachable")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list attachable status = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	var items []map[string]any
	decodeBody(t, response, &items)
	if len(items) != 4 {
		t.Fatalf("devices = %+v", items)
	}
	if items[0]["registered"] != true || items[0]["disk_name"] != unattached.Name {
		t.Fatalf("unattached registration = %+v", items[0])
	}
	if items[1]["registered"] != true || items[1]["attached_vm_name"] != "attached-vm" ||
		!strings.Contains(items[1]["unavailable_reason"].(string), "attached to attached-vm") {
		t.Fatalf("attached registration = %+v", items[1])
	}
	if items[2]["available"] != true || items[2]["registered"] == true {
		t.Fatalf("free device = %+v", items[2])
	}
	if available, ok := items[3]["available"].(bool); ok && available {
		t.Fatalf("unavailable device = %+v", items[3])
	}
	if items[3]["unavailable_reason"] != "mounted at /mnt" {
		t.Fatalf("unavailable device = %+v", items[3])
	}
}

func TestListAttachableBlockDevicesError(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "attachable-error.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	gw, err := gateway.New(st, fakeReadiness{status: readyStatus()}, gateway.WithAttachableBlockDeviceLister(
		func() ([]host.AttachableBlockDevice, error) { return nil, errors.New("device scan failed") },
	))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/api/host/block-devices/attachable")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("list attachable status = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	_ = response.Body.Close()
}

func TestHostDeviceDiskValidationErrors(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "host-validation.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	dir := t.TempDir()
	goodPath := filepath.Join(dir, "good")
	zeroPath := filepath.Join(dir, "zero")
	for _, path := range []string{goodPath, zeroPath} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.CreateDisk(context.Background(), store.Disk{
		Name: "existing", SizeGB: 1, Device: goodPath, Source: store.DiskSourceHostDevice,
	}); err != nil {
		t.Fatal(err)
	}

	gw, err := gateway.New(st, fakeReadiness{status: readyStatus()}, gateway.WithHostDeviceValidator(
		func(path string) (host.AttachableBlockDevice, error) {
			switch path {
			case goodPath:
				return host.AttachableBlockDevice{Name: "good", Path: path, SizeBytes: 1, Available: true}, nil
			case zeroPath:
				return host.AttachableBlockDevice{Name: "zero", Path: path, SizeBytes: 0, Available: true}, nil
			default:
				return host.AttachableBlockDevice{}, errors.New("device rejected")
			}
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	post := func(body string) *http.Response {
		t.Helper()
		response, err := http.Post(server.URL+"/api/disks", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	cases := []struct {
		name string
		body string
		want int
	}{
		{name: "storage backend", body: `{"name":"bad-1","host_device":"` + goodPath + `","storage_backend_id":1}`, want: http.StatusBadRequest},
		{name: "provisioning", body: `{"name":"bad-2","host_device":"` + goodPath + `","provisioning_script":"/bin/true"}`, want: http.StatusBadRequest},
		{name: "validator", body: `{"name":"bad-3","host_device":"` + filepath.Join(dir, "missing") + `"}`, want: http.StatusBadRequest},
		{name: "name conflict", body: `{"name":"existing","host_device":"` + zeroPath + `"}`, want: http.StatusConflict},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			response := post(test.body)
			defer response.Body.Close()
			if response.StatusCode != test.want {
				t.Fatalf("status = %d, body = %s", response.StatusCode, readBody(t, response))
			}
		})
	}

	zero := post(`{"name":"zero-size","host_device":"` + zeroPath + `"}`)
	if zero.StatusCode != http.StatusCreated {
		t.Fatalf("zero-size status = %d, body = %s", zero.StatusCode, readBody(t, zero))
	}
	var zeroDisk map[string]any
	decodeBody(t, zero, &zeroDisk)
	if zeroDisk["size_gb"].(float64) != 1 {
		t.Fatalf("zero-size disk = %+v", zeroDisk)
	}
}

func TestHostDeviceDiskUpdateAndAttachedDelete(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "host-update.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	path := filepath.Join(t.TempDir(), "attached")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	disk, err := st.CreateDisk(context.Background(), store.Disk{
		Name: "attached", SizeGB: 1, Device: path, Source: store.DiskSourceHostDevice,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateVM(context.Background(), store.VM{
		Name: "host-vm", Status: store.StatusStopped, VCPUs: 1, MemoryMB: 512,
		DiskIDs: []int64{disk.ID},
	}); err != nil {
		t.Fatal(err)
	}

	gw, err := gateway.New(st, fakeReadiness{status: readyStatus()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	request, err := http.NewRequest(http.MethodPut, server.URL+"/api/disks/"+strconv.FormatInt(disk.ID, 10),
		strings.NewReader(`{"is_template":true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("template update status = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	_ = response.Body.Close()

	request, err = http.NewRequest(http.MethodPut, server.URL+"/api/disks/"+strconv.FormatInt(disk.ID, 10),
		strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("no-op update status = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	_ = response.Body.Close()

	request, err = http.NewRequest(http.MethodDelete, server.URL+"/api/disks/"+strconv.FormatInt(disk.ID, 10), nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("attached delete status = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	_ = response.Body.Close()
}
