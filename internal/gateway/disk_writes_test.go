package gateway_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/readiness"
	"github.com/voidstarit/rookery/internal/store"
)

type fakeDiskProvisioner struct {
	created []string
	deleted []string
}

func (f *fakeDiskProvisioner) CreateThinVolume(_ context.Context, _, _, name string, _ int) error {
	f.created = append(f.created, name)
	return nil
}
func (f *fakeDiskProvisioner) DeleteThinVolume(_ context.Context, _, name string) error {
	f.deleted = append(f.deleted, name)
	return nil
}
func (f *fakeDiskProvisioner) ListThinVolumes(_ context.Context, _, _ string) ([]host.ThinVolume, error) {
	return nil, nil
}

func TestDiskWriteRoutes(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "disk-writes.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	provisioner := &fakeDiskProvisioner{}
	gw, err := gateway.New(st, fakeReadiness{status: readiness.Status{
		Ready: true,
		ThinPool: readiness.ThinPoolStatus{
			ComponentStatus: readiness.ComponentStatus{Ready: true},
			VG:              "vg0",
			Name:            "pool",
		},
	}}, gateway.WithDiskProvisioner(provisioner))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	created, err := http.Post(server.URL+"/api/disks", "application/json", strings.NewReader(`{"name":"data-1","size_gb":20}`))
	if err != nil {
		t.Fatal(err)
	}
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create disk status = %d, body = %s", created.StatusCode, readBody(t, created))
	}
	var disk map[string]any
	decodeBody(t, created, &disk)
	id := jsonID(disk["id"])
	if disk["name"] != "data-1" || disk["size_gb"].(float64) != 20 || disk["vg"] != "vg0" {
		t.Fatalf("disk = %+v", disk)
	}
	if len(provisioner.created) != 1 || provisioner.created[0] != "data-1" {
		t.Fatalf("created volumes = %+v", provisioner.created)
	}

	request, err := http.NewRequest(http.MethodDelete, server.URL+"/api/disks/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.StatusCode != http.StatusNoContent {
		t.Fatalf("delete disk status = %d, body = %s", deleted.StatusCode, readBody(t, deleted))
	}
	_ = deleted.Body.Close()
	if len(provisioner.deleted) != 1 || provisioner.deleted[0] != "data-1" {
		t.Fatalf("deleted volumes = %+v", provisioner.deleted)
	}
}
