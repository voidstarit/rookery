package gateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/qemu"
	"github.com/voidstarit/rookery/internal/store"
)

func TestLightVMMetrics(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "lightvm-metrics.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	kernel, err := st.CreateLightVMKernel(ctx, store.LightVMKernel{Name: "vmlinuz", Path: "/tmp/vmlinuz", SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	initramfs, err := st.CreateLightVMInitramfs(ctx, store.LightVMInitramfs{Name: "initramfs", Path: "/tmp/initramfs", SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	network, err := st.CreateNetwork(ctx, store.Network{
		Name: "lab", IPv4CIDR: "10.0.0.0/16", IPv6CIDR: "fd00:20::/64",
	})
	if err != nil {
		t.Fatal(err)
	}
	iface, err := st.CreateInterface(ctx, store.VirtualInterface{
		Name: "metrics-if", NetworkID: network.ID, MAC: "02:00:00:00:00:01",
	})
	if err != nil {
		t.Fatal(err)
	}
	disk, err := st.CreateDisk(ctx, store.Disk{Name: "data", SizeGB: 5, VG: "vg0", ThinPool: "pool"})
	if err != nil {
		t.Fatal(err)
	}
	vm, err := st.CreateLightVM(ctx, store.LightVM{
		Name: "metrics-lightvm", VCPUs: 2, MemoryMB: 512,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "alpine", ExecutablePath: "/bin/app",
		InterfaceIDs: []int64{iface.ID}, DiskIDs: []int64{disk.ID},
	})
	if err != nil {
		t.Fatal(err)
	}

	runtime := &fakeLightVMRuntime{
		running: map[string]bool{vm.ID: true},
		metrics: qemu.Metrics{
			Running: true, PID: 4321, CPUSecondsTotal: 2.5, RSSBytes: 2048, VirtualBytes: 4096,
			ReadBytesTotal: 1024, WriteBytesTotal: 2048, UptimeSeconds: 90,
		},
		blockStats: []qemu.BlockDeviceStats{{
			Device: fmt.Sprintf("rookery-disk-%d", disk.ID), ReadBytes: 1024, WriteBytes: 2048,
			FailedReadOps: 1,
		}},
	}
	counters := &fakeInterfaceCounterSource{counters: map[string]host.InterfaceCounters{
		iface.Name: {RXPackets: 10, TXPackets: 20, RXBytes: 1000, TXBytes: 2000},
	}}
	gw, err := gateway.New(st, fakeReadiness{},
		gateway.WithLightVMRuntime(runtime),
		gateway.WithInterfaceCounters(counters),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	getMetrics := func(path string) (int, map[string]any) {
		t.Helper()
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var decoded map[string]any
		if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
			t.Fatalf("decode metrics: %v", err)
		}
		return response.StatusCode, decoded
	}

	status, metrics := getMetrics("/api/lightvm/instances/" + vm.ID + "/metrics")
	if status != http.StatusOK {
		t.Fatalf("metrics status = %d, body = %+v", status, metrics)
	}
	if metrics["running"] != true || metrics["lightvm_id"] != vm.ID || metrics["pid"] != float64(4321) ||
		metrics["cpu_seconds_total"] != 2.5 || metrics["name"] != vm.Name ||
		metrics["vcpus"] != float64(2) || metrics["memory_mb"] != float64(512) {
		t.Fatalf("metrics = %+v", metrics)
	}
	blockDevices, _ := metrics["block_devices"].([]any)
	if len(blockDevices) != 1 {
		t.Fatalf("block devices = %+v", metrics["block_devices"])
	}
	blockDevice, _ := blockDevices[0].(map[string]any)
	if blockDevice["device"] != fmt.Sprintf("rookery-disk-%d", disk.ID) ||
		blockDevice["disk_id"] != float64(disk.ID) || blockDevice["name"] != "data" {
		t.Fatalf("block device = %+v", blockDevice)
	}
	if metrics["network_rx_bytes_total"] != float64(1000) || metrics["network_tx_bytes_total"] != float64(2000) ||
		metrics["network_rx_packets_total"] != float64(10) || metrics["network_tx_packets_total"] != float64(20) {
		t.Fatalf("network metrics = %+v", metrics)
	}

	// Block and interface counter failures must degrade the response, not fail it.
	runtime.blockStatsErr = errors.New("qmp unavailable")
	counters.err = errors.New("vpp unavailable")
	status, metrics = getMetrics("/api/lightvm/instances/" + vm.ID + "/metrics")
	if status != http.StatusOK || metrics["running"] != true {
		t.Fatalf("degraded metrics = %d, %+v", status, metrics)
	}
	if metrics["block_devices"] != nil {
		t.Fatalf("degraded block devices = %+v", metrics["block_devices"])
	}
	if value, ok := metrics["network_rx_bytes_total"]; ok && value != float64(0) {
		t.Fatalf("degraded network metrics = %+v", metrics)
	}
	runtime.blockStatsErr = nil
	counters.err = nil

	runtime.metricsErr = errors.New("process disappeared")
	status, _ = getMetrics("/api/lightvm/instances/" + vm.ID + "/metrics")
	if status != http.StatusInternalServerError {
		t.Fatalf("metrics error status = %d", status)
	}
	runtime.metricsErr = nil

	runtime.running[vm.ID] = false
	runtime.metrics = qemu.Metrics{}
	status, metrics = getMetrics("/api/lightvm/instances/" + vm.ID + "/metrics")
	if status != http.StatusOK || metrics["running"] == true {
		t.Fatalf("stopped metrics = %d, %+v", status, metrics)
	}
	if metrics["block_devices"] != nil {
		t.Fatalf("stopped block devices = %+v", metrics["block_devices"])
	}
	runtime.running[vm.ID] = true

	status, _ = getMetrics("/api/lightvm/instances/00000000-0000-0000-0000-000000000000/metrics")
	if status != http.StatusNotFound {
		t.Fatalf("missing lightvm status = %d", status)
	}
}
