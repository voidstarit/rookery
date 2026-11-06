package gateway_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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
