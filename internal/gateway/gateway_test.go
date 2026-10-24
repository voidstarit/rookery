package gateway_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/readiness"
	"github.com/voidstarit/rookery/internal/store"
)

type fakeReadiness struct {
	status readiness.Status
}

func (f fakeReadiness) Ready() bool              { return f.status.Ready }
func (f fakeReadiness) Status() readiness.Status { return f.status }

func TestSystemAndVMReadOnlyRoutes(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	checked := time.Now().UTC()
	readinessState := readiness.Status{
		Ready:     true,
		CheckedAt: &checked,
		ThinPool: readiness.ThinPoolStatus{
			ComponentStatus: readiness.ComponentStatus{Ready: true, State: "ready"},
			VG:              "vg0",
			Name:            "pool",
		},
		VPP: readiness.VPPStatus{
			ComponentStatus: readiness.ComponentStatus{Ready: true, State: "ready"},
			Socket:          "/run/vpp/api.sock",
		},
	}
	gw, err := gateway.New(st, fakeReadiness{status: readinessState})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)

	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	get := func(path string) *http.Response {
		t.Helper()
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	health := get("/api/healthz")
	if health.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", health.StatusCode)
	}
	var healthBody map[string]string
	if err := json.NewDecoder(health.Body).Decode(&healthBody); err != nil {
		t.Fatal(err)
	}
	_ = health.Body.Close()
	if healthBody["status"] != "ok" {
		t.Fatalf("health body = %+v", healthBody)
	}

	statusResponse := get("/api/status")
	if statusResponse.StatusCode != http.StatusOK {
		t.Fatalf("status code = %d", statusResponse.StatusCode)
	}
	var statusBody map[string]any
	if err := json.NewDecoder(statusResponse.Body).Decode(&statusBody); err != nil {
		t.Fatal(err)
	}
	_ = statusResponse.Body.Close()
	if statusBody["ready"] != true {
		t.Fatalf("status body = %+v", statusBody)
	}
	if statusBody["thin_pool"].(map[string]any)["vg"] != "vg0" {
		t.Fatalf("status thin pool = %+v", statusBody["thin_pool"])
	}

	vms := get("/api/vms")
	if vms.StatusCode != http.StatusOK {
		t.Fatalf("vms status = %d", vms.StatusCode)
	}
	var vmsBody []any
	if err := json.NewDecoder(vms.Body).Decode(&vmsBody); err != nil {
		t.Fatal(err)
	}
	_ = vms.Body.Close()
	if len(vmsBody) != 0 {
		t.Fatalf("vms body = %+v", vmsBody)
	}

	invalid := get("/api/vms/not-a-uuid")
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid UUID status = %d", invalid.StatusCode)
	}
	var invalidBody map[string]string
	if err := json.NewDecoder(invalid.Body).Decode(&invalidBody); err != nil {
		t.Fatal(err)
	}
	_ = invalid.Body.Close()
	if invalidBody["error"] == "" {
		t.Fatalf("invalid UUID body = %+v", invalidBody)
	}

	for _, path := range []string{
		"/api/disks",
		"/api/media",
		"/api/networks",
		"/api/interfaces",
		"/api/acls",
		"/api/vnfs",
		"/api/storage-backends",
	} {
		response := get(path)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d", path, response.StatusCode)
		}
		var body []any
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatalf("%s body: %v", path, err)
		}
		_ = response.Body.Close()
		if len(body) != 0 {
			t.Fatalf("%s body = %+v", path, body)
		}
	}

	hosts := get("/api/host/interfaces")
	if hosts.StatusCode != http.StatusOK {
		t.Fatalf("host interfaces status = %d", hosts.StatusCode)
	}
	var hostBody []any
	if err := json.NewDecoder(hosts.Body).Decode(&hostBody); err != nil {
		t.Fatal(err)
	}
	_ = hosts.Body.Close()
	if len(hostBody) == 0 {
		t.Fatal("host interfaces should not be empty")
	}

	for _, path := range []string{
		"/api/disks/999",
		"/api/media/999",
		"/api/networks/999",
		"/api/interfaces/999",
		"/api/acls/999",
		"/api/vnfs/999",
		"/api/storage-backends/999",
	} {
		response := get(path)
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status = %d", path, response.StatusCode)
		}
		_ = response.Body.Close()
	}

	response, err := http.Post(server.URL+"/api/vms", "application/json", stringsReader(`{"name":"bad name"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid VM create status = %d", response.StatusCode)
	}
}

func stringsReader(value string) *strings.Reader { return strings.NewReader(value) }

func TestGatewayWithGRPCListener(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "grpc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gw, err := gateway.New(st, fakeReadiness{status: readiness.Status{Ready: true}}, gateway.WithGRPCListener(listener))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)

	conn, err := grpc.NewClient(
		listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	response, err := rookeryv1.NewSystemServiceClient(conn).GetHealth(context.Background(), &rookeryv1.GetHealthRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if response.GetStatus() != "ok" {
		t.Fatalf("gRPC health = %+v", response)
	}
	healthResponse, err := healthpb.NewHealthClient(conn).Check(context.Background(), &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if healthResponse.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("gRPC serving status = %v", healthResponse.GetStatus())
	}
}

func TestSimpleWriteRoutes(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "writes.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	gw, err := gateway.New(st, fakeReadiness{status: readiness.Status{Ready: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	post := func(path, body string) *http.Response {
		t.Helper()
		response, err := http.Post(server.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	backendResponse := post("/api/storage-backends", `{"name":"default","driver":"lvm-thin","config":{"vg":"vg0","thin_pool":"pool"}}`)
	if backendResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create backend status = %d, body = %s", backendResponse.StatusCode, readBody(t, backendResponse))
	}
	var backend map[string]any
	decodeBody(t, backendResponse, &backend)
	backendID := jsonID(backend["id"])
	if backend["config"].(map[string]any)["vg"] != "vg0" {
		t.Fatalf("backend = %+v", backend)
	}

	request, err := http.NewRequest(http.MethodPut, server.URL+"/api/storage-backends/"+backendID, strings.NewReader(`{"name":"default","title":"Default","driver":"lvm-thin","config":{"vg":"vg0","thin_pool":"pool"}}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	updated, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if updated.StatusCode != http.StatusOK {
		t.Fatalf("update backend status = %d, body = %s", updated.StatusCode, readBody(t, updated))
	}
	var updatedBackend map[string]any
	decodeBody(t, updated, &updatedBackend)
	if updatedBackend["title"] != "Default" {
		t.Fatalf("updated backend = %+v", updatedBackend)
	}

	mediaFile := filepath.Join(t.TempDir(), "installer.iso")
	if err := os.WriteFile(mediaFile, []byte("iso"), 0o644); err != nil {
		t.Fatal(err)
	}
	mediaResponse := post("/api/media", `{"name":"installer","path":`+strconvQuote(mediaFile)+`}`)
	if mediaResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create media status = %d, body = %s", mediaResponse.StatusCode, readBody(t, mediaResponse))
	}
	var media map[string]any
	decodeBody(t, mediaResponse, &media)
	mediaID := jsonID(media["id"])
	if jsonInteger(media["size_bytes"]) != 3 {
		t.Fatalf("media = %+v", media)
	}

	for _, path := range []string{
		"/api/storage-backends/" + backendID,
		"/api/media/" + mediaID,
	} {
		request, err := http.NewRequest(http.MethodDelete, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("delete %s status = %d, body = %s", path, response.StatusCode, readBody(t, response))
		}
		_ = response.Body.Close()
	}
}

func decodeBody(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func jsonInteger(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case int:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(typed, 10, 64)
		return parsed
	default:
		return 0
	}
}

func jsonID(value any) string {
	return strconv.FormatInt(jsonInteger(value), 10)
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func strconvQuote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
