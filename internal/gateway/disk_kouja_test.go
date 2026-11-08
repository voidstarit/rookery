package gateway_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	koujav1 "github.com/voidstarit/kouja/gen/kouja/v1"
	"google.golang.org/grpc"

	"github.com/voidstarit/rookery/internal/blockdev"
	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/kouja"
	"github.com/voidstarit/rookery/internal/storage"
	"github.com/voidstarit/rookery/internal/store"
)

type gatewayKoujaDriver struct {
	prepared  int
	createErr error
	healthErr error
}

func (d *gatewayKoujaDriver) Kind() string { return store.DiskSourceKouja }
func (d *gatewayKoujaDriver) CreateDisk(_ context.Context, _ store.StorageBackend, request storage.CreateDiskRequest) (storage.CreatedDisk, error) {
	if d.createErr != nil {
		return storage.CreatedDisk{}, d.createErr
	}
	return storage.CreatedDisk{
		Metadata: store.DiskBackendMetadata{
			Version: 1, Driver: store.DiskSourceKouja,
			Volume: store.VolumeMetadata{VolumeID: request.Disk.BackendMetadata.Volume.VolumeID, RequestID: request.RequestID,
				ResourceID: "resource-1", Owner: "spiffe://kouja/caller/rookery", StorageClass: "nvme"},
			Attachment: blockdev.Reference{Protocol: blockdev.ProtocolISCSI, ISCSI: &blockdev.ISCSIReference{
				Portal: "10.0.0.1:3260", ExportPortal: "10.0.0.1:3260",
				Target: "iqn.2026-11.io.example:disk", LUN: 0, Initiator: "iqn.2026-11.io.example:rookery",
			}},
		},
		SizeBytes: 5 << 30,
	}, nil
}
func (d *gatewayKoujaDriver) DeleteDisk(context.Context, store.StorageBackend, store.Disk) error {
	return nil
}
func (d *gatewayKoujaDriver) Health(context.Context, store.StorageBackend, *store.Disk) (storage.Health, error) {
	if d.healthErr != nil {
		return storage.Health{}, d.healthErr
	}
	return storage.Health{Reachable: true, Ready: true, Protocols: []string{"iscsi"},
		Classes: []storage.ClassCapacity{{Name: "nvme", VGName: "vg0", State: "ready", AllocatableBytes: 10}}}, nil
}
func (d *gatewayKoujaDriver) PrepareStart(context.Context, store.StorageBackend, store.Disk) (blockdev.Attachment, error) {
	d.prepared++
	return blockdev.Attachment{
		Protocol: blockdev.ProtocolISCSI,
		ISCSI: &blockdev.ISCSIAttachment{
			ISCSIReference: blockdev.ISCSIReference{Portal: "10.0.0.1:3260", Target: "iqn.2026-11.io.example:disk", LUN: 0},
			Username:       "kouja-resource-1", Credential: "secret",
		},
	}, nil
}

func TestKoujaDiskAndVMStartRoutes(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "kouja-gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	backend, err := st.CreateStorageBackend(ctx, store.StorageBackend{
		Name: "kouja", Driver: store.DiskSourceKouja,
		Config: store.StorageBackendConfig{Kouja: &store.KoujaConfig{StorageClass: "nvme"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	driver := &gatewayKoujaDriver{}
	registry := storage.NewRegistry(driver)
	runtime := &fakeVMController{}
	gw, err := gateway.New(st, fakeReadiness{status: readyStatus()},
		gateway.WithStorageRegistry(registry), gateway.WithVMController(runtime))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	body := strings.NewReader(`{"name":"remote-disk","size_gb":5,"storage_backend_id":` + jsonNumber(backend.ID) + `}`)
	response, err := http.Post(server.URL+"/api/disks", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	var disk map[string]any
	decodeBody(t, response, &disk)
	id := jsonID(disk["id"])
	if disk["source"] != "kouja" || disk["protocol"] != "iscsi" || !strings.Contains(disk["locator"].(string), "iqn.2026-11.io.example:disk") {
		t.Fatalf("disk = %+v", disk)
	}

	vmBody := strings.NewReader(`{"name":"remote-vm","disk_ids":[` + id + `]}`)
	vmResponse, err := http.Post(server.URL+"/api/vms", "application/json", vmBody)
	if err != nil {
		t.Fatal(err)
	}
	if vmResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create vm status = %d, body = %s", vmResponse.StatusCode, readBody(t, vmResponse))
	}
	if driver.prepared != 1 || len(runtime.plan.Attachments) != 1 {
		t.Fatalf("prepared=%d plan=%+v", driver.prepared, runtime.plan)
	}
	var vm map[string]any
	decodeBody(t, vmResponse, &vm)
	vmID := vm["id"].(string)

	stop, err := http.Post(server.URL+"/api/vms/"+vmID+"/stop", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = stop.Body.Close()
	deleteVM, err := http.NewRequest(http.MethodDelete, server.URL+"/api/vms/"+vmID, nil)
	if err != nil {
		t.Fatal(err)
	}
	deletedVM, err := http.DefaultClient.Do(deleteVM)
	if err != nil {
		t.Fatal(err)
	}
	_ = deletedVM.Body.Close()

	deleteDisk, err := http.NewRequest(http.MethodDelete, server.URL+"/api/disks/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	deletedDisk, err := http.DefaultClient.Do(deleteDisk)
	if err != nil {
		t.Fatal(err)
	}
	if deletedDisk.StatusCode != http.StatusAccepted {
		t.Fatalf("delete disk status = %d, body = %s", deletedDisk.StatusCode, readBody(t, deletedDisk))
	}
	_ = deletedDisk.Body.Close()
	stored, err := st.GetDisk(ctx, jsonIDInt(id))
	if err != nil || stored.DeletionState != store.DiskDeletionDeleting {
		t.Fatalf("stored disk = %+v, %v", stored, err)
	}
}

func jsonNumber(value int64) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func jsonIDInt(value string) int64 {
	var id int64
	_ = json.Unmarshal([]byte(value), &id)
	return id
}

func gatewayCert(t *testing.T) (string, string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	uri, _ := url.Parse("spiffe://kouja/caller/rookery")
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "rookery"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, BasicConstraintsValid: true, IsCA: true,
		URIs: []*url.URL{uri},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile, caFile := filepath.Join(dir, "client.crt"), filepath.Join(dir, "client.key"), filepath.Join(dir, "ca.crt")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile, caFile
}

func koujaBackendPEMBody(t *testing.T, name string) string {
	t.Helper()
	certFile, keyFile, caFile := gatewayCert(t)
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"name": name, "driver": "kouja",
		"config": map[string]any{"kouja": map[string]any{
			"endpoint": "10.0.0.1:9443", "protocol": "iscsi", "storage_class": "nvme",
			"initiator": "iqn.2026-11.io.example:rookery",
			"ca_pem":    string(caPEM), "client_cert_pem": string(certPEM), "client_key_pem": string(keyPEM),
		}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestKoujaBackendConfigAndHealthRoutes(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "kouja-backend.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	driver := &gatewayKoujaDriver{}
	registry := storage.NewRegistry(driver)
	gw, err := gateway.New(st, fakeReadiness{status: readyStatus()}, gateway.WithStorageRegistry(registry))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	response, err := http.Post(server.URL+"/api/storage-backends", "application/json", strings.NewReader(koujaBackendPEMBody(t, "kouja-backend")))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("backend create = %d, %s", response.StatusCode, readBody(t, response))
	}
	var backend map[string]any
	decodeBody(t, response, &backend)
	id := jsonID(backend["id"])
	config := backend["config"].(map[string]any)["kouja"].(map[string]any)
	if config["storage_class"] != "nvme" {
		t.Fatalf("config = %+v", backend["config"])
	}

	health, err := http.Get(server.URL + "/api/storage-backends/" + id + "/health")
	if err != nil {
		t.Fatal(err)
	}
	if health.StatusCode != http.StatusOK {
		t.Fatalf("health = %d, %s", health.StatusCode, readBody(t, health))
	}
	var healthBody map[string]any
	decodeBody(t, health, &healthBody)
	if healthBody["ready"] != true || len(healthBody["classes"].([]any)) != 1 {
		t.Fatalf("health body = %+v", healthBody)
	}

	_ = st.DeleteStorageBackend(ctx, jsonIDInt(id))
}

func TestKoujaDiskValidationAndCreateFailure(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "kouja-disk-errors.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	backend, err := st.CreateStorageBackend(ctx, store.StorageBackend{Name: "kouja", Driver: store.DiskSourceKouja})
	if err != nil {
		t.Fatal(err)
	}
	driver := &gatewayKoujaDriver{createErr: errors.New("boom")}
	gw, err := gateway.New(st, fakeReadiness{status: readyStatus()}, gateway.WithStorageRegistry(storage.NewRegistry(driver)))
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
	for _, body := range []string{
		`{"name":"template","size_gb":5,"storage_backend_id":` + jsonNumber(backend.ID) + `,"is_template":true}`,
		`{"name":"provisioned","size_gb":5,"storage_backend_id":` + jsonNumber(backend.ID) + `,"provisioning_script":"/bin/true"}`,
	} {
		response := post(body)
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("validation status = %d, %s", response.StatusCode, readBody(t, response))
		}
		_ = response.Body.Close()
	}
	response := post(`{"name":"failure","size_gb":5,"storage_backend_id":` + jsonNumber(backend.ID) + `}`)
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("create failure = %d, %s", response.StatusCode, readBody(t, response))
	}
	_ = response.Body.Close()
	failed, err := st.GetDiskByName(ctx, "failure")
	if err != nil || failed.DeletionState != store.DiskDeletionDeleting {
		t.Fatalf("failed disk = %+v, %v", failed, err)
	}
}

func TestKoujaOnlyBackendSelection(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "kouja-select.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.CreateStorageBackend(ctx, store.StorageBackend{Name: "only", Driver: store.DiskSourceKouja, Config: store.StorageBackendConfig{Kouja: &store.KoujaConfig{StorageClass: "nvme"}}}); err != nil {
		t.Fatal(err)
	}
	driver := &gatewayKoujaDriver{}
	gw, err := gateway.New(st, fakeReadiness{status: readyStatus()}, gateway.WithStorageRegistry(storage.NewRegistry(driver)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)
	response, err := http.Post(server.URL+"/api/disks", "application/json", strings.NewReader(`{"name":"implicit","size_gb":5}`))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("implicit backend = %d, %s", response.StatusCode, readBody(t, response))
	}
	_ = response.Body.Close()
}

type probeKoujaAPI struct{}

func (probeKoujaAPI) CreateVolume(context.Context, *koujav1.CreateVolumeRequest, ...grpc.CallOption) (*koujav1.CreateVolumeResponse, error) {
	return nil, nil
}
func (probeKoujaAPI) GetVolume(context.Context, *koujav1.GetVolumeRequest, ...grpc.CallOption) (*koujav1.GetVolumeResponse, error) {
	return nil, nil
}
func (probeKoujaAPI) GetCredential(context.Context, *koujav1.GetCredentialRequest, ...grpc.CallOption) (*koujav1.GetCredentialResponse, error) {
	return nil, nil
}
func (probeKoujaAPI) UnexportVolume(context.Context, *koujav1.UnexportVolumeRequest, ...grpc.CallOption) (*koujav1.UnexportVolumeResponse, error) {
	return nil, nil
}
func (probeKoujaAPI) DeleteVolume(context.Context, *koujav1.DeleteVolumeRequest, ...grpc.CallOption) (*koujav1.DeleteVolumeResponse, error) {
	return nil, nil
}
func (probeKoujaAPI) GetInfo(context.Context, *koujav1.GetInfoRequest, ...grpc.CallOption) (*koujav1.GetInfoResponse, error) {
	return &koujav1.GetInfoResponse{Ready: true, Protocols: []koujav1.Protocol{
		koujav1.Protocol_PROTOCOL_ISCSI, koujav1.Protocol_PROTOCOL_UNSPECIFIED,
	}}, nil
}
func (probeKoujaAPI) ListStorageClasses(context.Context, *koujav1.ListStorageClassesRequest, ...grpc.CallOption) (*koujav1.ListStorageClassesResponse, error) {
	return &koujav1.ListStorageClassesResponse{Classes: []*koujav1.StorageClass{{Name: "nvme"}, {Name: "archive"}}}, nil
}

func koujaProbePEMBody(t *testing.T) string {
	t.Helper()
	certFile, keyFile, caFile := gatewayCert(t)
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"config": map[string]any{
		"endpoint": "10.0.0.1:9443", "protocol": "iscsi",
		"initiator": "iqn.2026-11.io.example:rookery",
		"ca_pem":    string(caPEM), "client_cert_pem": string(certPEM), "client_key_pem": string(keyPEM),
	}}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestKoujaProbeRoute(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "kouja-probe.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	gw, err := gateway.New(st, fakeReadiness{status: readyStatus()}, gateway.WithKoujaClientFactory(func(kouja.Config) (*kouja.Client, error) {
		return kouja.NewClientFromService(probeKoujaAPI{}), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)
	response, err := http.Post(server.URL+"/api/storage-backends/kouja/probe", "application/json", strings.NewReader(koujaProbePEMBody(t)))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("probe status = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	var body map[string]any
	decodeBody(t, response, &body)
	classes, _ := body["storage_classes"].([]any)
	if body["reachable"] != true || len(classes) != 2 || body["owner"] != "spiffe://kouja/caller/rookery" {
		t.Fatalf("probe body = %+v", body)
	}
	// Invalid connection fields are rejected before a client is constructed.
	bad, err := http.Post(server.URL+"/api/storage-backends/kouja/probe", "application/json", strings.NewReader(`{"config":{"endpoint":"10.0.0.1:9443"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad probe status = %d, body = %s", bad.StatusCode, readBody(t, bad))
	}
	_ = bad.Body.Close()
}

type probeKoujaErrorAPI struct {
	probeKoujaAPI
	infoErr    error
	classesErr error
}

func (a probeKoujaErrorAPI) GetInfo(context.Context, *koujav1.GetInfoRequest, ...grpc.CallOption) (*koujav1.GetInfoResponse, error) {
	if a.infoErr != nil {
		return nil, a.infoErr
	}
	return a.probeKoujaAPI.GetInfo(context.Background(), nil)
}

func (a probeKoujaErrorAPI) ListStorageClasses(context.Context, *koujav1.ListStorageClassesRequest, ...grpc.CallOption) (*koujav1.ListStorageClassesResponse, error) {
	if a.classesErr != nil {
		return nil, a.classesErr
	}
	return a.probeKoujaAPI.ListStorageClasses(context.Background(), nil)
}

type probeKoujaNotReadyAPI struct{ probeKoujaAPI }

func (a probeKoujaNotReadyAPI) GetInfo(context.Context, *koujav1.GetInfoRequest, ...grpc.CallOption) (*koujav1.GetInfoResponse, error) {
	return &koujav1.GetInfoResponse{Ready: false, Protocols: []koujav1.Protocol{koujav1.Protocol_PROTOCOL_ISCSI}}, nil
}

func TestKoujaProbeErrors(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "kouja-probe-errors.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	tests := []struct {
		name    string
		factory func(kouja.Config) (*kouja.Client, error)
		status  int
	}{
		{name: "factory", factory: func(kouja.Config) (*kouja.Client, error) { return nil, errors.New("boom") }, status: http.StatusServiceUnavailable},
		{name: "info", factory: func(kouja.Config) (*kouja.Client, error) {
			return kouja.NewClientFromService(probeKoujaErrorAPI{infoErr: errors.New("info failed")}), nil
		}, status: http.StatusServiceUnavailable},
		{name: "classes", factory: func(kouja.Config) (*kouja.Client, error) {
			return kouja.NewClientFromService(probeKoujaErrorAPI{classesErr: errors.New("classes failed")}), nil
		}, status: http.StatusServiceUnavailable},
		{name: "not-ready", factory: func(kouja.Config) (*kouja.Client, error) {
			return kouja.NewClientFromService(probeKoujaNotReadyAPI{}), nil
		}, status: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gw, err := gateway.New(st, fakeReadiness{status: readyStatus()}, gateway.WithKoujaClientFactory(test.factory))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(gw.Close)
			server := httptest.NewServer(gw.Handler)
			t.Cleanup(server.Close)
			response, err := http.Post(server.URL+"/api/storage-backends/kouja/probe", "application/json", strings.NewReader(koujaProbePEMBody(t)))
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != test.status {
				t.Fatalf("status = %d, body = %s", response.StatusCode, readBody(t, response))
			}
			if test.status == http.StatusOK {
				var body map[string]any
				decodeBody(t, response, &body)
				if ready, ok := body["ready"]; ok && ready != false {
					t.Fatalf("not-ready body = %+v", body)
				}
				if !strings.Contains(body["detail"].(string), "not ready") {
					t.Fatalf("not-ready body = %+v", body)
				}
			} else {
				_ = response.Body.Close()
			}
		})
	}
}
