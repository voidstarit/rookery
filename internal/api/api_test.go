package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/websocket"

	"github.com/voidstarit/rookery/internal/api"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/qemu"
	"github.com/voidstarit/rookery/internal/readiness"
	"github.com/voidstarit/rookery/internal/store"
)

type apiError struct {
	Error string `json:"error"`
}

type fakeReadiness struct {
	ready  bool
	status readiness.Status
}

func (f fakeReadiness) Ready() bool {
	return f.ready
}

func (f fakeReadiness) Status() readiness.Status {
	return f.status
}

type volumeCall struct {
	VG       string
	ThinPool string
	Name     string
	SizeGB   int
}

type fakeDiskProvisioner struct {
	created     []volumeCall
	deleted     []volumeCall
	listed      []volumeCall
	thinVolumes []host.ThinVolume
	createErr   error
	deleteErr   error
	listErr     error
}

func (f *fakeDiskProvisioner) CreateThinVolume(_ context.Context, vg, thinPool, name string, sizeGB int) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, volumeCall{VG: vg, ThinPool: thinPool, Name: name, SizeGB: sizeGB})
	return nil
}

func (f *fakeDiskProvisioner) DeleteThinVolume(_ context.Context, vg, name string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, volumeCall{VG: vg, Name: name})
	return nil
}

func (f *fakeDiskProvisioner) ListThinVolumes(_ context.Context, vg, thinPool string) ([]host.ThinVolume, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	f.listed = append(f.listed, volumeCall{VG: vg, ThinPool: thinPool})
	return append([]host.ThinVolume(nil), f.thinVolumes...), nil
}

type fakeNetworkProvisioner struct {
	ensuredACLs   []int64
	deletedACLs   []uint32
	interfaceACLs map[uint32][2]uint32
	nextBD        uint32
	createdBDs    []string
	ensuredBDs    []uint32
	ensuredNames  []string
	deletedBDs    []uint32
	createdIfs    []string
	ensuredIfs    []string
	deletedIfs    []uint32
	createdTaps   []string
	deletedTaps   []uint32
	createBDErr   error
	deleteBDErr   error
	ensureBDErr   error
	createIfErr   error
	ensureIfErr   error
	deleteIfErr   error
	createTapErr  error
	deleteTapErr  error
}

func (f *fakeNetworkProvisioner) Ping(_ context.Context) error { return nil }

func (f *fakeNetworkProvisioner) EnsureVPPACL(_ context.Context, aclID int64, _ []store.ACLRule) (uint32, error) {
	f.ensuredACLs = append(f.ensuredACLs, aclID)
	return uint32(1000 + len(f.ensuredACLs)), nil
}

func (f *fakeNetworkProvisioner) DeleteVPPACL(_ context.Context, index uint32) error {
	f.deletedACLs = append(f.deletedACLs, index)
	return nil
}

func (f *fakeNetworkProvisioner) SetInterfaceACLs(_ context.Context, swIfIndex uint32, ingress, egress uint32) error {
	if f.interfaceACLs == nil {
		f.interfaceACLs = make(map[uint32][2]uint32)
	}
	f.interfaceACLs[swIfIndex] = [2]uint32{ingress, egress}
	return nil
}

func (f *fakeNetworkProvisioner) InterfaceCounters(_ context.Context, names []string) (map[string]host.InterfaceCounters, error) {
	result := make(map[string]host.InterfaceCounters, len(names))
	for _, name := range names {
		result[name] = host.InterfaceCounters{
			RXBytes:   1000,
			TXBytes:   2000,
			RXPackets: 10,
			TXPackets: 20,
		}
	}
	return result, nil
}

func (f *fakeNetworkProvisioner) Identity() (string, error) { return "fake-vpp", nil }

func (f *fakeNetworkProvisioner) CreateBridgeDomain(_ context.Context, name string, _ []uint32) (uint32, error) {
	if f.createBDErr != nil {
		return 0, f.createBDErr
	}
	f.nextBD++
	f.createdBDs = append(f.createdBDs, name)
	return f.nextBD, nil
}

func (f *fakeNetworkProvisioner) EnsureBridgeDomain(_ context.Context, bdID uint32, name string) error {
	if f.ensureBDErr != nil {
		return f.ensureBDErr
	}
	f.ensuredBDs = append(f.ensuredBDs, bdID)
	f.ensuredNames = append(f.ensuredNames, name)
	return nil
}

func (f *fakeNetworkProvisioner) DeleteBridgeDomain(_ context.Context, bdID uint32) error {
	if f.deleteBDErr != nil {
		return f.deleteBDErr
	}
	f.deletedBDs = append(f.deletedBDs, bdID)
	return nil
}

func (f *fakeNetworkProvisioner) CreateVhostUser(_ context.Context, name, _ string, _ uint32) (string, uint32, error) {
	if f.createIfErr != nil {
		return "", 0, f.createIfErr
	}
	f.createdIfs = append(f.createdIfs, name)
	return "/run/vpp/" + name + ".sock", uint32(100 + len(f.createdIfs)), nil
}

func (f *fakeNetworkProvisioner) EnsureVhostUser(_ context.Context, name, _ string, _ uint32) (string, uint32, error) {
	if f.ensureIfErr != nil {
		return "", 0, f.ensureIfErr
	}
	f.ensuredIfs = append(f.ensuredIfs, name)
	return "/run/vpp/" + name + ".sock", uint32(500 + len(f.ensuredIfs)), nil
}

func (f *fakeNetworkProvisioner) DeleteVhostUser(_ context.Context, swIfIndex uint32) error {
	if f.deleteIfErr != nil {
		return f.deleteIfErr
	}
	f.deletedIfs = append(f.deletedIfs, swIfIndex)
	return nil
}

func (f *fakeNetworkProvisioner) CreateTap(_ context.Context, hostName, _ string, _ uint32) (uint32, error) {
	if f.createTapErr != nil {
		return 0, f.createTapErr
	}
	f.createdTaps = append(f.createdTaps, hostName)
	return uint32(300 + len(f.createdTaps)), nil
}

func (f *fakeNetworkProvisioner) DeleteTap(_ context.Context, swIfIndex uint32) error {
	if f.deleteTapErr != nil {
		return f.deleteTapErr
	}
	f.deletedTaps = append(f.deletedTaps, swIfIndex)
	return nil
}

type fakeVNFManager struct {
	running       map[int64]bool
	started       []int64
	startedHosts  []string
	stopped       []int64
	cleaned       []int64
	startErr      error
	startFailures int
	stopErr       error
	leases        map[int64][]store.VNFLease
	leaseErr      error
}

func newFakeVNFManager() *fakeVNFManager {
	return &fakeVNFManager{running: make(map[int64]bool), leases: make(map[int64][]store.VNFLease)}
}

func (f *fakeVNFManager) Start(_ context.Context, vnf store.VNF, hostIf string) error {
	if f.startErr != nil {
		return f.startErr
	}
	if f.startFailures > 0 {
		f.startFailures--
		return errors.New("simulated VNF start failure")
	}
	f.running[vnf.ID] = true
	f.started = append(f.started, vnf.ID)
	f.startedHosts = append(f.startedHosts, hostIf)
	return nil
}

func (f *fakeVNFManager) Stop(_ context.Context, vnfID int64, _ bool) error {
	if f.stopErr != nil {
		return f.stopErr
	}
	delete(f.running, vnfID)
	f.stopped = append(f.stopped, vnfID)
	return nil
}

func (f *fakeVNFManager) ResetRuntime(_ context.Context) error {
	f.running = make(map[int64]bool)
	return nil
}

func (f *fakeVNFManager) Running(vnfID int64) bool {
	return f.running[vnfID]
}

func (f *fakeVNFManager) Leases(_ context.Context, vnfID int64) ([]store.VNFLease, error) {
	if f.leaseErr != nil {
		return nil, f.leaseErr
	}
	return f.leases[vnfID], nil
}

func (f *fakeVNFManager) CleanupLegacyNetwork(networkID int64) {
	f.cleaned = append(f.cleaned, networkID)
}

type fakeNAT44 struct {
	bvis          []int64
	addedIPs      []string
	hostIfs       []string
	applied       []string
	removed       []string
	deletedBVI    []string
	sessions      []string
	staticVNFID   int64
	staticRules   []store.DNATRule
	removedStatic []int64
	ensureErr     error
	applyErr      error
	removeErr     error
	sessionErr    error
	staticErr     error
}

func (f *fakeNAT44) EnsureBVI(_ context.Context, networkID int64, _ uint32) (string, uint32, error) {
	if f.ensureErr != nil {
		return "", 0, f.ensureErr
	}
	f.bvis = append(f.bvis, networkID)
	return fmt.Sprintf("bvi%d", networkID), uint32(900 + len(f.bvis)), nil
}

func (f *fakeNAT44) DeleteBVIByName(_ context.Context, name string) error {
	f.deletedBVI = append(f.deletedBVI, name)
	return nil
}

func (f *fakeNAT44) bvisContain(networkID int64) bool {
	for _, id := range f.bvis {
		if id == networkID {
			return true
		}
	}
	return false
}

func (f *fakeNAT44) AddInterfaceIPv4(_ context.Context, name, cidr string) error {
	f.addedIPs = append(f.addedIPs, name+" "+cidr)
	return nil
}

func (f *fakeNAT44) EnsureHostInterface(_ context.Context, nic string) (string, error) {
	if f.ensureErr != nil {
		return "", f.ensureErr
	}
	f.hostIfs = append(f.hostIfs, nic)
	return "host-" + nic, nil
}

func (f *fakeNAT44) ApplyNAT44(_ context.Context, bvi, outside, _, _, _ string) error {
	if f.applyErr != nil {
		return f.applyErr
	}
	f.applied = append(f.applied, bvi+"/"+outside)
	return nil
}

func (f *fakeNAT44) RemoveNAT44(_ context.Context, bvi, outside, _ string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	f.removed = append(f.removed, bvi+"/"+outside)
	return nil
}

func (f *fakeNAT44) ApplyNAT44StaticMappings(_ context.Context, _ string, vnfID int64, rules []store.DNATRule) error {
	if f.staticErr != nil {
		return f.staticErr
	}
	f.staticVNFID = vnfID
	f.staticRules = append([]store.DNATRule(nil), rules...)
	return nil
}

func (f *fakeNAT44) RemoveNAT44StaticMappings(_ context.Context, _ string, vnfID int64) error {
	if f.staticErr != nil {
		return f.staticErr
	}
	f.removedStatic = append(f.removedStatic, vnfID)
	if f.staticVNFID == vnfID {
		f.staticVNFID = 0
		f.staticRules = nil
	}
	return nil
}

func (f *fakeNAT44) NAT44Sessions(_ context.Context) ([]string, error) {
	if f.sessionErr != nil {
		return nil, f.sessionErr
	}
	return f.sessions, nil
}

type fakeVMController struct {
	running       map[string]bool
	started       []string
	stopped       []string
	reset         []string
	ctrlAltDel    []string
	runDir        string
	startErr      error
	stopErr       error
	resetErr      error
	ctrlAltDelErr error
}

func newFakeVMController(runDir string) *fakeVMController {
	return &fakeVMController{running: make(map[string]bool), runDir: runDir}
}

func (f *fakeVMController) Start(_ context.Context, vm store.VM) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.running[vm.ID] = true
	f.started = append(f.started, vm.ID)
	return nil
}

func (f *fakeVMController) Stop(_ context.Context, vmID string) error {
	if f.stopErr != nil {
		return f.stopErr
	}
	delete(f.running, vmID)
	f.stopped = append(f.stopped, vmID)
	return nil
}

func (f *fakeVMController) Reset(_ context.Context, vmID string) error {
	if f.resetErr != nil {
		return f.resetErr
	}
	f.reset = append(f.reset, vmID)
	return nil
}

func (f *fakeVMController) SendCtrlAltDel(_ context.Context, vmID string) error {
	if f.ctrlAltDelErr != nil {
		return f.ctrlAltDelErr
	}
	f.ctrlAltDel = append(f.ctrlAltDel, vmID)
	return nil
}

func (f *fakeVMController) Running(vmID string) bool {
	return f.running[vmID]
}

func (f *fakeVMController) Metrics(vmID string) (qemu.Metrics, error) {
	if !f.running[vmID] {
		return qemu.Metrics{Running: false}, nil
	}
	return qemu.Metrics{
		Running:         true,
		PID:             4242,
		UptimeSeconds:   125,
		CPUSecondsTotal: 12.5,
		RSSBytes:        64 * 1024 * 1024,
		VirtualBytes:    128 * 1024 * 1024,
		ReadBytesTotal:  4096,
		WriteBytesTotal: 8192,
	}, nil
}

func (f *fakeVMController) VNCSocket(vmID string) string {
	return filepath.Join(f.runDir, vmID+".vnc")
}

func readyReadiness() fakeReadiness {
	return fakeReadiness{
		ready: true,
		status: readiness.Status{
			Ready: true,
			ThinPool: readiness.ThinPoolStatus{
				ComponentStatus: readiness.ComponentStatus{Ready: true, State: "ready"},
				VG:              "vg0",
				Name:            "nerds-vms",
			},
			VPP: readiness.VPPStatus{
				ComponentStatus: readiness.ComponentStatus{Ready: true, State: "ready"},
				Socket:          "/run/vpp/api.sock",
			},
		},
	}
}

type testAPI struct {
	handler     http.Handler
	server      *api.Server
	store       *store.Store
	provisioner *fakeDiskProvisioner
	vms         *fakeVMController
	networks    *fakeNetworkProvisioner
	vnfs        *fakeVNFManager
	nat         *fakeNAT44
}

func newTestAPI(t *testing.T) *testAPI {
	t.Helper()
	return newTestAPIWithReadiness(t, readyReadiness())
}

func newTestAPIWithReadiness(t *testing.T, ready api.Readiness) *testAPI {
	t.Helper()

	st, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	provisioner := &fakeDiskProvisioner{}
	controller := newFakeVMController(t.TempDir())
	networkProvisioner := &fakeNetworkProvisioner{}
	vnfManager := newFakeVNFManager()
	natManager := &fakeNAT44{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	apiServer := api.New(st, ready, provisioner, controller, networkProvisioner, vnfManager, natManager, logger)
	return &testAPI{
		handler:     apiServer,
		server:      apiServer,
		store:       st,
		provisioner: provisioner,
		vms:         controller,
		networks:    networkProvisioner,
		vnfs:        vnfManager,
		nat:         natManager,
	}
}

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	return newTestAPI(t).handler
}

func newTestHandlerWithReadiness(t *testing.T, ready api.Readiness) http.Handler {
	t.Helper()
	return newTestAPIWithReadiness(t, ready).handler
}

func do(t *testing.T, handler http.Handler, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != nil {
		switch value := body.(type) {
		case string:
			reader = strings.NewReader(value)
		case []byte:
			reader = bytes.NewReader(value)
		default:
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatalf("marshal request body: %v", err)
			}
			reader = bytes.NewReader(data)
		}
	}

	req := httptest.NewRequest(method, target, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func decode[T any](t *testing.T, recorder *httptest.ResponseRecorder) T {
	t.Helper()

	var value T
	if err := json.Unmarshal(recorder.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode %q: %v", recorder.Body.String(), err)
	}
	return value
}

func createDiskAPI(t *testing.T, handler http.Handler, name string, sizeGB int) store.Disk {
	t.Helper()

	recorder := do(t, handler, http.MethodPost, "/api/disks", map[string]any{
		"name":    name,
		"size_gb": sizeGB,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create disk %q status = %d, body = %s", name, recorder.Code, recorder.Body.String())
	}
	return decode[store.Disk](t, recorder)
}

func createISO(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "install.iso")
	if err := os.WriteFile(path, []byte("iso-data"), 0o444); err != nil {
		t.Fatalf("write iso: %v", err)
	}
	return path
}

func createMediaAPI(t *testing.T, handler http.Handler, name string) store.Media {
	t.Helper()

	recorder := do(t, handler, http.MethodPost, "/api/media", map[string]any{
		"name": name,
		"path": createISO(t),
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create media %q status = %d, body = %s", name, recorder.Code, recorder.Body.String())
	}
	return decode[store.Media](t, recorder)
}

func createNetworkAPI(t *testing.T, handler http.Handler, name string) store.Network {
	t.Helper()

	recorder := do(t, handler, http.MethodPost, "/api/networks", map[string]any{"name": name})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create network %q status = %d, body = %s", name, recorder.Code, recorder.Body.String())
	}
	return decode[store.Network](t, recorder)
}

func createInterfaceAPI(t *testing.T, handler http.Handler, name string, networkID int64) store.VirtualInterface {
	t.Helper()

	recorder := do(t, handler, http.MethodPost, "/api/interfaces", map[string]any{
		"name":       name,
		"network_id": networkID,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create interface %q status = %d, body = %s", name, recorder.Code, recorder.Body.String())
	}
	return decode[store.VirtualInterface](t, recorder)
}

func createAPI(t *testing.T, handler http.Handler, name string) store.VM {
	t.Helper()

	disk := createDiskAPI(t, handler, name+"-root", 20)
	recorder := do(t, handler, http.MethodPost, "/api/vms", map[string]any{
		"name":    name,
		"disk_id": disk.ID,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create %q status = %d, body = %s", name, recorder.Code, recorder.Body.String())
	}
	return decode[store.VM](t, recorder)
}

func TestACLCRUDAndInterfaceAssignment(t *testing.T) {
	env := newTestAPI(t)
	network := createNetworkAPI(t, env.handler, "acl-net")
	iface := createInterfaceAPI(t, env.handler, "vm-acl", network.ID)

	payload := map[string]any{
		"name":        "web-acl",
		"title":       "Web traffic",
		"description": "Allow HTTP and deny the rest",
		"rules": []map[string]any{
			{
				"action":                 "permit",
				"protocol":               "tcp",
				"source":                 "0.0.0.0/0",
				"destination":            "10.20.0.0/24",
				"destination_port_start": 80,
				"destination_port_end":   80,
			},
			{
				"action":      "deny",
				"protocol":    "any",
				"source":      "0.0.0.0/0",
				"destination": "0.0.0.0/0",
			},
		},
	}
	recorder := do(t, env.handler, http.MethodPost, "/api/acls", payload)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create ACL status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	acl := decode[store.ACL](t, recorder)
	if acl.Name != "web-acl" || len(acl.Rules) != 2 {
		t.Fatalf("created ACL = %+v", acl)
	}

	recorder = do(t, env.handler, http.MethodPut, fmt.Sprintf("/api/interfaces/%d/acls", iface.ID), map[string]any{
		"ingress_acl": "web-acl",
		"egress_acl":  "",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("assign ACL status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	assigned := decode[store.VirtualInterface](t, recorder)
	if assigned.IngressACLName != "web-acl" || assigned.EgressACLName != "" {
		t.Fatalf("assigned interface = %+v", assigned)
	}
	if len(env.networks.interfaceACLs) != 1 {
		t.Fatalf("VPP interface ACLs = %+v", env.networks.interfaceACLs)
	}

	recorder = do(t, env.handler, http.MethodDelete, fmt.Sprintf("/api/acls/%d", acl.ID), nil)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("delete assigned ACL status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, env.handler, http.MethodPut, fmt.Sprintf("/api/interfaces/%d/acls", iface.ID), map[string]any{
		"ingress_acl": "",
		"egress_acl":  "",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("clear ACL status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, env.handler, http.MethodDelete, fmt.Sprintf("/api/acls/%d", acl.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete ACL status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.networks.deletedACLs) != 1 {
		t.Fatalf("deleted VPP ACLs = %v", env.networks.deletedACLs)
	}
}

func TestVMMetricsEndpoint(t *testing.T) {
	env := newTestAPI(t)
	vm := createAPI(t, env.handler, "metrics-vm")
	env.vms.running[vm.ID] = true

	recorder := do(t, env.handler, http.MethodGet, "/api/vms/"+vm.ID+"/metrics", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var metrics struct {
		Running         bool    `json:"running"`
		PID             int     `json:"pid"`
		CPUSecondsTotal float64 `json:"cpu_seconds_total"`
		RSSBytes        uint64  `json:"memory_rss_bytes"`
		ReadBytesTotal  uint64  `json:"disk_read_bytes_total"`
		VMID            string  `json:"vm_id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &metrics); err != nil {
		t.Fatalf("decode metrics: %v", err)
	}
	if !metrics.Running || metrics.PID != 4242 || metrics.CPUSecondsTotal != 12.5 || metrics.VMID != vm.ID {
		t.Fatalf("metrics = %+v", metrics)
	}
}

func TestVMCRUD(t *testing.T) {
	handler := newTestHandler(t)

	recorder := do(t, handler, http.MethodGet, "/api/vms", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("list content type = %q", contentType)
	}
	if vms := decode[[]store.VM](t, recorder); len(vms) != 0 {
		t.Fatalf("initial list = %+v, want empty", vms)
	}

	disk := createDiskAPI(t, handler, "web-01-root", 20)
	if disk.VG != "vg0" || disk.ThinPool != "nerds-vms" || disk.Device != "/dev/vg0/web-01-root" {
		t.Fatalf("created disk = %+v", disk)
	}

	recorder = do(t, handler, http.MethodPost, "/api/vms", map[string]any{"name": "web-01", "disk_id": disk.ID})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	created := decode[store.VM](t, recorder)
	if created.ID == "" {
		t.Fatal("created id is empty")
	}
	if _, err := uuid.Parse(created.ID); err != nil {
		t.Fatalf("created id = %q, want UUID: %v", created.ID, err)
	}
	if created.Status != store.StatusStopped || created.VCPUs != 1 || created.MemoryMB != 512 {
		t.Fatalf("created defaults = %+v", created)
	}
	if created.ConsoleEnabled {
		t.Fatal("new VM console access should be disabled by default")
	}
	if created.DiskID == nil || *created.DiskID != disk.ID || created.Disk == nil || created.Disk.Name != disk.Name {
		t.Fatalf("created disk = %+v, id = %v", created.Disk, created.DiskID)
	}
	if created.Description != "" {
		t.Fatalf("created description = %q, want empty", created.Description)
	}
	if want := "/api/vms/" + created.ID; recorder.Header().Get("Location") != want {
		t.Fatalf("Location = %q, want %q", recorder.Header().Get("Location"), want)
	}

	otherDisk := createDiskAPI(t, handler, "web-02-root", 10)
	recorder = do(t, handler, http.MethodPost, "/api/vms", map[string]any{"name": "WEB-01", "disk_id": otherDisk.ID})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("duplicate create status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, handler, http.MethodGet, "/api/vms/"+created.ID, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := decode[store.VM](t, recorder); got.Name != "web-01" {
		t.Fatalf("get name = %q", got.Name)
	}

	recorder = do(t, handler, http.MethodPut, "/api/vms/"+created.ID, map[string]any{
		"name":            "web-01",
		"description":     "public web node",
		"status":          store.StatusRunning,
		"vcpus":           4,
		"memory_mb":       4096,
		"console_enabled": true,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	updated := decode[store.VM](t, recorder)
	if updated.Status != store.StatusRunning || updated.VCPUs != 4 || updated.MemoryMB != 4096 {
		t.Fatalf("updated vm = %+v", updated)
	}
	if !updated.ConsoleEnabled {
		t.Fatal("updated console_enabled = false, want true")
	}
	if updated.DiskID == nil || *updated.DiskID != disk.ID || updated.Disk == nil || updated.Disk.ID != disk.ID {
		t.Fatalf("updated disk = %+v, id = %v", updated.Disk, updated.DiskID)
	}
	if updated.Description != "public web node" {
		t.Fatalf("updated description = %q", updated.Description)
	}
	if !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("created_at changed: got %v, want %v", updated.CreatedAt, created.CreatedAt)
	}

	recorder = do(t, handler, http.MethodGet, "/api/vms", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list after update status = %d", recorder.Code)
	}
	if vms := decode[[]store.VM](t, recorder); len(vms) != 1 || vms[0].ID != created.ID {
		t.Fatalf("list after update = %+v", vms)
	}

	recorder = do(t, handler, http.MethodDelete, "/api/vms/"+created.ID, nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("delete body = %q, want empty", recorder.Body.String())
	}

	recorder = do(t, handler, http.MethodGet, "/api/vms/"+created.ID, nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("get deleted status = %d", recorder.Code)
	}
	if payload := decode[apiError](t, recorder); payload.Error != "vm not found" {
		t.Fatalf("get deleted error = %q", payload.Error)
	}
}

func TestVMMultipleDisks(t *testing.T) {
	handler := newTestHandler(t)

	first := createDiskAPI(t, handler, "multi-root", 20)
	second := createDiskAPI(t, handler, "multi-data", 10)
	third := createDiskAPI(t, handler, "multi-scratch", 5)

	recorder := do(t, handler, http.MethodPost, "/api/vms", map[string]any{
		"name":     "multi",
		"disk_ids": []int64{first.ID, second.ID, second.ID},
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create multi-disk status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	created := decode[store.VM](t, recorder)
	if len(created.Disks) != 2 || len(created.DiskIDs) != 2 {
		t.Fatalf("created disks = %+v, ids = %v", created.Disks, created.DiskIDs)
	}
	if created.DiskIDs[0] != first.ID || created.DiskIDs[1] != second.ID {
		t.Fatalf("created disk ids = %v, want [%d %d]", created.DiskIDs, first.ID, second.ID)
	}
	if created.DiskID == nil || *created.DiskID != first.ID {
		t.Fatalf("created primary disk = %v, want %d", created.DiskID, first.ID)
	}

	recorder = do(t, handler, http.MethodPut, "/api/vms/"+created.ID, map[string]any{
		"name":      "multi",
		"status":    store.StatusStopped,
		"vcpus":     2,
		"memory_mb": 2048,
		"disk_ids":  []int64{second.ID, third.ID},
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("update multi-disk status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	updated := decode[store.VM](t, recorder)
	if len(updated.Disks) != 2 || updated.DiskIDs[0] != second.ID || updated.DiskIDs[1] != third.ID {
		t.Fatalf("updated disks = %+v, ids = %v", updated.Disks, updated.DiskIDs)
	}

	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/disks/%d", first.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get detached disk status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := decode[store.Disk](t, recorder); got.AttachedVMID != nil {
		t.Fatalf("detached disk still attached to %v", got.AttachedVMID)
	}

	recorder = do(t, handler, http.MethodPost, "/api/vms", map[string]any{
		"name":     "other",
		"disk_ids": []int64{first.ID},
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create other status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	other := decode[store.VM](t, recorder)

	recorder = do(t, handler, http.MethodPut, "/api/vms/"+other.ID, map[string]any{
		"name":      "other",
		"status":    store.StatusStopped,
		"vcpus":     1,
		"memory_mb": 512,
		"disk_ids":  []int64{second.ID},
	})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("attach in-use disk status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestVMDisksRequireStoppedVM(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	vm := createAPI(t, handler, "running-vm")
	other := createDiskAPI(t, handler, "running-vm-data", 10)
	env.vms.running[vm.ID] = true

	recorder := do(t, handler, http.MethodPut, "/api/vms/"+vm.ID, map[string]any{
		"name":      vm.Name,
		"status":    store.StatusRunning,
		"vcpus":     vm.VCPUs,
		"memory_mb": vm.MemoryMB,
		"disk_ids":  []int64{vm.Disks[0].ID, other.ID},
	})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("update running VM disks status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if payload := decode[apiError](t, recorder); !strings.Contains(payload.Error, "stop the VM") {
		t.Fatalf("error = %q, want stop hint", payload.Error)
	}
}

func TestStorageBackendAndDiskSelection(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler

	recorder := do(t, handler, http.MethodPost, "/api/storage-backends", map[string]any{
		"name":        "fast",
		"title":       "Fast pool",
		"driver":      "lvm-thin",
		"description": "secondary pool",
		"config":      map[string]any{"vg": "vg-fast", "thin_pool": "fast-pool"},
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create backend status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	backend := decode[store.StorageBackend](t, recorder)
	if backend.Name != "fast" || backend.Config.VG != "vg-fast" || backend.Config.ThinPool != "fast-pool" {
		t.Fatalf("backend = %+v", backend)
	}

	recorder = do(t, handler, http.MethodPost, "/api/disks", map[string]any{
		"name":               "fast-root",
		"size_gb":            20,
		"storage_backend_id": backend.ID,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create disk status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	disk := decode[store.Disk](t, recorder)
	if disk.StorageBackendName != "fast" || disk.VG != "vg-fast" || disk.ThinPool != "fast-pool" {
		t.Fatalf("disk = %+v", disk)
	}
	if len(env.provisioner.created) != 1 || env.provisioner.created[0].VG != "vg-fast" {
		t.Fatalf("provisioner calls = %+v", env.provisioner.created)
	}

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/storage-backends/%d", backend.ID), nil)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("delete used backend status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/disks/%d", disk.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete disk status = %d", recorder.Code)
	}
	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/storage-backends/%d", backend.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete backend status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestStorageBackendImportDisks(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler

	recorder := do(t, handler, http.MethodPost, "/api/storage-backends", map[string]any{
		"name":   "import-pool",
		"driver": "lvm-thin",
		"config": map[string]any{"vg": "vg-import", "thin_pool": "import-pool"},
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create backend status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	backend := decode[store.StorageBackend](t, recorder)

	recorder = do(t, handler, http.MethodPost, "/api/disks", map[string]any{
		"name":               "already-registered",
		"size_gb":            10,
		"storage_backend_id": backend.ID,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create registered disk status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	registered := decode[store.Disk](t, recorder)

	env.provisioner.thinVolumes = []host.ThinVolume{
		{Name: "legacy-root", SizeGB: 20},
		{Name: "already-registered", SizeGB: 10},
		{Name: "legacy-data", SizeGB: 6},
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/storage-backends/%d/importable-disks", backend.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list importable status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var importable []struct {
		Name               string `json:"name"`
		SizeGB             int    `json:"size_gb"`
		Registered         bool   `json:"registered"`
		DiskID             *int64 `json:"disk_id"`
		StorageBackendName string `json:"storage_backend_name"`
	}
	importable = decode[[]struct {
		Name               string `json:"name"`
		SizeGB             int    `json:"size_gb"`
		Registered         bool   `json:"registered"`
		DiskID             *int64 `json:"disk_id"`
		StorageBackendName string `json:"storage_backend_name"`
	}](t, recorder)
	if len(importable) != 3 {
		t.Fatalf("importable = %+v, want three", importable)
	}
	if !importable[0].Registered || importable[0].Name != "already-registered" ||
		importable[0].DiskID == nil || *importable[0].DiskID != registered.ID ||
		importable[0].StorageBackendName != backend.Name {
		t.Fatalf("registered importable disk = %+v", importable[0])
	}
	if importable[1].Name != "legacy-data" || importable[1].SizeGB != 6 || importable[1].Registered {
		t.Fatalf("legacy-data = %+v", importable[1])
	}
	if importable[2].Name != "legacy-root" || importable[2].SizeGB != 20 || importable[2].Registered {
		t.Fatalf("legacy-root = %+v", importable[2])
	}

	recorder = do(t, handler, http.MethodPost,
		fmt.Sprintf("/api/storage-backends/%d/import-disks", backend.ID),
		map[string]any{"names": []string{"legacy-root", "legacy-data"}})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("import status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	imported := decode[[]store.Disk](t, recorder)
	if len(imported) != 2 {
		t.Fatalf("imported = %+v, want two", imported)
	}
	if imported[0].Name != "legacy-root" || imported[0].SizeGB != 20 ||
		imported[0].StorageBackendID == nil || *imported[0].StorageBackendID != backend.ID ||
		imported[0].VG != "vg-import" || imported[0].ThinPool != "import-pool" {
		t.Fatalf("imported root = %+v", imported[0])
	}
	if len(env.provisioner.created) != 1 {
		t.Fatalf("import created host volumes: %+v", env.provisioner.created)
	}

	recorder = do(t, handler, http.MethodPost,
		fmt.Sprintf("/api/storage-backends/%d/import-disks", backend.ID),
		map[string]any{"names": []string{"legacy-root"}})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("duplicate import status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodPost,
		fmt.Sprintf("/api/storage-backends/%d/import-disks", backend.ID),
		map[string]any{"names": []string{"does-not-exist"}})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown import status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodPost,
		fmt.Sprintf("/api/storage-backends/%d/import-disks", backend.ID),
		map[string]any{"names": []string{}})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("empty import status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestDiskCRUD(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler

	recorder := do(t, handler, http.MethodGet, "/api/disks", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if disks := decode[[]store.Disk](t, recorder); len(disks) != 0 {
		t.Fatalf("initial list = %+v, want empty", disks)
	}

	recorder = do(t, handler, http.MethodPost, "/api/disks", map[string]any{"name": "vm01-root"})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	created := decode[store.Disk](t, recorder)
	if created.ID < 1 || created.Name != "vm01-root" || created.SizeGB != 10 {
		t.Fatalf("created disk = %+v", created)
	}
	if created.VG != "vg0" || created.ThinPool != "nerds-vms" || created.Device != "/dev/vg0/vm01-root" {
		t.Fatalf("created disk location = %+v", created)
	}
	if want := fmt.Sprintf("/api/disks/%d", created.ID); recorder.Header().Get("Location") != want {
		t.Fatalf("Location = %q, want %q", recorder.Header().Get("Location"), want)
	}
	if len(env.provisioner.created) != 1 {
		t.Fatalf("provisioner calls = %+v", env.provisioner.created)
	}
	if call := env.provisioner.created[0]; call.VG != "vg0" || call.ThinPool != "nerds-vms" || call.Name != "vm01-root" || call.SizeGB != 10 {
		t.Fatalf("provisioner call = %+v", call)
	}

	recorder = do(t, handler, http.MethodPost, "/api/disks", map[string]any{"name": "VM01-ROOT"})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("duplicate create status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.provisioner.created) != 1 {
		t.Fatalf("duplicate create provisioned volumes: %+v", env.provisioner.created)
	}

	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/disks/%d", created.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := decode[store.Disk](t, recorder); got.Name != "vm01-root" || got.AttachedVMID != nil {
		t.Fatalf("get disk = %+v", got)
	}

	recorder = do(t, handler, http.MethodGet, "/api/disks", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list after create status = %d", recorder.Code)
	}
	if disks := decode[[]store.Disk](t, recorder); len(disks) != 1 || disks[0].ID != created.ID {
		t.Fatalf("list after create = %+v", disks)
	}

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/disks/%d", created.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("delete body = %q, want empty", recorder.Body.String())
	}
	if len(env.provisioner.deleted) != 1 || env.provisioner.deleted[0].Name != "vm01-root" {
		t.Fatalf("provisioner deleted = %+v", env.provisioner.deleted)
	}

	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/disks/%d", created.ID), nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("get deleted status = %d", recorder.Code)
	}
	if payload := decode[apiError](t, recorder); payload.Error != "disk not found" {
		t.Fatalf("get deleted error = %q", payload.Error)
	}
}

func TestDiskCreateValidation(t *testing.T) {
	handler := newTestHandler(t)

	tests := []struct {
		name string
		body any
	}{
		{name: "empty body", body: ""},
		{name: "malformed json", body: `{"name":`},
		{name: "missing name", body: map[string]any{"size_gb": 10}},
		{name: "blank name", body: map[string]any{"name": "   "}},
		{name: "name starts with dash", body: map[string]any{"name": "-vm01-root"}},
		{name: "name with spaces", body: map[string]any{"name": "vm01 root"}},
		{name: "long name", body: map[string]any{"name": strings.Repeat("a", 128)}},
		{name: "zero size", body: map[string]any{"name": "vm01-root", "size_gb": 0}},
		{name: "too much size", body: map[string]any{"name": "vm01-root", "size_gb": 100_000}},
		{name: "unknown field", body: map[string]any{"name": "vm01-root", "wat": true}},
		{name: "trailing object", body: `{"name":"one"}{"name":"two"}`},
		{name: "wrong top-level type", body: `[{"name":"vm01-root"}]`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := do(t, handler, http.MethodPost, "/api/disks", test.body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s, want 400", recorder.Code, recorder.Body.String())
			}
			if payload := decode[apiError](t, recorder); payload.Error == "" {
				t.Fatal("error message is empty")
			}
		})
	}
}

func TestDiskAttachment(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler

	disk := createDiskAPI(t, handler, "shared-root", 20)

	recorder := do(t, handler, http.MethodPost, "/api/vms", map[string]any{
		"name":    "vm01",
		"disk_id": disk.ID,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create vm status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	vm := decode[store.VM](t, recorder)

	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/disks/%d", disk.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get attached disk status = %d", recorder.Code)
	}
	attached := decode[store.Disk](t, recorder)
	if attached.AttachedVMID == nil || *attached.AttachedVMID != vm.ID || attached.AttachedVMName != vm.Name {
		t.Fatalf("attached disk = %+v", attached)
	}

	recorder = do(t, handler, http.MethodPost, "/api/vms", map[string]any{
		"name":    "vm02",
		"disk_id": disk.ID,
	})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("attach used disk status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/disks/%d", disk.ID), nil)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("delete attached disk status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.provisioner.deleted) != 0 {
		t.Fatalf("attached disk was deleted: %+v", env.provisioner.deleted)
	}

	recorder = do(t, handler, http.MethodDelete, "/api/vms/"+vm.ID, nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete vm status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/disks/%d", disk.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get freed disk status = %d", recorder.Code)
	}
	if got := decode[store.Disk](t, recorder); got.AttachedVMID != nil {
		t.Fatalf("freed disk still attached = %+v", got)
	}

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/disks/%d", disk.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete detached disk status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestDiskProvisionerFailures(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler

	env.provisioner.createErr = errors.New("lvm unavailable")
	recorder := do(t, handler, http.MethodPost, "/api/disks", map[string]any{"name": "broken-root", "size_gb": 20})
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("create failure status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodGet, "/api/disks", nil)
	if disks := decode[[]store.Disk](t, recorder); len(disks) != 0 {
		t.Fatalf("failed create left records: %+v", disks)
	}

	env.provisioner.createErr = nil
	disk := createDiskAPI(t, handler, "good-root", 20)
	env.provisioner.deleteErr = errors.New("lvremove failed")
	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/disks/%d", disk.ID), nil)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("delete failure status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/disks/%d", disk.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("failed delete removed record: status %d", recorder.Code)
	}
}

func TestVMStartStop(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	vm := createAPI(t, handler, "vm01")

	recorder := do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/start", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("start status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	started := decode[store.VM](t, recorder)
	if started.Status != store.StatusRunning {
		t.Fatalf("start status = %q, want running", started.Status)
	}
	if !env.vms.Running(vm.ID) || len(env.vms.started) != 1 {
		t.Fatalf("controller start state = running %v, calls %v", env.vms.Running(vm.ID), env.vms.started)
	}

	recorder = do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/start", nil)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("second start status = %d, body = %s, want 409", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/stop", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("stop status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	stopped := decode[store.VM](t, recorder)
	if stopped.Status != store.StatusStopped {
		t.Fatalf("stop status = %q, want stopped", stopped.Status)
	}
	if env.vms.Running(vm.ID) || len(env.vms.stopped) != 1 {
		t.Fatalf("controller stop state = running %v, calls %v", env.vms.Running(vm.ID), env.vms.stopped)
	}

	recorder = do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/stop", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("second stop status = %d, body = %s, want 200", recorder.Code, recorder.Body.String())
	}
}

func TestVMStartFailureMarksError(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	vm := createAPI(t, handler, "vm01")

	env.vms.startErr = errors.New("KVM is not available")
	recorder := do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/start", nil)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("start status = %d, body = %s, want 500", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, handler, http.MethodGet, "/api/vms/"+vm.ID, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get vm status = %d", recorder.Code)
	}
	if got := decode[store.VM](t, recorder); got.Status != store.StatusError {
		t.Fatalf("vm status = %q, want error", got.Status)
	}
}

func TestVMStopFailureKeepsRunning(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	vm := createAPI(t, handler, "vm01")

	recorder := do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/start", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("start status = %d", recorder.Code)
	}

	env.vms.stopErr = errors.New("qemu did not stop")
	recorder = do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/stop", nil)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("stop status = %d, body = %s, want 500", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, handler, http.MethodGet, "/api/vms/"+vm.ID, nil)
	if got := decode[store.VM](t, recorder); got.Status != store.StatusRunning {
		t.Fatalf("vm status = %q, want running", got.Status)
	}
}

func TestDeleteRunningVMStopsQEMU(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	vm := createAPI(t, handler, "vm01")

	recorder := do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/start", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("start status = %d", recorder.Code)
	}

	recorder = do(t, handler, http.MethodDelete, "/api/vms/"+vm.ID, nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if env.vms.Running(vm.ID) {
		t.Fatal("QEMU still running after VM delete")
	}
	if len(env.vms.stopped) != 1 || env.vms.stopped[0] != vm.ID {
		t.Fatalf("stop calls = %v, want one for %s", env.vms.stopped, vm.ID)
	}
}

func TestVMResetAndCtrlAltDel(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	vm := createAPI(t, handler, "vm01")

	for _, action := range []string{"reset", "ctrl-alt-del"} {
		recorder := do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/"+action, nil)
		if recorder.Code != http.StatusConflict {
			t.Fatalf("%s stopped status = %d, want 409", action, recorder.Code)
		}
	}

	recorder := do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/start", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("start status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/ctrl-alt-del", nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("ctrl-alt-del status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/reset", nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("reset status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.vms.ctrlAltDel) != 1 || env.vms.ctrlAltDel[0] != vm.ID {
		t.Fatalf("ctrl-alt-del calls = %v", env.vms.ctrlAltDel)
	}
	if len(env.vms.reset) != 1 || env.vms.reset[0] != vm.ID {
		t.Fatalf("reset calls = %v", env.vms.reset)
	}

	env.vms.ctrlAltDelErr = errors.New("monitor unavailable")
	recorder = do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/ctrl-alt-del", nil)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("ctrl-alt-del failure status = %d, want 500", recorder.Code)
	}

	env.vms.resetErr = errors.New("monitor unavailable")
	recorder = do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/reset", nil)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("reset failure status = %d, want 500", recorder.Code)
	}
}

func TestVMResetFallsBackWhenMonitorMissing(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	vm := createAPI(t, handler, "vm01")

	recorder := do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/start", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("start status = %d", recorder.Code)
	}

	env.vms.resetErr = qemu.ErrMonitorUnavailable
	recorder = do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/reset", nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("reset fallback status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.vms.stopped) != 1 || env.vms.stopped[0] != vm.ID {
		t.Fatalf("stop calls = %v", env.vms.stopped)
	}
	if len(env.vms.started) != 2 || env.vms.started[1] != vm.ID {
		t.Fatalf("start calls = %v, want a restart", env.vms.started)
	}
	if !env.vms.Running(vm.ID) {
		t.Fatal("VM is not running after reset fallback")
	}

	recorder = do(t, handler, http.MethodGet, "/api/vms/"+vm.ID, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get vm status = %d", recorder.Code)
	}
	if got := decode[store.VM](t, recorder); got.Status != store.StatusRunning {
		t.Fatalf("vm status = %q, want running", got.Status)
	}
}

func TestVMCtrlAltDelReportsMissingMonitor(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	vm := createAPI(t, handler, "vm01")

	recorder := do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/start", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("start status = %d", recorder.Code)
	}

	env.vms.ctrlAltDelErr = qemu.ErrMonitorUnavailable
	recorder = do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/ctrl-alt-del", nil)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("ctrl-alt-del status = %d, body = %s, want 409", recorder.Code, recorder.Body.String())
	}
	if payload := decode[apiError](t, recorder); !strings.Contains(payload.Error, "monitor") {
		t.Fatalf("error = %q, want monitor hint", payload.Error)
	}
}

func createVNFAPI(t *testing.T, handler http.Handler, name string, networkID int64, vnfType string, enabled bool) store.VNF {
	t.Helper()
	config := map[string]any{
		"gateway":     "10.20.0.1/24",
		"range_start": "10.20.0.100",
		"range_end":   "10.20.0.200",
		"dns":         "1.1.1.1,8.8.8.8",
		"lease_time":  "12h",
	}
	if vnfType == store.VNFTypeDHCPv6 {
		config = map[string]any{
			"gateway":     "fd00:20::1/64",
			"range_start": "fd00:20::100",
			"range_end":   "fd00:20::200",
			"dns":         "2001:4860:4860::8888",
			"lease_time":  "12h",
		}
	}
	recorder := do(t, handler, http.MethodPost, "/api/vnfs", map[string]any{
		"name":       name,
		"network_id": networkID,
		"type":       vnfType,
		"enabled":    enabled,
		"config":     config,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create vnf status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	return decode[store.VNF](t, recorder)
}

func TestVNFDHCPv4Lifecycle(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "lab")

	vnf := createVNFAPI(t, handler, "lab-dhcpv4", network.ID, store.VNFTypeDHCPv4, true)
	if !vnf.Enabled || vnf.Type != store.VNFTypeDHCPv4 || vnf.TapSwIfIndex == 0 ||
		vnf.TapHostName != fmt.Sprintf("vnf%d", vnf.ID) {
		t.Fatalf("created vnf = %+v", vnf)
	}
	if len(env.networks.createdTaps) != 1 || env.networks.createdTaps[0] != vnf.TapHostName {
		t.Fatalf("created taps = %v", env.networks.createdTaps)
	}
	if len(env.vnfs.started) != 1 || env.vnfs.started[0] != vnf.ID {
		t.Fatalf("started vnfs = %v", env.vnfs.started)
	}

	recorder := do(t, handler, http.MethodGet, fmt.Sprintf("/api/vnfs?network_id=%d", network.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list vnfs status = %d", recorder.Code)
	}
	list := decode[[]map[string]any](t, recorder)
	if len(list) != 1 || list[0]["status"] != "running" {
		t.Fatalf("listed vnfs = %+v", list)
	}

	env.vnfs.leases[vnf.ID] = []store.VNFLease{{
		ClientID:  "02:00:00:00:00:01",
		Address:   "10.20.0.100",
		ExpiresAt: time.Now().Add(time.Hour),
		Active:    true,
	}}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/vnfs/%d/leases", vnf.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("leases status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var leases struct {
		VNFID  int64            `json:"vnf_id"`
		Type   string           `json:"type"`
		Leases []store.VNFLease `json:"leases"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &leases); err != nil {
		t.Fatalf("decode leases: %v", err)
	}
	if leases.VNFID != vnf.ID || leases.Type != store.VNFTypeDHCPv4 || len(leases.Leases) != 1 {
		t.Fatalf("leases = %+v", leases)
	}

	recorder = do(t, handler, http.MethodPut, fmt.Sprintf("/api/vnfs/%d", vnf.ID), map[string]any{"enabled": false})
	if recorder.Code != http.StatusOK {
		t.Fatalf("disable vnf status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	disabled := decode[store.VNF](t, recorder)
	if disabled.Enabled || disabled.TapSwIfIndex != 0 || len(env.networks.deletedTaps) == 0 {
		t.Fatalf("disabled vnf = %+v, deleted taps = %v", disabled, env.networks.deletedTaps)
	}

	recorder = do(t, handler, http.MethodPost, fmt.Sprintf("/api/vnfs/%d/restart", vnf.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("restart vnf status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	restarted := decode[store.VNF](t, recorder)
	if !restarted.Enabled || restarted.TapSwIfIndex == 0 {
		t.Fatalf("restarted vnf = %+v", restarted)
	}

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/vnfs/%d", vnf.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete vnf status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/vnfs/%d", vnf.ID), nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("get deleted vnf status = %d", recorder.Code)
	}
}

func TestVNFDHCPv6Lifecycle(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "lab")

	vnf := createVNFAPI(t, handler, "lab-dhcpv6", network.ID, store.VNFTypeDHCPv6, true)
	if !vnf.Enabled || vnf.Type != store.VNFTypeDHCPv6 {
		t.Fatalf("created vnf = %+v", vnf)
	}
	if vnf.Config.Gateway != "fd00:20::1/64" || vnf.Config.RangeStart != "fd00:20::100" {
		t.Fatalf("v6 config = %+v", vnf.Config)
	}
	if len(env.vnfs.started) != 1 {
		t.Fatalf("started vnfs = %v", env.vnfs.started)
	}
}

func TestVNFValidation(t *testing.T) {
	handler := newTestHandler(t)
	network := createNetworkAPI(t, handler, "lab")
	base := map[string]any{
		"name":       "lab-dhcp",
		"network_id": network.ID,
		"type":       store.VNFTypeDHCPv4,
		"enabled":    false,
		"config": map[string]any{
			"gateway":     "10.20.0.1/24",
			"range_start": "10.20.0.100",
			"range_end":   "10.20.0.200",
			"dns":         "1.1.1.1",
			"lease_time":  "12h",
		},
	}
	tests := []struct {
		name   string
		mutate func(map[string]any)
		status int
	}{
		{name: "missing name", mutate: func(m map[string]any) { delete(m, "name") }, status: http.StatusBadRequest},
		{name: "missing network", mutate: func(m map[string]any) { delete(m, "network_id") }, status: http.StatusBadRequest},
		{name: "unknown network", mutate: func(m map[string]any) { m["network_id"] = 999 }, status: http.StatusBadRequest},
		{name: "missing type", mutate: func(m map[string]any) { delete(m, "type") }, status: http.StatusBadRequest},
		{name: "bad type", mutate: func(m map[string]any) { m["type"] = "dns" }, status: http.StatusBadRequest},
		{name: "missing config", mutate: func(m map[string]any) { delete(m, "config") }, status: http.StatusBadRequest},
		{name: "v4 bad gateway", mutate: func(m map[string]any) { m["config"].(map[string]any)["gateway"] = "not-a-cidr" }, status: http.StatusBadRequest},
		{name: "v4 v6 gateway", mutate: func(m map[string]any) { m["config"].(map[string]any)["gateway"] = "fd00::1/64" }, status: http.StatusBadRequest},
		{name: "v4 range outside", mutate: func(m map[string]any) { m["config"].(map[string]any)["range_start"] = "10.21.0.10" }, status: http.StatusBadRequest},
		{name: "v4 range gateway", mutate: func(m map[string]any) { m["config"].(map[string]any)["range_start"] = "10.20.0.1" }, status: http.StatusBadRequest},
		{name: "v4 bad dns", mutate: func(m map[string]any) { m["config"].(map[string]any)["dns"] = "nope" }, status: http.StatusBadRequest},
		{name: "v4 bad lease", mutate: func(m map[string]any) { m["config"].(map[string]any)["lease_time"] = "forever" }, status: http.StatusBadRequest},
		{name: "v6 bad gateway", mutate: func(m map[string]any) {
			m["type"] = store.VNFTypeDHCPv6
			cfg := m["config"].(map[string]any)
			cfg["gateway"] = "10.20.0.1/24"
			cfg["range_start"] = "fd00:20::100"
			cfg["range_end"] = "fd00:20::200"
			cfg["dns"] = "2001:4860:4860::8888"
		}, status: http.StatusBadRequest},
		{name: "v6 v4 dns", mutate: func(m map[string]any) {
			m["type"] = store.VNFTypeDHCPv6
			cfg := m["config"].(map[string]any)
			cfg["gateway"] = "fd00:20::1/64"
			cfg["range_start"] = "fd00:20::100"
			cfg["range_end"] = "fd00:20::200"
			cfg["dns"] = "1.1.1.1"
		}, status: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := map[string]any{}
			for key, value := range base {
				if cfg, ok := value.(map[string]any); ok {
					copied := map[string]any{}
					for k, v := range cfg {
						copied[k] = v
					}
					body[key] = copied
				} else {
					body[key] = value
				}
			}
			test.mutate(body)
			recorder := do(t, handler, http.MethodPost, "/api/vnfs", body)
			if recorder.Code != test.status {
				t.Fatalf("status = %d, body = %s, want %d", recorder.Code, recorder.Body.String(), test.status)
			}
		})
	}

	first := createVNFAPI(t, handler, "duplicate", network.ID, store.VNFTypeDHCPv4, false)
	recorder := do(t, handler, http.MethodPost, "/api/vnfs", map[string]any{
		"name":       first.Name,
		"network_id": network.ID,
		"type":       store.VNFTypeDHCPv4,
		"config": map[string]any{
			"gateway":     "10.20.0.1/24",
			"range_start": "10.20.0.100",
			"range_end":   "10.20.0.200",
		},
	})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("duplicate name status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestVNFFailures(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "lab")
	payload := map[string]any{
		"name":       "lab-dhcp",
		"network_id": network.ID,
		"type":       store.VNFTypeDHCPv4,
		"enabled":    true,
		"config": map[string]any{
			"gateway":     "10.20.0.1/24",
			"range_start": "10.20.0.100",
			"range_end":   "10.20.0.200",
		},
	}

	env.networks.createTapErr = errors.New("tap failed")
	recorder := do(t, handler, http.MethodPost, "/api/vnfs", payload)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("tap failure status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	env.networks.createTapErr = nil

	env.vnfs.startErr = errors.New("coredhcp failed")
	recorder = do(t, handler, http.MethodPost, "/api/vnfs", payload)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("vnf start failure status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.networks.deletedTaps) == 0 {
		t.Fatal("VNF start failure did not roll back the tap")
	}
	env.vnfs.startErr = nil

	recorder = do(t, handler, http.MethodGet, "/api/vnfs", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d", recorder.Code)
	}
	if list := decode[[]store.VNF](t, recorder); len(list) != 0 {
		t.Fatalf("failed VNFs left records: %+v", list)
	}
}

func TestVNFDuplicateTypeAndNetworkDelete(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "lab")

	createVNFAPI(t, handler, "lab-dhcpv4", network.ID, store.VNFTypeDHCPv4, false)
	recorder := do(t, handler, http.MethodPost, "/api/vnfs", map[string]any{
		"name":       "second-dhcpv4",
		"network_id": network.ID,
		"type":       store.VNFTypeDHCPv4,
		"enabled":    false,
		"config": map[string]any{
			"gateway":     "10.20.0.1/24",
			"range_start": "10.20.0.100",
			"range_end":   "10.20.0.200",
		},
	})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("duplicate type status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	v6 := createVNFAPI(t, handler, "lab-dhcpv6", network.ID, store.VNFTypeDHCPv6, true)
	if !v6.Enabled {
		t.Fatalf("v6 vnf = %+v", v6)
	}

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/networks/%d", network.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete network status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.vnfs.stopped) == 0 {
		t.Fatalf("network delete did not stop VNFs: %+v", env.vnfs)
	}
	recorder = do(t, handler, http.MethodGet, "/api/vnfs", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list vnfs status = %d", recorder.Code)
	}
	if list := decode[[]store.VNF](t, recorder); len(list) != 0 {
		t.Fatalf("network delete left VNFs: %+v", list)
	}
}

func TestVNFUpdateFailureAndRecovery(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "lab")
	vnf := createVNFAPI(t, handler, "lab-dhcpv4", network.ID, store.VNFTypeDHCPv4, false)

	payload := map[string]any{
		"name":    vnf.Name,
		"enabled": true,
		"config": map[string]any{
			"gateway":     "10.20.0.1/24",
			"range_start": "10.20.0.100",
			"range_end":   "10.20.0.200",
			"dns":         "1.1.1.1",
			"lease_time":  "6h",
		},
	}
	env.vnfs.startErr = errors.New("start failed")
	recorder := do(t, handler, http.MethodPut, fmt.Sprintf("/api/vnfs/%d", vnf.ID), payload)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("failed update status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/vnfs/%d", vnf.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get vnf status = %d", recorder.Code)
	}
	failed := decode[store.VNF](t, recorder)
	if failed.Enabled || failed.TapSwIfIndex != 0 {
		t.Fatalf("failed VNF left enabled: %+v", failed)
	}
	env.vnfs.startErr = nil

	recorder = do(t, handler, http.MethodPut, fmt.Sprintf("/api/vnfs/%d", vnf.ID), payload)
	if recorder.Code != http.StatusOK {
		t.Fatalf("recovery status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recovered := decode[store.VNF](t, recorder)
	if !recovered.Enabled || recovered.TapSwIfIndex == 0 || recovered.Config.LeaseTime != "6h" {
		t.Fatalf("recovered vnf = %+v", recovered)
	}

	recorder = do(t, handler, http.MethodPut, fmt.Sprintf("/api/vnfs/%d", vnf.ID), map[string]any{"enabled": false})
	if recorder.Code != http.StatusOK {
		t.Fatalf("disable status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	disabled := decode[store.VNF](t, recorder)
	if disabled.Enabled || disabled.TapSwIfIndex != 0 {
		t.Fatalf("disabled vnf = %+v", disabled)
	}
}

func TestVNFCleansLegacyNetworkDHCP(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "lab")
	if _, err := env.store.SetNetworkDHCP(context.Background(), network.ID, store.Network{
		DHCPEnabled:      true,
		DHCPGateway:      "10.20.0.1/24",
		DHCPRangeStart:   "10.20.0.100",
		DHCPRangeEnd:     "10.20.0.200",
		DHCPDNS:          "1.1.1.1",
		DHCPTapSwIfIndex: 77,
		DHCPTapHostName:  "ndn1",
	}); err != nil {
		t.Fatalf("seed legacy DHCP: %v", err)
	}

	vnf := createVNFAPI(t, handler, "lab-dhcpv4", network.ID, store.VNFTypeDHCPv4, true)
	if vnf.TapHostName == "ndn1" || vnf.TapHostName != fmt.Sprintf("vnf%d", vnf.ID) {
		t.Fatalf("vnf tap = %+v", vnf)
	}
	foundLegacyTap := false
	for _, tap := range env.networks.deletedTaps {
		if tap == 77 {
			foundLegacyTap = true
		}
	}
	if !foundLegacyTap {
		t.Fatalf("legacy tap was not deleted: %v", env.networks.deletedTaps)
	}
	foundCleanup := false
	for _, id := range env.vnfs.cleaned {
		if id == network.ID {
			foundCleanup = true
		}
	}
	if !foundCleanup {
		t.Fatalf("legacy DHCP cleanup was not requested: %v", env.vnfs.cleaned)
	}
	got, err := env.store.GetNetwork(context.Background(), network.ID)
	if err != nil {
		t.Fatalf("get network: %v", err)
	}
	if got.DHCPEnabled || got.DHCPTapSwIfIndex != 0 || got.DHCPGateway != "" {
		t.Fatalf("legacy network DHCP was not cleared: %+v", got)
	}
}

func TestReconcileNetworks(t *testing.T) {
	env := newTestAPI(t)
	network := createNetworkAPI(t, env.handler, "lab")

	if err := env.server.ReconcileNetworks(context.Background()); err != nil {
		t.Fatalf("reconcile networks: %v", err)
	}
	found := false
	for i, bdID := range env.networks.ensuredBDs {
		if bdID == network.BridgeDomainID && env.networks.ensuredNames[i] == network.Name {
			found = true
		}
	}
	if !found {
		t.Fatalf("bridge domain was not ensured: ids=%v names=%v", env.networks.ensuredBDs, env.networks.ensuredNames)
	}

	env.networks.ensureBDErr = errors.New("bridge domain unavailable")
	if err := env.server.ReconcileNetworks(context.Background()); err == nil {
		t.Fatal("expected bridge-domain reconciliation error")
	}
}

func TestReconcileEnabledVNFs(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "lab")

	// Existing tap: reuse it and only start the CoreDHCP runtime.
	existing, err := env.store.CreateVNF(context.Background(), store.VNF{
		Name:         "existing-v4",
		NetworkID:    network.ID,
		Type:         store.VNFTypeDHCPv4,
		Enabled:      true,
		Config:       store.VNFConfig{Gateway: "10.20.0.1/24", RangeStart: "10.20.0.100", RangeEnd: "10.20.0.200"},
		TapSwIfIndex: 700,
		TapHostName:  "vnf77",
	})
	if err != nil {
		t.Fatalf("create existing vnf: %v", err)
	}
	// No tap: recreate the tap, as for a migrated legacy network.
	migrated, err := env.store.CreateVNF(context.Background(), store.VNF{
		Name:      "migrated-v6",
		NetworkID: network.ID,
		Type:      store.VNFTypeDHCPv6,
		Enabled:   true,
		Config: store.VNFConfig{
			Gateway:    "fd00:20::1/64",
			RangeStart: "fd00:20::100",
			RangeEnd:   "fd00:20::200",
		},
	})
	if err != nil {
		t.Fatalf("create migrated vnf: %v", err)
	}
	disabled, err := env.store.CreateVNF(context.Background(), store.VNF{
		Name:      "disabled-v4",
		NetworkID: network.ID,
		Type:      store.VNFTypeDHCPv4,
		Enabled:   false,
		Config:    store.VNFConfig{Gateway: "10.30.0.1/24", RangeStart: "10.30.0.100", RangeEnd: "10.30.0.200"},
	})
	if err != nil {
		t.Fatalf("create disabled vnf: %v", err)
	}

	if err := env.server.ReconcileVNFs(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(env.vnfs.started) != 2 {
		t.Fatalf("started VNFs = %v, want 2", env.vnfs.started)
	}
	if env.vnfs.startedHosts[0] != "vnf77" {
		t.Fatalf("first start host = %q, want existing tap", env.vnfs.startedHosts[0])
	}
	if _, ok := env.vnfs.running[migrated.ID]; !ok {
		t.Fatalf("migrated VNF was not started: %+v", env.vnfs)
	}
	if _, ok := env.vnfs.running[existing.ID]; !ok {
		t.Fatalf("existing VNF was not started: %+v", env.vnfs)
	}
	if env.vnfs.running[disabled.ID] {
		t.Fatal("disabled VNF was started")
	}

	gotMigrated, err := env.store.GetVNF(context.Background(), migrated.ID)
	if err != nil {
		t.Fatalf("get migrated vnf: %v", err)
	}
	if !gotMigrated.Enabled || gotMigrated.TapSwIfIndex == 0 ||
		gotMigrated.TapHostName != fmt.Sprintf("vnf%d", migrated.ID) {
		t.Fatalf("migrated vnf after reconcile = %+v", gotMigrated)
	}
	foundTap := false
	for _, tap := range env.networks.createdTaps {
		if tap == gotMigrated.TapHostName {
			foundTap = true
		}
	}
	if !foundTap {
		t.Fatalf("recreated tap %q not in %v", gotMigrated.TapHostName, env.networks.createdTaps)
	}

	// A second reconciliation is a no-op for running VNFs.
	before := len(env.vnfs.started)
	if err := env.server.ReconcileVNFs(context.Background()); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if len(env.vnfs.started) != before {
		t.Fatalf("second reconcile restarted VNFs: %v", env.vnfs.started)
	}
}

func TestRunInfrastructureReconcilerStartsEnabledVNFs(t *testing.T) {
	env := newTestAPI(t)
	network := createNetworkAPI(t, env.handler, "lab")
	vnf, err := env.store.CreateVNF(context.Background(), store.VNF{
		Name:      "startup-v4",
		NetworkID: network.ID,
		Type:      store.VNFTypeDHCPv4,
		Enabled:   true,
		Config: store.VNFConfig{
			Gateway:    "10.20.0.1/24",
			RangeStart: "10.20.0.100",
			RangeEnd:   "10.20.0.200",
		},
	})
	if err != nil {
		t.Fatalf("create vnf: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		env.server.RunInfrastructureReconciler(ctx)
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !env.vnfs.Running(vnf.ID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("VNF reconciler did not stop")
	}
	if !env.vnfs.Running(vnf.ID) {
		t.Fatal("enabled VNF was not started by the reconciler")
	}
	foundBD := false
	for _, bdID := range env.networks.ensuredBDs {
		if bdID == network.BridgeDomainID {
			foundBD = true
		}
	}
	if !foundBD {
		t.Fatalf("reconciler did not ensure bridge domain %d", network.BridgeDomainID)
	}
}

func TestReconcileRecreatesUnusableTap(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "lab")
	vnf, err := env.store.CreateVNF(context.Background(), store.VNF{
		Name:         "stale-tap",
		NetworkID:    network.ID,
		Type:         store.VNFTypeDHCPv4,
		Enabled:      true,
		Config:       store.VNFConfig{Gateway: "10.20.0.1/24", RangeStart: "10.20.0.100", RangeEnd: "10.20.0.200"},
		TapSwIfIndex: 701,
		TapHostName:  "vnfstale",
	})
	if err != nil {
		t.Fatalf("create vnf: %v", err)
	}

	env.vnfs.startFailures = 1
	if err := env.server.ReconcileVNFs(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(env.vnfs.stopped) == 0 {
		t.Fatal("stale runtime was not stopped")
	}
	foundDeleted := false
	for _, tap := range env.networks.deletedTaps {
		if tap == 701 {
			foundDeleted = true
		}
	}
	if !foundDeleted {
		t.Fatalf("stale tap was not deleted: %v", env.networks.deletedTaps)
	}
	got, err := env.store.GetVNF(context.Background(), vnf.ID)
	if err != nil {
		t.Fatalf("get vnf: %v", err)
	}
	if got.TapHostName != fmt.Sprintf("vnf%d", vnf.ID) || got.TapSwIfIndex == 0 {
		t.Fatalf("vnf after recreation = %+v", got)
	}
	if !env.vnfs.Running(vnf.ID) {
		t.Fatal("recreated VNF is not running")
	}
}

func TestReconcileVhostUsersUpdatesRuntimeMetadata(t *testing.T) {
	env := newTestAPI(t)
	network := createNetworkAPI(t, env.handler, "vhost-net")
	iface, err := env.store.CreateInterface(context.Background(), store.VirtualInterface{
		Name:       "vm-reconcile",
		NetworkID:  network.ID,
		SocketPath: "/run/vpp/stale.sock",
		MAC:        "02:00:00:00:00:42",
		SwIfIndex:  1,
	})
	if err != nil {
		t.Fatalf("create interface: %v", err)
	}
	if err := env.server.ReconcileVhostUsers(context.Background()); err != nil {
		t.Fatalf("reconcile vhosts: %v", err)
	}
	if len(env.networks.ensuredIfs) != 1 || env.networks.ensuredIfs[0] != "vm-reconcile" {
		t.Fatalf("ensured interfaces = %v", env.networks.ensuredIfs)
	}
	got, err := env.store.GetInterface(context.Background(), iface.ID)
	if err != nil {
		t.Fatalf("get interface: %v", err)
	}
	if got.SwIfIndex != 501 || got.SocketPath != "/run/vpp/vm-reconcile.sock" {
		t.Fatalf("reconciled interface = %+v", got)
	}
}

func TestNAT44VNFAndDHCPIntegration(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "nat-lab")
	dhcp := createVNFAPI(t, handler, "dhcpv4", network.ID, store.VNFTypeDHCPv4, true)

	payload := map[string]any{
		"name":       "nat44",
		"network_id": network.ID,
		"type":       store.VNFTypeNAT44,
		"enabled":    true,
		"config": map[string]any{
			"gateway":           "10.20.0.1/24",
			"outside_interface": "enp9s0",
			"outside_address":   "198.51.100.10/24",
			"outside_gateway":   "198.51.100.1",
			"dnat_rules": []map[string]any{
				{"protocol": "tcp", "external_port": 8080, "local_ip": "10.20.0.100", "local_port": 80},
				{"protocol": "udp", "external_port": 5353, "local_ip": "10.20.0.101", "local_port": 53},
			},
		},
	}
	recorder := do(t, handler, http.MethodPost, "/api/vnfs", payload)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create nat44 status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	nat := decode[store.VNF](t, recorder)
	if !nat.Enabled || nat.Type != store.VNFTypeNAT44 {
		t.Fatalf("created nat44 = %+v", nat)
	}
	if len(env.nat.bvis) != 1 || len(env.nat.hostIfs) != 1 || len(env.nat.applied) != 1 {
		t.Fatalf("NAT calls: bvis=%v host=%v applied=%v", env.nat.bvis, env.nat.hostIfs, env.nat.applied)
	}
	if len(nat.Config.DNATRules) != 2 || len(env.nat.staticRules) != 2 {
		t.Fatalf("DNAT rules were not applied: config=%+v applied=%+v", nat.Config.DNATRules, env.nat.staticRules)
	}
	if env.nat.staticRules[0].Protocol != "tcp" || env.nat.staticRules[0].ExternalPort != 8080 ||
		env.nat.staticRules[0].LocalIP != "10.20.0.100" || env.nat.staticRules[0].LocalPort != 80 {
		t.Fatalf("applied DNAT rules = %+v", env.nat.staticRules)
	}
	gotNetwork, err := env.store.GetNetwork(context.Background(), network.ID)
	if err != nil {
		t.Fatalf("get network: %v", err)
	}
	if gotNetwork.BVIHostName != fmt.Sprintf("bvi%d", network.ID) || gotNetwork.IPv4Gateway != "10.20.0.1/24" {
		t.Fatalf("network BVI = %+v", gotNetwork)
	}
	gotDHCP, err := env.store.GetVNF(context.Background(), dhcp.ID)
	if err != nil {
		t.Fatalf("get DHCP VNF: %v", err)
	}
	if gotDHCP.Config.BindIP != "10.20.0.2/24" || gotDHCP.Config.Gateway != "10.20.0.1/24" {
		t.Fatalf("DHCP VNF after NAT = %+v", gotDHCP.Config)
	}
	if len(env.vnfs.stopped) == 0 || len(env.vnfs.started) < 2 {
		t.Fatalf("DHCP VNF was not restarted: stopped=%v started=%v", env.vnfs.stopped, env.vnfs.started)
	}

	env.nat.sessions = []string{"udp 10.20.0.100:1234 -> 198.51.100.10:1234 -> 1.1.1.1:53"}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/vnfs/%d/sessions", nat.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("sessions status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var sessions struct {
		Sessions []string `json:"sessions"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &sessions); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if len(sessions.Sessions) != 1 {
		t.Fatalf("sessions = %+v", sessions)
	}

	recorder = do(t, handler, http.MethodPut, fmt.Sprintf("/api/vnfs/%d", nat.ID), map[string]any{"enabled": false})
	if recorder.Code != http.StatusOK {
		t.Fatalf("disable nat44 status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	disabled := decode[store.VNF](t, recorder)
	if disabled.Enabled || len(env.nat.removed) != 1 {
		t.Fatalf("disabled nat44 = %+v, removed = %v", disabled, env.nat.removed)
	}
	if len(env.nat.removedStatic) == 0 || len(env.nat.staticRules) != 0 {
		t.Fatalf("NAT44 DNAT rules were not removed: removed=%v applied=%+v", env.nat.removedStatic, env.nat.staticRules)
	}

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/vnfs/%d", nat.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete nat44 status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.nat.deletedBVI) != 0 {
		t.Fatalf("VNF delete unexpectedly removed network BVI: %v", env.nat.deletedBVI)
	}

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/networks/%d", network.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete network status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.nat.deletedBVI) != 1 || env.nat.deletedBVI[0] != fmt.Sprintf("bvi%d", network.ID) {
		t.Fatalf("network BVI was not removed: %v", env.nat.deletedBVI)
	}
}

func TestNAT44DeleteCleansUpFailedBVI(t *testing.T) {
	env := newTestAPI(t)
	network := createNetworkAPI(t, env.handler, "nat-lab")
	recorder := do(t, env.handler, http.MethodPost, "/api/vnfs", map[string]any{
		"name":       "nat44",
		"network_id": network.ID,
		"type":       store.VNFTypeNAT44,
		"enabled":    false,
		"config": map[string]any{
			"gateway":           "10.20.0.1/24",
			"outside_interface": "enp9s0",
		},
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create nat44 status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	nat := decode[store.VNF](t, recorder)
	if _, err := env.store.SetNetworkBVI(context.Background(), network.ID, "bvi-leftover", 42, "10.20.0.1/24"); err != nil {
		t.Fatalf("seed network BVI: %v", err)
	}
	recorder = do(t, env.handler, http.MethodDelete, fmt.Sprintf("/api/vnfs/%d", nat.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete nat44 status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.nat.deletedBVI) != 1 || env.nat.deletedBVI[0] != "bvi-leftover" {
		t.Fatalf("deleted BVIs = %v", env.nat.deletedBVI)
	}
	got, err := env.store.GetNetwork(context.Background(), network.ID)
	if err != nil {
		t.Fatalf("get network: %v", err)
	}
	if got.BVIHostName != "" || got.BVISwIfIndex != 0 || got.IPv4Gateway != "" {
		t.Fatalf("network BVI was not cleared: %+v", got)
	}
}

func TestNAT44OutsideFailureDoesNotLeaveBVI(t *testing.T) {
	env := newTestAPI(t)
	env.nat.ensureErr = errors.New("outside NIC unavailable")
	network := createNetworkAPI(t, env.handler, "nat-lab")
	recorder := do(t, env.handler, http.MethodPost, "/api/vnfs", map[string]any{
		"name":       "nat44",
		"network_id": network.ID,
		"type":       store.VNFTypeNAT44,
		"enabled":    true,
		"config": map[string]any{
			"gateway":           "10.20.0.1/24",
			"outside_interface": "enp9s0",
		},
	})
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("create nat44 status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.nat.bvis) != 0 {
		t.Fatalf("BVI was created before outside activation: %v", env.nat.bvis)
	}
	got, err := env.store.GetNetwork(context.Background(), network.ID)
	if err != nil {
		t.Fatalf("get network: %v", err)
	}
	if got.BVIHostName != "" || got.IPv4Gateway != "" {
		t.Fatalf("failed activation left network BVI: %+v", got)
	}
}

func TestNAT44DNATFailureRollsBack(t *testing.T) {
	env := newTestAPI(t)
	env.nat.staticErr = errors.New("DNAT mapping rejected")
	network := createNetworkAPI(t, env.handler, "nat-lab")
	recorder := do(t, env.handler, http.MethodPost, "/api/vnfs", map[string]any{
		"name":       "nat44",
		"network_id": network.ID,
		"type":       store.VNFTypeNAT44,
		"enabled":    true,
		"config": map[string]any{
			"gateway":           "10.20.0.1/24",
			"outside_interface": "enp9s0",
			"outside_address":   "198.51.100.10/24",
			"outside_gateway":   "198.51.100.1",
			"dnat_rules": []map[string]any{
				{"protocol": "tcp", "external_port": 8080, "local_ip": "10.20.0.100", "local_port": 80},
			},
		},
	})
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("create nat44 status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.nat.applied) != 1 || len(env.nat.removed) != 1 {
		t.Fatalf("NAT44 was not rolled back: applied=%v removed=%v", env.nat.applied, env.nat.removed)
	}
	if len(env.nat.deletedBVI) != 1 {
		t.Fatalf("BVI was not rolled back: %v", env.nat.deletedBVI)
	}
	got, err := env.store.GetNetwork(context.Background(), network.ID)
	if err != nil {
		t.Fatalf("get network: %v", err)
	}
	if got.BVIHostName != "" || got.IPv4Gateway != "" {
		t.Fatalf("failed DNAT activation left network BVI: %+v", got)
	}
	vnfs, err := env.store.ListVNFs(context.Background(), nil)
	if err != nil {
		t.Fatalf("list vnfs: %v", err)
	}
	if len(vnfs) != 0 {
		t.Fatalf("failed DNAT activation left VNF records: %+v", vnfs)
	}
}

func TestNAT44Validation(t *testing.T) {
	handler := newTestHandler(t)
	network := createNetworkAPI(t, handler, "nat-lab")
	base := map[string]any{
		"name":       "nat44",
		"network_id": network.ID,
		"type":       store.VNFTypeNAT44,
		"enabled":    false,
		"config": map[string]any{
			"gateway":           "10.20.0.1/24",
			"outside_interface": "enp9s0",
			"outside_address":   "198.51.100.10/24",
			"outside_gateway":   "198.51.100.1",
		},
	}
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "missing outside interface", mutate: func(m map[string]any) { delete(m["config"].(map[string]any), "outside_interface") }},
		{name: "bad gateway", mutate: func(m map[string]any) { m["config"].(map[string]any)["gateway"] = "nope" }},
		{name: "bad outside address", mutate: func(m map[string]any) { m["config"].(map[string]any)["outside_address"] = "nope" }},
		{name: "bad outside gateway", mutate: func(m map[string]any) { m["config"].(map[string]any)["outside_gateway"] = "nope" }},
		{name: "outside gateway without address", mutate: func(m map[string]any) { delete(m["config"].(map[string]any), "outside_address") }},
		{name: "outside gateway outside subnet", mutate: func(m map[string]any) { m["config"].(map[string]any)["outside_gateway"] = "203.0.113.1" }},
		{name: "zero outside prefix", mutate: func(m map[string]any) { m["config"].(map[string]any)["outside_address"] = "0.0.0.0/0" }},
		{name: "bad pool", mutate: func(m map[string]any) { m["config"].(map[string]any)["nat44_pool"] = "nope" }},
		{name: "dnat bad protocol", mutate: func(m map[string]any) {
			m["config"].(map[string]any)["dnat_rules"] = []map[string]any{
				{"protocol": "sctp", "external_port": 8080, "local_ip": "10.20.0.100", "local_port": 80},
			}
		}},
		{name: "dnat bad external port", mutate: func(m map[string]any) {
			m["config"].(map[string]any)["dnat_rules"] = []map[string]any{
				{"protocol": "tcp", "external_port": 0, "local_ip": "10.20.0.100", "local_port": 80},
			}
		}},
		{name: "dnat bad local port", mutate: func(m map[string]any) {
			m["config"].(map[string]any)["dnat_rules"] = []map[string]any{
				{"protocol": "tcp", "external_port": 8080, "local_ip": "10.20.0.100", "local_port": 70000},
			}
		}},
		{name: "dnat local ip outside subnet", mutate: func(m map[string]any) {
			m["config"].(map[string]any)["dnat_rules"] = []map[string]any{
				{"protocol": "tcp", "external_port": 8080, "local_ip": "10.21.0.100", "local_port": 80},
			}
		}},
		{name: "dnat local ip network address", mutate: func(m map[string]any) {
			m["config"].(map[string]any)["dnat_rules"] = []map[string]any{
				{"protocol": "tcp", "external_port": 8080, "local_ip": "10.20.0.0", "local_port": 80},
			}
		}},
		{name: "dnat duplicate external port", mutate: func(m map[string]any) {
			m["config"].(map[string]any)["dnat_rules"] = []map[string]any{
				{"protocol": "tcp", "external_port": 8080, "local_ip": "10.20.0.100", "local_port": 80},
				{"protocol": "tcp", "external_port": 8080, "local_ip": "10.20.0.101", "local_port": 80},
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := map[string]any{}
			for key, value := range base {
				if cfg, ok := value.(map[string]any); ok {
					copied := map[string]any{}
					for k, v := range cfg {
						copied[k] = v
					}
					body[key] = copied
				} else {
					body[key] = value
				}
			}
			test.mutate(body)
			recorder := do(t, handler, http.MethodPost, "/api/vnfs", body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s, want 400", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestNAT44DNATRulesPersistWhenDisabled(t *testing.T) {
	env := newTestAPI(t)
	network := createNetworkAPI(t, env.handler, "nat-lab")
	recorder := do(t, env.handler, http.MethodPost, "/api/vnfs", map[string]any{
		"name":       "nat44",
		"network_id": network.ID,
		"type":       store.VNFTypeNAT44,
		"enabled":    false,
		"config": map[string]any{
			"gateway":           "10.20.0.1/24",
			"outside_interface": "enp9s0",
			"dnat_rules": []map[string]any{
				{"protocol": "tcp", "external_port": 8443, "local_ip": "10.20.0.50", "local_port": 443},
			},
		},
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create nat44 status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	nat := decode[store.VNF](t, recorder)
	if len(nat.Config.DNATRules) != 1 || nat.Config.DNATRules[0].LocalIP != "10.20.0.50" {
		t.Fatalf("DNAT rules were not saved: %+v", nat.Config.DNATRules)
	}
	if len(env.nat.staticRules) != 0 {
		t.Fatalf("disabled NAT44 applied DNAT rules: %+v", env.nat.staticRules)
	}
	stored, err := env.store.GetVNF(context.Background(), nat.ID)
	if err != nil {
		t.Fatalf("get nat44: %v", err)
	}
	if len(stored.Config.DNATRules) != 1 || stored.Config.DNATRules[0].ExternalPort != 8443 {
		t.Fatalf("stored DNAT rules = %+v", stored.Config.DNATRules)
	}
}

func TestNAT44DNATRulesAllowSamePortPerProtocol(t *testing.T) {
	env := newTestAPI(t)
	network := createNetworkAPI(t, env.handler, "nat-lab")
	recorder := do(t, env.handler, http.MethodPost, "/api/vnfs", map[string]any{
		"name":       "nat44",
		"network_id": network.ID,
		"type":       store.VNFTypeNAT44,
		"enabled":    false,
		"config": map[string]any{
			"gateway":           "10.20.0.1/24",
			"outside_interface": "enp9s0",
			"dnat_rules": []map[string]any{
				{"protocol": "tcp", "external_port": 53, "local_ip": "10.20.0.53", "local_port": 53},
				{"protocol": "udp", "external_port": 53, "local_ip": "10.20.0.53", "local_port": 53},
			},
		},
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create nat44 status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	nat := decode[store.VNF](t, recorder)
	if len(nat.Config.DNATRules) != 2 {
		t.Fatalf("DNAT rules = %+v", nat.Config.DNATRules)
	}
}

func TestNAT44Reconcile(t *testing.T) {
	env := newTestAPI(t)
	network := createNetworkAPI(t, env.handler, "nat-lab")
	nat, err := env.store.CreateVNF(context.Background(), store.VNF{
		Name:      "nat44",
		NetworkID: network.ID,
		Type:      store.VNFTypeNAT44,
		Enabled:   true,
		Config: store.VNFConfig{
			Gateway:          "10.20.0.1/24",
			OutsideInterface: "enp9s0",
			OutsideAddress:   "198.51.100.10/24",
			OutsideGateway:   "198.51.100.1",
			DNATRules: []store.DNATRule{
				{Protocol: "tcp", ExternalPort: 8443, LocalIP: "10.20.0.50", LocalPort: 443},
			},
		},
	})
	if err != nil {
		t.Fatalf("create nat44: %v", err)
	}
	if err := env.server.ReconcileNAT44s(context.Background()); err != nil {
		t.Fatalf("reconcile nat44: %v", err)
	}
	if !env.nat.bvisContain(network.ID) || len(env.nat.applied) == 0 {
		t.Fatalf("NAT44 was not reconciled: bvis=%v applied=%v", env.nat.bvis, env.nat.applied)
	}
	if len(env.nat.staticRules) != 1 || env.nat.staticRules[0].ExternalPort != 8443 {
		t.Fatalf("NAT44 DNAT rules were not reconciled: %+v", env.nat.staticRules)
	}
	recorder := do(t, env.handler, http.MethodGet, fmt.Sprintf("/api/vnfs/%d", nat.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get nat44 status = %d", recorder.Code)
	}
	if got := decode[map[string]any](t, recorder); got["status"] != "running" {
		t.Fatalf("NAT44 status = %v, want running", got["status"])
	}
}

func TestCreateVMWithInterfaces(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler

	network := createNetworkAPI(t, handler, "lab")
	first := createInterfaceAPI(t, handler, "vm01-eth0", network.ID)
	second := createInterfaceAPI(t, handler, "vm01-eth1", network.ID)
	disk := createDiskAPI(t, handler, "vm01-root", 20)

	recorder := do(t, handler, http.MethodPost, "/api/vms", map[string]any{
		"name":          "vm01",
		"disk_id":       disk.ID,
		"interface_ids": []int64{first.ID, second.ID},
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create vm status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	vm := decode[store.VM](t, recorder)
	if len(vm.Interfaces) != 2 {
		t.Fatalf("vm interfaces = %+v, want 2", vm.Interfaces)
	}

	recorder = do(t, handler, http.MethodGet, "/api/interfaces", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list interfaces status = %d", recorder.Code)
	}
	for _, iface := range decode[[]store.VirtualInterface](t, recorder) {
		if iface.AttachedVMID == nil || *iface.AttachedVMID != vm.ID {
			t.Fatalf("interface %q not attached to created VM: %+v", iface.Name, iface)
		}
	}

	otherDisk := createDiskAPI(t, handler, "vm02-root", 20)
	recorder = do(t, handler, http.MethodPost, "/api/vms", map[string]any{
		"name":          "vm02",
		"disk_id":       otherDisk.ID,
		"interface_ids": []int64{first.ID},
	})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("attach used interface status = %d, body = %s, want 409", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, handler, http.MethodGet, "/api/vms", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list vms status = %d", recorder.Code)
	}
	if vms := decode[[]store.VM](t, recorder); len(vms) != 1 || vms[0].ID != vm.ID {
		t.Fatalf("vms = %+v, want only the first VM", vms)
	}

	recorder = do(t, handler, http.MethodDelete, "/api/vms/"+vm.ID, nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete vm status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodGet, "/api/interfaces", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list interfaces after delete status = %d", recorder.Code)
	}
	for _, iface := range decode[[]store.VirtualInterface](t, recorder) {
		if iface.AttachedVMID != nil {
			t.Fatalf("interface %q still attached after VM delete: %+v", iface.Name, iface)
		}
	}
}

func TestEditVMInterfaces(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler

	network := createNetworkAPI(t, handler, "lab")
	first := createInterfaceAPI(t, handler, "vm01-eth0", network.ID)
	second := createInterfaceAPI(t, handler, "vm01-eth1", network.ID)
	third := createInterfaceAPI(t, handler, "vm01-eth2", network.ID)
	disk := createDiskAPI(t, handler, "vm01-root", 20)

	recorder := do(t, handler, http.MethodPost, "/api/vms", map[string]any{
		"name":          "vm01",
		"disk_id":       disk.ID,
		"interface_ids": []int64{first.ID},
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create vm status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	vm := decode[store.VM](t, recorder)
	if len(vm.Interfaces) != 1 || vm.Interfaces[0].ID != first.ID {
		t.Fatalf("created vm interfaces = %+v", vm.Interfaces)
	}

	// Omitting interface_ids leaves attachments unchanged.
	recorder = do(t, handler, http.MethodPut, "/api/vms/"+vm.ID, map[string]any{"name": vm.Name})
	if recorder.Code != http.StatusOK {
		t.Fatalf("update without interfaces status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := decode[store.VM](t, recorder); len(got.Interfaces) != 1 || got.Interfaces[0].ID != first.ID {
		t.Fatalf("interfaces changed on update without interface_ids: %+v", got.Interfaces)
	}

	recorder = do(t, handler, http.MethodPut, "/api/vms/"+vm.ID, map[string]any{
		"name":          vm.Name,
		"interface_ids": []int64{first.ID, second.ID},
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("add interface status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	vm = decode[store.VM](t, recorder)
	if len(vm.Interfaces) != 2 {
		t.Fatalf("interfaces after add = %+v", vm.Interfaces)
	}

	recorder = do(t, handler, http.MethodPut, "/api/vms/"+vm.ID, map[string]any{
		"name":          vm.Name,
		"interface_ids": []int64{second.ID},
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("remove interface status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	vm = decode[store.VM](t, recorder)
	if len(vm.Interfaces) != 1 || vm.Interfaces[0].ID != second.ID {
		t.Fatalf("interfaces after remove = %+v", vm.Interfaces)
	}

	recorder = do(t, handler, http.MethodPut, "/api/vms/"+vm.ID, map[string]any{
		"name":          vm.Name,
		"interface_ids": []int64{},
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("remove all interfaces status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	vm = decode[store.VM](t, recorder)
	if len(vm.Interfaces) != 0 {
		t.Fatalf("interfaces after remove all = %+v", vm.Interfaces)
	}

	recorder = do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/start", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("start status = %d", recorder.Code)
	}
	recorder = do(t, handler, http.MethodPut, "/api/vms/"+vm.ID, map[string]any{
		"name":          vm.Name,
		"interface_ids": []int64{first.ID},
	})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("edit running interfaces status = %d, body = %s, want 409", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/stop", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("stop status = %d", recorder.Code)
	}

	recorder = do(t, handler, http.MethodPut, "/api/vms/"+vm.ID, map[string]any{
		"name":          vm.Name,
		"interface_ids": []int64{third.ID},
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("attach third interface status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	otherDisk := createDiskAPI(t, handler, "vm02-root", 20)
	recorder = do(t, handler, http.MethodPost, "/api/vms", map[string]any{
		"name":          "vm02",
		"disk_id":       otherDisk.ID,
		"interface_ids": []int64{second.ID},
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create vm02 status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, handler, http.MethodPut, "/api/vms/"+vm.ID, map[string]any{
		"name":          vm.Name,
		"interface_ids": []int64{second.ID},
	})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("attach interface used by another vm status = %d, body = %s, want 409", recorder.Code, recorder.Body.String())
	}
}

func TestNetworkInterfaceCRUD(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler

	network := createNetworkAPI(t, handler, "lab")
	if network.BridgeDomainID != 1 || network.InterfaceCount != 0 {
		t.Fatalf("created network = %+v", network)
	}
	if len(env.networks.createdBDs) != 1 || env.networks.createdBDs[0] != "lab" {
		t.Fatalf("bridge domain calls = %v", env.networks.createdBDs)
	}

	recorder := do(t, handler, http.MethodPost, "/api/networks", map[string]any{"name": "LAB"})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("duplicate network status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.networks.deletedBDs) != 1 {
		t.Fatalf("duplicate network did not roll back bridge domain: %v", env.networks.deletedBDs)
	}

	iface := createInterfaceAPI(t, handler, "vm01-eth0", network.ID)
	if iface.SocketPath != "/run/vpp/vm01-eth0.sock" || iface.SwIfIndex == 0 || iface.MAC == "" {
		t.Fatalf("created interface = %+v", iface)
	}
	if _, err := net.ParseMAC(iface.MAC); err != nil {
		t.Fatalf("interface MAC %q: %v", iface.MAC, err)
	}

	recorder = do(t, handler, http.MethodGet, "/api/interfaces", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list interfaces status = %d", recorder.Code)
	}
	if interfaces := decode[[]store.VirtualInterface](t, recorder); len(interfaces) != 1 || interfaces[0].ID != iface.ID {
		t.Fatalf("interfaces = %+v", interfaces)
	}

	vm := createAPI(t, handler, "vm01")
	recorder = do(t, handler, http.MethodPut, fmt.Sprintf("/api/interfaces/%d", iface.ID), map[string]any{
		"attached_vm_id": vm.ID,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("attach status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	attached := decode[store.VirtualInterface](t, recorder)
	if attached.AttachedVMID == nil || *attached.AttachedVMID != vm.ID || attached.AttachedVMName != vm.Name {
		t.Fatalf("attached interface = %+v", attached)
	}

	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/networks/%d", network.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get network status = %d", recorder.Code)
	}
	if got := decode[store.Network](t, recorder); got.InterfaceCount != 1 {
		t.Fatalf("network interface count = %d, want 1", got.InterfaceCount)
	}

	recorder = do(t, handler, http.MethodGet, "/api/vms/"+vm.ID, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get vm status = %d", recorder.Code)
	}
	if got := decode[store.VM](t, recorder); len(got.Interfaces) != 1 || got.Interfaces[0].ID != iface.ID {
		t.Fatalf("vm interfaces = %+v", got.Interfaces)
	}

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/networks/%d", network.ID), nil)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("delete network with interfaces status = %d, want 409", recorder.Code)
	}
	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/interfaces/%d", iface.ID), nil)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("delete attached interface status = %d, want 409", recorder.Code)
	}

	recorder = do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/start", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("start vm status = %d", recorder.Code)
	}
	recorder = do(t, handler, http.MethodPut, fmt.Sprintf("/api/interfaces/%d", iface.ID), map[string]any{})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("detach running interface status = %d, want 409", recorder.Code)
	}
	recorder = do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/stop", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("stop vm status = %d", recorder.Code)
	}

	recorder = do(t, handler, http.MethodPut, fmt.Sprintf("/api/interfaces/%d", iface.ID), map[string]any{})
	if recorder.Code != http.StatusOK {
		t.Fatalf("detach status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := decode[store.VirtualInterface](t, recorder); got.AttachedVMID != nil {
		t.Fatalf("detached interface = %+v", got)
	}

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/interfaces/%d", iface.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete interface status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.networks.deletedIfs) != 1 || env.networks.deletedIfs[0] != iface.SwIfIndex {
		t.Fatalf("deleted vhost calls = %v", env.networks.deletedIfs)
	}

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/networks/%d", network.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete network status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.networks.deletedBDs) != 2 || env.networks.deletedBDs[1] != network.BridgeDomainID {
		t.Fatalf("deleted bridge domains = %v", env.networks.deletedBDs)
	}
}

func TestNetworkInterfaceValidation(t *testing.T) {
	handler := newTestHandler(t)

	networkBad := []any{
		"",
		`{"name":`,
		map[string]any{},
		map[string]any{"name": "   "},
		map[string]any{"name": "-lab"},
		map[string]any{"name": "lab network"},
		map[string]any{"name": strings.Repeat("a", 65)},
		map[string]any{"name": "lab", "wat": true},
	}
	for _, body := range networkBad {
		recorder := do(t, handler, http.MethodPost, "/api/networks", body)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("network body %v status = %d, want 400", body, recorder.Code)
		}
	}

	network := createNetworkAPI(t, handler, "lab")
	interfaceBad := []any{
		"",
		`{"name":`,
		map[string]any{"network_id": network.ID},
		map[string]any{"name": "   ", "network_id": network.ID},
		map[string]any{"name": "-eth0", "network_id": network.ID},
		map[string]any{"name": "eth 0", "network_id": network.ID},
		map[string]any{"name": "eth0"},
		map[string]any{"name": "eth0", "network_id": 0},
		map[string]any{"name": "eth0", "network_id": 999},
		map[string]any{"name": "eth0", "network_id": network.ID, "attached_vm_id": "not-a-uuid"},
		map[string]any{"name": "eth0", "network_id": network.ID, "attached_vm_id": uuid.NewString()},
	}
	for _, body := range interfaceBad {
		recorder := do(t, handler, http.MethodPost, "/api/interfaces", body)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("interface body %v status = %d, body = %s, want 400", body, recorder.Code, recorder.Body.String())
		}
	}

	iface := createInterfaceAPI(t, handler, "eth0", network.ID)
	recorder := do(t, handler, http.MethodPost, "/api/interfaces", map[string]any{
		"name":       "ETH0",
		"network_id": network.ID,
	})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("duplicate interface status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodPut, fmt.Sprintf("/api/interfaces/%d", iface.ID), map[string]any{
		"attached_vm_id": "not-a-uuid",
	})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid attach id status = %d, want 400", recorder.Code)
	}
}

func TestNetworkInterfaceVPPFailures(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler

	env.networks.createBDErr = errors.New("VPP unavailable")
	recorder := do(t, handler, http.MethodPost, "/api/networks", map[string]any{"name": "lab"})
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("create network failure status = %d, want 500", recorder.Code)
	}
	env.networks.createBDErr = nil

	network := createNetworkAPI(t, handler, "lab")
	env.networks.createIfErr = errors.New("VPP unavailable")
	recorder = do(t, handler, http.MethodPost, "/api/interfaces", map[string]any{
		"name":       "eth0",
		"network_id": network.ID,
	})
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("create interface failure status = %d, want 500", recorder.Code)
	}
	env.networks.createIfErr = nil

	iface := createInterfaceAPI(t, handler, "eth0", network.ID)
	env.networks.deleteIfErr = errors.New("VPP unavailable")
	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/interfaces/%d", iface.ID), nil)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("delete interface failure status = %d, want 500", recorder.Code)
	}
	env.networks.deleteIfErr = nil

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/interfaces/%d", iface.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete interface status = %d", recorder.Code)
	}

	env.networks.deleteBDErr = errors.New("VPP unavailable")
	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/networks/%d", network.ID), nil)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("delete network failure status = %d, want 500", recorder.Code)
	}
}

func TestVNCProxy(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	vm := createAPI(t, handler, "vm01")

	recorder := do(t, handler, http.MethodGet, "/ws/vms/"+vm.ID+"/vnc", nil)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("disabled vnc status = %d, body = %s, want 403", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, handler, http.MethodPut, "/api/vms/"+vm.ID, map[string]any{
		"name":            vm.Name,
		"console_enabled": true,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("enable console status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	vm = decode[store.VM](t, recorder)

	recorder = do(t, handler, http.MethodGet, "/ws/vms/"+vm.ID+"/vnc", nil)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("stopped vnc status = %d, body = %s, want 409", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, handler, http.MethodGet, "/ws/vms/not-a-uuid/vnc", nil)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid vnc id status = %d, want 400", recorder.Code)
	}

	recorder = do(t, handler, http.MethodGet, "/ws/vms/"+uuid.NewString()+"/vnc", nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing vnc vm status = %d, want 404", recorder.Code)
	}

	recorder = do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/start", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("start status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	socket := env.vms.VNCSocket(vm.ID)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen vnc socket: %v", err)
	}
	defer listener.Close()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	server := httptest.NewServer(handler)
	defer server.Close()

	target := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/vms/" + vm.ID + "/vnc"
	ws, err := websocket.Dial(target, "", server.URL)
	if err != nil {
		t.Fatalf("dial vnc websocket: %v", err)
	}
	defer ws.Close()
	ws.PayloadType = websocket.BinaryFrame

	payload := []byte("vnc-frame")
	if _, err := ws.Write(payload); err != nil {
		t.Fatalf("write vnc payload: %v", err)
	}
	received := make([]byte, len(payload))
	if _, err := io.ReadFull(ws, received); err != nil {
		t.Fatalf("read vnc payload: %v", err)
	}
	if string(received) != string(payload) {
		t.Fatalf("vnc payload = %q, want %q", received, payload)
	}

	current, err := env.store.GetVM(context.Background(), vm.ID)
	if err != nil {
		t.Fatalf("get vm before disable: %v", err)
	}
	current.ConsoleEnabled = false
	if _, err := env.store.UpdateVM(context.Background(), current); err != nil {
		t.Fatalf("disable console: %v", err)
	}

	start := time.Now()
	_ = ws.SetReadDeadline(time.Now().Add(2500 * time.Millisecond))
	if _, err := ws.Read(make([]byte, 8)); err == nil {
		t.Fatal("console stayed open after console access was disabled")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("console close took %v, want under 2s", elapsed)
	}
}

func TestMediaCRUD(t *testing.T) {
	handler := newTestHandler(t)

	recorder := do(t, handler, http.MethodGet, "/api/media", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if media := decode[[]store.Media](t, recorder); len(media) != 0 {
		t.Fatalf("initial list = %+v, want empty", media)
	}

	path := createISO(t)
	recorder = do(t, handler, http.MethodPost, "/api/media", map[string]any{
		"name": "debian-13",
		"path": path,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	created := decode[store.Media](t, recorder)
	if created.ID < 1 || created.Name != "debian-13" || created.Path != path || created.SizeBytes != 8 {
		t.Fatalf("created media = %+v", created)
	}
	if want := fmt.Sprintf("/api/media/%d", created.ID); recorder.Header().Get("Location") != want {
		t.Fatalf("Location = %q, want %q", recorder.Header().Get("Location"), want)
	}

	recorder = do(t, handler, http.MethodPost, "/api/media", map[string]any{
		"name": "DEBIAN-13",
		"path": createISO(t),
	})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("duplicate create status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/media/%d", created.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := decode[store.Media](t, recorder); got.Name != "debian-13" {
		t.Fatalf("get name = %q", got.Name)
	}

	recorder = do(t, handler, http.MethodGet, "/api/media", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list after create status = %d", recorder.Code)
	}
	if media := decode[[]store.Media](t, recorder); len(media) != 1 || media[0].ID != created.ID {
		t.Fatalf("list after create = %+v", media)
	}

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/media/%d", created.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("delete removed the ISO file: %v", err)
	}

	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/media/%d", created.ID), nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("get deleted status = %d", recorder.Code)
	}
	if payload := decode[apiError](t, recorder); payload.Error != "media not found" {
		t.Fatalf("get deleted error = %q", payload.Error)
	}
}

func TestMediaCreateValidation(t *testing.T) {
	handler := newTestHandler(t)
	directory := t.TempDir()
	missing := filepath.Join(t.TempDir(), "missing.iso")

	tests := []struct {
		name string
		body any
	}{
		{name: "empty body", body: ""},
		{name: "malformed json", body: `{"name":`},
		{name: "missing name", body: map[string]any{"path": createISO(t)}},
		{name: "blank name", body: map[string]any{"name": "   ", "path": createISO(t)}},
		{name: "name starts with dash", body: map[string]any{"name": "-debian", "path": createISO(t)}},
		{name: "name with spaces", body: map[string]any{"name": "debian 13", "path": createISO(t)}},
		{name: "long name", body: map[string]any{"name": strings.Repeat("a", 128), "path": createISO(t)}},
		{name: "missing path", body: map[string]any{"name": "debian-13"}},
		{name: "blank path", body: map[string]any{"name": "debian-13", "path": "   "}},
		{name: "relative path", body: map[string]any{"name": "debian-13", "path": "isos/debian.iso"}},
		{name: "missing file", body: map[string]any{"name": "debian-13", "path": missing}},
		{name: "directory", body: map[string]any{"name": "debian-13", "path": directory}},
		{name: "unknown field", body: map[string]any{"name": "debian-13", "path": createISO(t), "size_bytes": 10}},
		{name: "trailing object", body: `{"name":"one","path":"/a.iso"}{"name":"two","path":"/b.iso"}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := do(t, handler, http.MethodPost, "/api/media", test.body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s, want 400", recorder.Code, recorder.Body.String())
			}
			if payload := decode[apiError](t, recorder); payload.Error == "" {
				t.Fatal("error message is empty")
			}
		})
	}
}

func TestVMBootMedia(t *testing.T) {
	handler := newTestHandler(t)

	disk := createDiskAPI(t, handler, "boot-test-root", 20)
	first := createMediaAPI(t, handler, "debian-13")
	second := createMediaAPI(t, handler, "ubuntu-24.04")

	recorder := do(t, handler, http.MethodPost, "/api/vms", map[string]any{
		"name":          "boot-test",
		"disk_id":       disk.ID,
		"boot_media_id": first.ID,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create vm status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	vm := decode[store.VM](t, recorder)
	if vm.BootMediaID == nil || *vm.BootMediaID != first.ID {
		t.Fatalf("boot media id = %v, want %d", vm.BootMediaID, first.ID)
	}
	if vm.BootMedia == nil || vm.BootMedia.Name != first.Name || vm.BootMedia.Path != first.Path {
		t.Fatalf("boot media = %+v", vm.BootMedia)
	}

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/media/%d", first.ID), nil)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("delete boot media status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, handler, http.MethodPut, "/api/vms/"+vm.ID, map[string]any{
		"name":          vm.Name,
		"boot_media_id": second.ID,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("change boot media status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	updated := decode[store.VM](t, recorder)
	if updated.BootMedia == nil || updated.BootMedia.ID != second.ID {
		t.Fatalf("updated boot media = %+v", updated.BootMedia)
	}

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/media/%d", first.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete detached media status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, handler, http.MethodPut, "/api/vms/"+vm.ID, map[string]any{
		"name": vm.Name,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("clear boot media status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	cleared := decode[store.VM](t, recorder)
	if cleared.BootMediaID != nil || cleared.BootMedia != nil {
		t.Fatalf("cleared boot media = %+v, id = %v", cleared.BootMedia, cleared.BootMediaID)
	}

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/media/%d", second.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete cleared media status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestCreateVMRejectsUnknownBootMedia(t *testing.T) {
	handler := newTestHandler(t)
	disk := createDiskAPI(t, handler, "missing-media-root", 20)

	recorder := do(t, handler, http.MethodPost, "/api/vms", map[string]any{
		"name":          "missing-media",
		"disk_id":       disk.ID,
		"boot_media_id": 999,
	})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("create status = %d, body = %s, want 400", recorder.Code, recorder.Body.String())
	}
	if payload := decode[apiError](t, recorder); payload.Error != "boot media not found" {
		t.Fatalf("error = %q, want boot media not found", payload.Error)
	}

	recorder = do(t, handler, http.MethodPost, "/api/vms", map[string]any{
		"name":          "bad-media-id",
		"disk_id":       disk.ID,
		"boot_media_id": -1,
	})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("negative media status = %d, body = %s, want 400", recorder.Code, recorder.Body.String())
	}
}

func TestUpdateRejectsDiskChange(t *testing.T) {
	handler := newTestHandler(t)
	vm := createAPI(t, handler, "vm01")
	other := createDiskAPI(t, handler, "vm02-root", 20)

	recorder := do(t, handler, http.MethodPut, "/api/vms/"+vm.ID, map[string]any{
		"name":    vm.Name,
		"disk_id": other.ID,
	})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("update disk status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, handler, http.MethodGet, "/api/vms/"+vm.ID, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get vm status = %d", recorder.Code)
	}
	got := decode[store.VM](t, recorder)
	if got.DiskID == nil || *got.DiskID != *vm.DiskID {
		t.Fatalf("disk changed to %+v", got.DiskID)
	}
}

func TestUpdateNameConflict(t *testing.T) {
	handler := newTestHandler(t)

	first := createAPI(t, handler, "alpha")
	second := createAPI(t, handler, "beta")

	recorder := do(t, handler, http.MethodPut, "/api/vms/"+second.ID, map[string]any{
		"name": first.Name,
	})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("update conflict status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestCreateValidation(t *testing.T) {
	handler := newTestHandler(t)

	tests := []struct {
		name string
		body any
	}{
		{name: "empty body", body: ""},
		{name: "malformed json", body: `{"name":`},
		{name: "missing name", body: map[string]any{"vcpus": 2}},
		{name: "blank name", body: map[string]any{"name": "   "}},
		{name: "name starts with dash", body: map[string]any{"name": "-web"}},
		{name: "name with spaces", body: map[string]any{"name": "web 01"}},
		{name: "long name", body: map[string]any{"name": strings.Repeat("a", 65)}},
		{name: "bad status", body: map[string]any{"name": "web-01", "status": "exploded"}},
		{name: "zero vcpus", body: map[string]any{"name": "web-01", "vcpus": 0}},
		{name: "too many vcpus", body: map[string]any{"name": "web-01", "vcpus": 129}},
		{name: "too little memory", body: map[string]any{"name": "web-01", "memory_mb": 64}},
		{name: "too much memory", body: map[string]any{"name": "web-01", "memory_mb": 2_000_000}},
		{name: "missing disk", body: map[string]any{"name": "web-01"}},
		{name: "zero disk", body: map[string]any{"name": "web-01", "disk_id": 0}},
		{name: "negative disk", body: map[string]any{"name": "web-01", "disk_id": -1}},
		{name: "unknown disk", body: map[string]any{"name": "web-01", "disk_id": 999}},
		{name: "zero interface id", body: map[string]any{"name": "web-01", "disk_id": 1, "interface_ids": []int64{0}}},
		{name: "unknown interface id", body: map[string]any{"name": "web-01", "disk_id": 1, "interface_ids": []int64{999}}},
		{name: "legacy disk_gb field", body: map[string]any{"name": "web-01", "disk_gb": 10}},
		{name: "too long description", body: map[string]any{"name": "web-01", "description": strings.Repeat("x", 2001)}},
		{name: "unknown field", body: map[string]any{"name": "web-01", "wat": true}},
		{name: "trailing object", body: `{"name":"one"}{"name":"two"}`},
		{name: "wrong top-level type", body: `[{"name":"web-01"}]`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := do(t, handler, http.MethodPost, "/api/vms", test.body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s, want 400", recorder.Code, recorder.Body.String())
			}
			if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
				t.Fatalf("content type = %q", contentType)
			}
			if payload := decode[apiError](t, recorder); payload.Error == "" {
				t.Fatal("error message is empty")
			}
		})
	}
}

func TestRequestErrors(t *testing.T) {
	handler := newTestHandler(t)
	missingVM := uuid.NewString()

	tests := []struct {
		name   string
		method string
		target string
		body   any
		status int
	}{
		{name: "invalid id", method: http.MethodGet, target: "/api/vms/abc", status: http.StatusBadRequest},
		{name: "zero id", method: http.MethodGet, target: "/api/vms/0", status: http.StatusBadRequest},
		{name: "missing vm", method: http.MethodGet, target: "/api/vms/" + missingVM, status: http.StatusNotFound},
		{name: "update missing vm", method: http.MethodPut, target: "/api/vms/" + missingVM, body: map[string]any{"name": "web-01"}, status: http.StatusNotFound},
		{name: "delete missing vm", method: http.MethodDelete, target: "/api/vms/" + missingVM, status: http.StatusNotFound},
		{name: "invalid disk id", method: http.MethodGet, target: "/api/disks/abc", status: http.StatusBadRequest},
		{name: "zero disk id", method: http.MethodGet, target: "/api/disks/0", status: http.StatusBadRequest},
		{name: "missing disk", method: http.MethodGet, target: "/api/disks/999", status: http.StatusNotFound},
		{name: "delete missing disk", method: http.MethodDelete, target: "/api/disks/999", status: http.StatusNotFound},
		{name: "invalid media id", method: http.MethodGet, target: "/api/media/abc", status: http.StatusBadRequest},
		{name: "zero media id", method: http.MethodGet, target: "/api/media/0", status: http.StatusBadRequest},
		{name: "missing media", method: http.MethodGet, target: "/api/media/999", status: http.StatusNotFound},
		{name: "delete missing media", method: http.MethodDelete, target: "/api/media/999", status: http.StatusNotFound},
		{name: "invalid network id", method: http.MethodGet, target: "/api/networks/abc", status: http.StatusBadRequest},
		{name: "missing network", method: http.MethodGet, target: "/api/networks/999", status: http.StatusNotFound},
		{name: "invalid interface id", method: http.MethodGet, target: "/api/interfaces/abc", status: http.StatusBadRequest},
		{name: "missing interface", method: http.MethodGet, target: "/api/interfaces/999", status: http.StatusNotFound},
		{name: "unknown api route", method: http.MethodGet, target: "/api/nope", status: http.StatusNotFound},
		{name: "method not allowed", method: http.MethodPatch, target: "/api/vms/" + uuid.NewString(), status: http.StatusMethodNotAllowed},
		{name: "disk method not allowed", method: http.MethodPatch, target: "/api/disks/1", status: http.StatusMethodNotAllowed},
		{name: "media method not allowed", method: http.MethodPatch, target: "/api/media/1", status: http.StatusMethodNotAllowed},
		{name: "network method not allowed", method: http.MethodPatch, target: "/api/networks/1", status: http.StatusMethodNotAllowed},
		{name: "vnf method not allowed", method: http.MethodPatch, target: "/api/vnfs/1", status: http.StatusMethodNotAllowed},
		{name: "vnf leases method not allowed", method: http.MethodPost, target: "/api/vnfs/1/leases", status: http.StatusMethodNotAllowed},
		{name: "vnf restart method not allowed", method: http.MethodGet, target: "/api/vnfs/1/restart", status: http.StatusMethodNotAllowed},
		{name: "interface method not allowed", method: http.MethodPatch, target: "/api/interfaces/1", status: http.StatusMethodNotAllowed},
		{name: "status method not allowed", method: http.MethodPost, target: "/api/status", status: http.StatusMethodNotAllowed},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := do(t, handler, test.method, test.target, test.body)
			if recorder.Code != test.status {
				t.Fatalf("status = %d, body = %s, want %d", recorder.Code, recorder.Body.String(), test.status)
			}
			if test.status == http.StatusNotFound || test.status == http.StatusBadRequest {
				if payload := decode[apiError](t, recorder); payload.Error == "" {
					t.Fatal("error message is empty")
				}
			}
		})
	}
}

func TestHealthAndStatus(t *testing.T) {
	handler := newTestHandler(t)

	recorder := do(t, handler, http.MethodGet, "/api/healthz", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("health status = %d", recorder.Code)
	}
	if payload := decode[map[string]string](t, recorder); payload["status"] != "ok" {
		t.Fatalf("health payload = %+v", payload)
	}

	recorder = do(t, handler, http.MethodGet, "/api/status", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status endpoint = %d", recorder.Code)
	}
	status := decode[readiness.Status](t, recorder)
	if !status.Ready {
		t.Fatal("status is not ready")
	}
	if !status.ThinPool.Ready || status.ThinPool.VG != "vg0" || status.ThinPool.Name != "nerds-vms" {
		t.Fatalf("thin pool status = %+v", status.ThinPool)
	}
	if !status.VPP.Ready || status.VPP.Socket != "/run/vpp/api.sock" {
		t.Fatalf("VPP status = %+v", status.VPP)
	}
}

func TestCreateBlockedWhenNotReady(t *testing.T) {
	ready := fakeReadiness{
		ready: false,
		status: readiness.Status{
			ThinPool: readiness.ThinPoolStatus{
				ComponentStatus: readiness.ComponentStatus{
					State:  "missing",
					Detail: "thin pool vg0/nerds-vms does not exist",
				},
				VG:   "vg0",
				Name: "nerds-vms",
			},
			VPP: readiness.VPPStatus{
				ComponentStatus: readiness.ComponentStatus{
					State:  "unreachable",
					Detail: "connect /run/vpp/api.sock: no such file or directory",
				},
				Socket: "/run/vpp/api.sock",
			},
		},
	}
	env := newTestAPIWithReadiness(t, ready)
	handler := env.handler

	recorder := do(t, handler, http.MethodGet, "/api/status", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status endpoint = %d", recorder.Code)
	}
	status := decode[readiness.Status](t, recorder)
	if status.Ready {
		t.Fatal("status is ready, want not ready")
	}

	recorder = do(t, handler, http.MethodPost, "/api/vms", map[string]any{"name": "web-01", "disk_id": 1})
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("create status = %d, body = %s, want 503", recorder.Code, recorder.Body.String())
	}
	payload := decode[apiError](t, recorder)
	for _, want := range []string{"blocked", "thin pool", "FD.io VPP"} {
		if !strings.Contains(payload.Error, want) {
			t.Fatalf("error %q does not contain %q", payload.Error, want)
		}
	}

	recorder = do(t, handler, http.MethodPost, "/api/disks", map[string]any{"name": "vm01-root"})
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("disk create status = %d, body = %s, want 503", recorder.Code, recorder.Body.String())
	}
	if payload := decode[apiError](t, recorder); !strings.Contains(payload.Error, "thin pool") {
		t.Fatalf("disk create error = %q, want thin pool reason", payload.Error)
	}

	recorder = do(t, handler, http.MethodPost, "/api/networks", map[string]any{"name": "lab"})
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("network create status = %d, body = %s, want 503", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodPost, "/api/interfaces", map[string]any{"name": "eth0", "network_id": 1})
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("interface create status = %d, body = %s, want 503", recorder.Code, recorder.Body.String())
	}
	network, err := env.store.CreateNetwork(context.Background(), store.Network{Name: "lab", BridgeDomainID: 100})
	if err != nil {
		t.Fatalf("create seeded network: %v", err)
	}
	recorder = do(t, handler, http.MethodPost, "/api/vnfs", map[string]any{
		"name":       "lab-dhcp",
		"network_id": network.ID,
		"type":       store.VNFTypeDHCPv4,
		"enabled":    true,
		"config": map[string]any{
			"gateway":     "10.20.0.1/24",
			"range_start": "10.20.0.100",
			"range_end":   "10.20.0.200",
		},
	})
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("vnf create status = %d, body = %s, want 503", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, handler, http.MethodGet, "/api/vms", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d", recorder.Code)
	}
	if vms := decode[[]store.VM](t, recorder); len(vms) != 0 {
		t.Fatalf("blocked create inserted VMs: %+v", vms)
	}
}

func TestCreateDiskDoesNotRequireVPP(t *testing.T) {
	ready := fakeReadiness{
		ready: false,
		status: readiness.Status{
			ThinPool: readiness.ThinPoolStatus{
				ComponentStatus: readiness.ComponentStatus{Ready: true, State: "ready"},
				VG:              "vg0",
				Name:            "nerds-vms",
			},
			VPP: readiness.VPPStatus{
				ComponentStatus: readiness.ComponentStatus{State: "unreachable"},
				Socket:          "/run/vpp/api.sock",
			},
		},
	}
	handler := newTestHandlerWithReadiness(t, ready)

	recorder := do(t, handler, http.MethodPost, "/api/disks", map[string]any{"name": "vm01-root"})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("disk create status = %d, body = %s, want 201", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, handler, http.MethodPost, "/api/vms", map[string]any{"name": "vm01", "disk_id": 1})
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("vm create status = %d, body = %s, want 503", recorder.Code, recorder.Body.String())
	}
}

func TestHealthAndEmbeddedUI(t *testing.T) {
	handler := newTestHandler(t)

	recorder := do(t, handler, http.MethodGet, "/", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("index status = %d", recorder.Code)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Fatalf("index content type = %q", contentType)
	}
	if !strings.Contains(recorder.Body.String(), "rookery") || !strings.Contains(recorder.Body.String(), "Virtual machines") {
		t.Fatal("index does not look like the embedded UI")
	}
	for _, want := range []string{"Readiness", "LVM thin pool", "FD.io VPP", `id="new-vm"`, `id="new-vm-hint"`, `id="vms-root"`, `id="tab-disks"`, `id="new-disk"`, `id="disks-root"`, `id="tab-media"`, `id="new-media"`, `id="media-root"`, `id="tab-networks"`, `id="new-network"`, `id="networks-root"`, `id="tab-vnfs"`, `id="vnfs-root"`} {
		if !strings.Contains(recorder.Body.String(), want) {
			t.Fatalf("index does not contain %q", want)
		}
	}
	if csp := recorder.Header().Get("Content-Security-Policy"); csp == "" {
		t.Fatal("Content-Security-Policy header is missing")
	}

	for _, asset := range []string{"/build/app.js", "/style.css", "/favicon.svg", "/console.html", "/build/console.js", "/console.css", "/novnc/core/rfb.js", "/novnc/LICENSE.txt"} {
		recorder = do(t, handler, http.MethodGet, asset, nil)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d", asset, recorder.Code)
		}
		if recorder.Body.Len() == 0 {
			t.Fatalf("%s is empty", asset)
		}
	}

	recorder = do(t, handler, http.MethodPost, "/", nil)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST / status = %d, want 405", recorder.Code)
	}
}
