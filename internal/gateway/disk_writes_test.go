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

func TestDiskThrottleClassDiskRoutes(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "disk-throttle.db"))
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

	classResponse, err := http.Post(server.URL+"/api/disk-throttle-classes", "application/json", strings.NewReader(
		`{"name":"limited","description":"test","limits":{"bps_read":10485760,"bps_write":5242880}}`))
	if err != nil {
		t.Fatal(err)
	}
	if classResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create class status = %d, body = %s", classResponse.StatusCode, readBody(t, classResponse))
	}
	if location := classResponse.Header.Get("Location"); location != "/api/disk-throttle-classes/1" {
		t.Fatalf("class Location = %q", location)
	}
	var class map[string]any
	decodeBody(t, classResponse, &class)
	classID := jsonID(class["id"])
	if class["name"] != "limited" || classID == "" {
		t.Fatalf("class = %+v", class)
	}

	response, err := http.Post(server.URL+"/api/disks", "application/json", strings.NewReader(
		`{"name":"limited","size_gb":10,"throttle_class_id":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create disk status = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	var disk map[string]any
	decodeBody(t, response, &disk)
	diskID := jsonID(disk["id"])
	if disk["throttle_class_id"].(float64) != 1 || disk["throttle_class_name"] != "limited" {
		t.Fatalf("created disk = %+v", disk)
	}

	classRequest, err := http.NewRequest(http.MethodPut, server.URL+"/api/disk-throttle-classes/"+classID, strings.NewReader(
		`{"name":"limited","description":"updated","limits":{"bps_total":3145728}}`))
	if err != nil {
		t.Fatal(err)
	}
	classRequest.Header.Set("Content-Type", "application/json")
	updatedClass, err := http.DefaultClient.Do(classRequest)
	if err != nil {
		t.Fatal(err)
	}
	if updatedClass.StatusCode != http.StatusOK {
		t.Fatalf("update class status = %d, body = %s", updatedClass.StatusCode, readBody(t, updatedClass))
	}
	var updatedClassBody map[string]any
	decodeBody(t, updatedClass, &updatedClassBody)
	limits := updatedClassBody["limits"].(map[string]any)
	if limits["bps_total"].(float64) != 3145728 || updatedClassBody["description"] != "updated" {
		t.Fatalf("updated class = %+v", updatedClassBody)
	}

	deleteInUse, err := http.NewRequest(http.MethodDelete, server.URL+"/api/disk-throttle-classes/"+classID, nil)
	if err != nil {
		t.Fatal(err)
	}
	inUse, err := http.DefaultClient.Do(deleteInUse)
	if err != nil {
		t.Fatal(err)
	}
	if inUse.StatusCode != http.StatusConflict {
		t.Fatalf("delete assigned class status = %d, body = %s", inUse.StatusCode, readBody(t, inUse))
	}

	clearRequest, err := http.NewRequest(http.MethodPut, server.URL+"/api/disks/"+diskID, strings.NewReader(`{"throttle_class_id":0}`))
	if err != nil {
		t.Fatal(err)
	}
	clearRequest.Header.Set("Content-Type", "application/json")
	cleared, err := http.DefaultClient.Do(clearRequest)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.StatusCode != http.StatusOK {
		t.Fatalf("clear class status = %d, body = %s", cleared.StatusCode, readBody(t, cleared))
	}
	var clearedDisk map[string]any
	decodeBody(t, cleared, &clearedDisk)
	if _, ok := clearedDisk["throttle_class_id"]; ok {
		t.Fatalf("cleared disk = %+v", clearedDisk)
	}

	before := len(provisioner.created)
	unknown, err := http.Post(server.URL+"/api/disks", "application/json", strings.NewReader(
		`{"name":"unknown","size_gb":10,"throttle_class_id":999}`))
	if err != nil {
		t.Fatal(err)
	}
	if unknown.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown class status = %d, body = %s", unknown.StatusCode, readBody(t, unknown))
	}
	if len(provisioner.created) != before {
		t.Fatalf("unknown class created a volume: %+v", provisioner.created)
	}
}
