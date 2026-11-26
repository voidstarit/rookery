package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"golang.org/x/net/websocket"

	"github.com/voidstarit/rookery/internal/api"
	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/lifecycle"
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
	ensuredACLs          []int64
	deletedACLs          []uint32
	interfaceACLs        map[uint32][2][]uint32
	nextBD               uint32
	createdBDs           []string
	ensuredBDs           []uint32
	ensuredNames         []string
	deletedBDs           []uint32
	ensuredPipes         [][3]uint32
	deletedPipes         []uint32
	interfaceRouteTables []string
	createdIfs           []string
	ensuredIfs           []string
	deletedIfs           []uint32
	createdTaps          []string
	deletedTaps          []uint32
	ensuredHostTaps      []string
	deletedHostTaps      []string
	addedAddresses       []string
	deletedAddresses     []string
	enabledDHCP          []string
	disabledDHCP         []string
	createdMemifs        []string
	deletedMemifs        []uint32
	createMemifErr       error
	deleteMemifErr       error
	resetCounters        int
	createBDErr          error
	deleteBDErr          error
	ensureBDErr          error
	createIfErr          error
	ensureIfErr          error
	deleteIfErr          error
	createTapErr         error
	deleteTapErr         error
	graph                host.VPPGraph
	graphErr             error
	vppInterfaces        []host.VPPInterface
	interfacesErr        error
	captureStatus        host.PacketCaptureStatus
	captureErr           error
	captureFiles         []host.PacketCaptureFile
	captureData          map[string][]byte
	deletedCapture       string
	aclCounters          []host.ACLInterfaceCounters
	aclCounterErr        error
	fibRoutes            []host.FIBRoute
	fibErr               error
	traceResult          host.TraceResult
	traceErr             error
}

func (f *fakeNetworkProvisioner) AddInterfaceAddress(_ context.Context, name, cidr string) error {
	f.addedAddresses = append(f.addedAddresses, name+" "+cidr)
	return nil
}

func (f *fakeNetworkProvisioner) DelInterfaceAddress(_ context.Context, name, cidr string) error {
	f.deletedAddresses = append(f.deletedAddresses, name+" "+cidr)
	return nil
}

func (f *fakeNetworkProvisioner) EnableDHCPClient(_ context.Context, name string) error {
	f.enabledDHCP = append(f.enabledDHCP, name)
	return nil
}

func (f *fakeNetworkProvisioner) DisableDHCPClient(_ context.Context, name string) error {
	f.disabledDHCP = append(f.disabledDHCP, name)
	return nil
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

func (f *fakeNetworkProvisioner) SetInterfaceACLs(_ context.Context, swIfIndex uint32, ingress, egress []uint32) error {
	if f.interfaceACLs == nil {
		f.interfaceACLs = make(map[uint32][2][]uint32)
	}
	f.interfaceACLs[swIfIndex] = [2][]uint32{
		append([]uint32(nil), ingress...),
		append([]uint32(nil), egress...),
	}
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

func (f *fakeNetworkProvisioner) ResetInterfaceCounters(_ context.Context) error {
	f.resetCounters++
	return nil
}

func (f *fakeNetworkProvisioner) Identity() (string, error) { return "fake-vpp", nil }

func (f *fakeNetworkProvisioner) VPPGraphNodes(_ context.Context) ([]host.VPPGraphNode, error) {
	return []host.VPPGraphNode{
		{ID: "ip4-input", Label: "ip4-input"},
		{ID: "ip4-lookup", Label: "ip4-lookup"},
	}, nil
}

func (f *fakeNetworkProvisioner) VPPNodeGraph(_ context.Context) (host.VPPGraph, error) {
	if f.graphErr != nil {
		return host.VPPGraph{}, f.graphErr
	}
	return f.graph, nil
}

func (f *fakeNetworkProvisioner) StartTrace(_ context.Context, node string, count uint32) (host.TraceResult, error) {
	if f.traceErr != nil {
		return host.TraceResult{}, f.traceErr
	}
	f.traceResult = host.TraceResult{Node: node, Count: count, Raw: "trace started", CapturedAt: time.Now()}
	return f.traceResult, nil
}

func (f *fakeNetworkProvisioner) GetTrace(_ context.Context) (host.TraceResult, error) {
	if f.traceErr != nil {
		return host.TraceResult{}, f.traceErr
	}
	return host.TraceResult{Raw: "Packet 1\n  ip4-input", CapturedAt: time.Now()}, nil
}

func (f *fakeNetworkProvisioner) ClearTrace(_ context.Context) error { return f.traceErr }

func (f *fakeNetworkProvisioner) ACLInterfaceCounters(_ context.Context) ([]host.ACLInterfaceCounters, error) {
	if f.aclCounterErr != nil {
		return nil, f.aclCounterErr
	}
	return f.aclCounters, nil
}

func (f *fakeNetworkProvisioner) VPPInterfaces(_ context.Context) ([]host.VPPInterface, error) {
	if f.interfacesErr != nil {
		return nil, f.interfacesErr
	}
	return f.vppInterfaces, nil
}

func (f *fakeNetworkProvisioner) StartPacketCapture(_ context.Context, options host.PacketCaptureOptions) (host.PacketCaptureStatus, error) {
	if f.captureErr != nil {
		return host.PacketCaptureStatus{}, f.captureErr
	}
	f.captureStatus = host.PacketCaptureStatus{
		Active:    true,
		Interface: options.Interface,
		Direction: options.Direction,
		File:      "rookery-test.pcap",
	}
	return f.captureStatus, nil
}

func (f *fakeNetworkProvisioner) StopPacketCapture(_ context.Context) (host.PacketCaptureStatus, error) {
	if f.captureErr != nil {
		return host.PacketCaptureStatus{}, f.captureErr
	}
	f.captureStatus.Active = false
	return f.captureStatus, nil
}

func (f *fakeNetworkProvisioner) PacketCaptureStatus(_ context.Context) (host.PacketCaptureStatus, error) {
	if f.captureErr != nil {
		return host.PacketCaptureStatus{}, f.captureErr
	}
	return f.captureStatus, nil
}

func (f *fakeNetworkProvisioner) ListPacketCaptures() ([]host.PacketCaptureFile, error) {
	if f.captureErr != nil {
		return nil, f.captureErr
	}
	return f.captureFiles, nil
}

func (f *fakeNetworkProvisioner) OpenPacketCapture(name string) (io.ReadSeekCloser, host.PacketCaptureFile, error) {
	data, ok := f.captureData[name]
	if !ok {
		return nil, host.PacketCaptureFile{}, host.ErrCaptureNotFound
	}
	return readSeekNopCloser{bytes.NewReader(data)}, host.PacketCaptureFile{Name: name, Size: int64(len(data))}, nil
}

func (f *fakeNetworkProvisioner) DeletePacketCapture(name string) error {
	if f.captureStatus.Active && f.captureStatus.File == name {
		return host.ErrCaptureFileActive
	}
	if _, ok := f.captureData[name]; !ok {
		return host.ErrCaptureNotFound
	}
	delete(f.captureData, name)
	f.deletedCapture = name
	return nil
}

type readSeekNopCloser struct {
	*bytes.Reader
}

func (readSeekNopCloser) Close() error { return nil }

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

func (f *fakeNetworkProvisioner) CreatePipe(_ context.Context, bdA, bdB uint32) (host.PipeRuntime, error) {
	return host.PipeRuntime{Instance: 1, ParentSwIfIndex: 70, EndpointASwIfIndex: bdA, EndpointBSwIfIndex: bdB}, nil
}

func (f *fakeNetworkProvisioner) EnsurePipe(_ context.Context, instance, bdA, bdB uint32) (host.PipeRuntime, error) {
	f.ensuredPipes = append(f.ensuredPipes, [3]uint32{instance, bdA, bdB})
	return host.PipeRuntime{Instance: instance, ParentSwIfIndex: 70, EndpointASwIfIndex: bdA, EndpointBSwIfIndex: bdB}, nil
}

func (f *fakeNetworkProvisioner) DeletePipe(_ context.Context, _ uint32, parent uint32) error {
	f.deletedPipes = append(f.deletedPipes, parent)
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

func (f *fakeNetworkProvisioner) CreateTap(_ context.Context, hostName, _ string, _ uint32, _ uint32) (uint32, error) {
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

func (f *fakeNetworkProvisioner) EnsureHostTap(_ context.Context, name string, _ uint32, _ string) (uint32, error) {
	f.ensuredHostTaps = append(f.ensuredHostTaps, name)
	return uint32(400 + len(f.ensuredHostTaps)), nil
}

func (f *fakeNetworkProvisioner) DeleteHostTapByName(_ context.Context, name string) error {
	f.deletedHostTaps = append(f.deletedHostTaps, name)
	return nil
}

func (f *fakeNetworkProvisioner) CreateMemif(_ context.Context, hostName, _ string, _ uint32, _ net.HardwareAddr, _ uint32) (uint32, error) {
	if f.createMemifErr != nil {
		return 0, f.createMemifErr
	}
	f.createdMemifs = append(f.createdMemifs, hostName)
	return uint32(400 + len(f.createdMemifs)), nil
}

func (f *fakeNetworkProvisioner) EnsureMemif(ctx context.Context, hostName, socketPath string, ifID uint32, mac net.HardwareAddr, bdID uint32) (uint32, error) {
	for i, name := range f.createdMemifs {
		if name == hostName {
			return uint32(400 + i + 1), nil
		}
	}
	return f.CreateMemif(ctx, hostName, socketPath, ifID, mac, bdID)
}

func (f *fakeNetworkProvisioner) DeleteMemif(_ context.Context, swIfIndex uint32) error {
	if f.deleteMemifErr != nil {
		return f.deleteMemifErr
	}
	f.deletedMemifs = append(f.deletedMemifs, swIfIndex)
	return nil
}

func (f *fakeNetworkProvisioner) EnsureRouteTable(_ context.Context, _ uint32, _ bool, _ string) error {
	return nil
}

func (f *fakeNetworkProvisioner) DeleteRouteTable(_ context.Context, _ uint32, _ bool) error {
	return nil
}

func (f *fakeNetworkProvisioner) SetInterfaceRouteTable(_ context.Context, swIfIndex uint32, ipv6 bool, tableID uint32) error {
	f.interfaceRouteTables = append(f.interfaceRouteTables, fmt.Sprintf("%d/%t/%d", swIfIndex, ipv6, tableID))
	return nil
}
func (f *fakeNetworkProvisioner) InterfaceRouteTable(_ context.Context, _ uint32, _ bool) (uint32, error) {
	return 0, nil
}

func (f *fakeNetworkProvisioner) DelInterfaceIPv4(_ context.Context, _, _ string) error {
	return nil
}
func (f *fakeNetworkProvisioner) EnsureHostInterface(_ context.Context, nic string) (string, error) {
	return "host-" + nic, nil
}

func (f *fakeNetworkProvisioner) DeleteHostInterface(_ context.Context, _ string) error {
	return nil
}

func (f *fakeNetworkProvisioner) AddRoute(_ context.Context, _ uint32, _ bool, _, _, _ string, _ uint32, _ uint32) error {
	return nil
}

func (f *fakeNetworkProvisioner) AddRouteSpec(_ context.Context, _ host.RouteSpec) error {
	return nil
}

func (f *fakeNetworkProvisioner) DeleteRoute(_ context.Context, _ uint32, _ bool, _ string) error {
	return nil
}

func (f *fakeNetworkProvisioner) DumpFIBRoutes(_ context.Context, _ uint32, _ bool) ([]host.FIBRoute, error) {
	if f.fibErr != nil {
		return nil, f.fibErr
	}
	return f.fibRoutes, nil
}

func (f *fakeNetworkProvisioner) DeleteMemifByName(_ context.Context, _ string) error {
	if f.deleteMemifErr != nil {
		return f.deleteMemifErr
	}
	f.deletedMemifs = append(f.deletedMemifs, 0)
	return nil
}

type fakeVNFManager struct {
	running        map[int64]bool
	started        []int64
	startedHosts   []string
	stopped        []int64
	cleaned        []int64
	startErr       error
	startFailures  int
	stopErr        error
	leases         map[int64][]store.VNFLease
	leaseErr       error
	removedLeases  map[int64][]string
	removeErr      error
	bgpStatus      map[int64]host.BGPStatus
	dnsRecords     map[int64][]host.DNSRecordView
	sharedIfIDs    []uint32
	reloaded       []int64
	reloadErr      error
	reloadFailures int
}

func newFakeVNFManager() *fakeVNFManager {
	return &fakeVNFManager{
		running:       make(map[int64]bool),
		leases:        make(map[int64][]store.VNFLease),
		removedLeases: make(map[int64][]string),
		bgpStatus:     make(map[int64]host.BGPStatus),
		dnsRecords:    make(map[int64][]host.DNSRecordView),
	}
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

func (f *fakeVNFManager) StartMemif(_ context.Context, vnf store.VNF, socketPath string, _ uint32) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.running[vnf.ID] = true
	f.started = append(f.started, vnf.ID)
	f.startedHosts = append(f.startedHosts, socketPath)
	return nil
}

func (f *fakeVNFManager) StartShared(_ context.Context, vnf store.VNF, socketPath string, ifID uint32, address, ipv6Address string) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.sharedIfIDs = append(f.sharedIfIDs, ifID)
	if f.startFailures > 0 {
		f.startFailures--
		return errors.New("simulated VNF start failure")
	}
	f.running[vnf.ID] = true
	f.started = append(f.started, vnf.ID)
	f.startedHosts = append(f.startedHosts, socketPath+"@"+address)
	return nil
}

func (f *fakeVNFManager) Stop(_ context.Context, vnf store.VNF, _ bool) error {
	if f.stopErr != nil {
		return f.stopErr
	}
	delete(f.running, vnf.ID)
	f.stopped = append(f.stopped, vnf.ID)
	return nil
}

func (f *fakeVNFManager) ReloadVNF(_ context.Context, vnf store.VNF) error {
	f.reloaded = append(f.reloaded, vnf.ID)
	if f.reloadFailures > 0 {
		f.reloadFailures--
		return errors.New("simulated VNF reload failure")
	}
	return f.reloadErr
}

func (f *fakeVNFManager) ResetRuntime(_ context.Context) error {
	f.running = make(map[int64]bool)
	return nil
}

func (f *fakeVNFManager) Running(_ context.Context, vnf store.VNF) bool {
	return f.running[vnf.ID]
}

func (f *fakeVNFManager) Leases(_ context.Context, vnfID int64) ([]store.VNFLease, error) {
	if f.leaseErr != nil {
		return nil, f.leaseErr
	}
	return f.leases[vnfID], nil
}

func (f *fakeVNFManager) RemoveLeases(_ context.Context, vnfID int64, clientIDs []string) (int, error) {
	if f.removeErr != nil {
		return 0, f.removeErr
	}
	f.removedLeases[vnfID] = append(f.removedLeases[vnfID], clientIDs...)
	return len(clientIDs), nil
}

func (f *fakeVNFManager) DNSRecords(_ context.Context, vnfID int64) ([]host.DNSRecordView, error) {
	return f.dnsRecords[vnfID], nil
}

func (f *fakeVNFManager) BGPStatus(_ context.Context, vnfID int64) (host.BGPStatus, error) {
	return f.bgpStatus[vnfID], nil
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
	for i, id := range f.bvis {
		if id == networkID {
			return fmt.Sprintf("bvi%d", networkID), uint32(900 + i + 1), nil
		}
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
	entry := name + " " + cidr
	for _, existing := range f.addedIPs {
		if existing == entry {
			return nil
		}
	}
	f.addedIPs = append(f.addedIPs, entry)
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
				Name:            "rookery",
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
	logger := zerolog.Nop()
	apiServer := api.New(st, ready, provisioner, controller, networkProvisioner, vnfManager, natManager, logger)
	// Network creation prepares a shared VNF memif socket. Keep that path in
	// the test sandbox instead of the production /run/rookery directory, which
	// is not writable on GitHub Actions runners.
	apiServer.ConfigureMemifDHCP(false, t.TempDir())
	lifecycleService := lifecycle.New(st, apiServer, apiServer.ResourceMutex())
	gw, err := gateway.New(st, ready,
		gateway.WithDiskProvisioner(provisioner),
		gateway.WithVMController(controller),
		gateway.WithInterfaceCounters(networkProvisioner),
		gateway.WithACLRuntime(networkProvisioner),
		gateway.WithInterfaceRuntime(networkProvisioner),
		gateway.WithHostWriter(networkProvisioner),
		gateway.WithHostTapWriter(lifecycleService),
		gateway.WithNetworkRuntime(networkProvisioner),
		gateway.WithDiagnosticsRuntime(networkProvisioner),
		gateway.WithVNFRuntime(vnfManager),
		gateway.WithVNFWriter(lifecycleService),
		gateway.WithNetworkWriter(lifecycleService),
		gateway.WithVNFStatusProvider(apiServer),
		gateway.WithDiskMutex(apiServer.ResourceMutex()),
		gateway.WithVMMutex(apiServer.RunMutex()),
	)
	if err != nil {
		t.Fatalf("create gateway: %v", err)
	}
	t.Cleanup(gw.Close)
	return &testAPI{
		handler:     api.CutoverHandler(gw.Handler, apiServer.NativeHandler()),
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
	return createNetworkAPICIDR(t, handler, name, "10.20.0.0/24", "fd00:20::/64")
}

func createNetworkAPICIDR(t *testing.T, handler http.Handler, name, ipv4CIDR, ipv6CIDR string) store.Network {
	t.Helper()

	recorder := do(t, handler, http.MethodPost, "/api/networks", map[string]any{
		"name": name, "ipv4_cidr": ipv4CIDR, "ipv6_cidr": ipv6CIDR,
	})
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

// createStoppedAPI creates a VM (which starts automatically) and stops it for
// tests that need a stopped VM to change disks or interfaces.
func createStoppedAPI(t *testing.T, handler http.Handler, name string) store.VM {
	t.Helper()

	vm := createAPI(t, handler, name)
	recorder := do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/stop", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("stop %q status = %d, body = %s", name, recorder.Code, recorder.Body.String())
	}
	return decode[store.VM](t, recorder)
}

func TestRouteTableAssignment(t *testing.T) {
	env := newTestAPI(t)
	network := createNetworkAPI(t, env.handler, "routes-net")
	iface := createInterfaceAPI(t, env.handler, "vm-routes", network.ID)

	recorder := do(t, env.handler, http.MethodPost, "/api/route-tables", map[string]any{
		"name":     "tenant-blue",
		"table_id": 42,
		"ipv6":     true,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create route table status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	table := decode[store.RouteTable](t, recorder)
	if table.TableID != 42 || !table.IPv6 {
		t.Fatalf("created route table = %+v", table)
	}

	recorder = do(t, env.handler, http.MethodGet, "/api/route-tables", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list route tables status = %d", recorder.Code)
	}
	tables := decode[[]store.RouteTable](t, recorder)
	if len(tables) < 3 {
		t.Fatalf("route tables = %+v, want default v4/v6 plus custom", tables)
	}

	recorder = do(t, env.handler, http.MethodPut, fmt.Sprintf("/api/interfaces/%d/route-tables", iface.ID), map[string]any{
		"ipv4_table_id": 0,
		"ipv6_table_id": 42,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("assign route table status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	assigned := decode[store.VirtualInterface](t, recorder)
	if assigned.IPv6TableID != 42 || assigned.IPv6TableName != "tenant-blue" {
		t.Fatalf("assigned interface = %+v", assigned)
	}

	recorder = do(t, env.handler, http.MethodPost, fmt.Sprintf("/api/route-tables/%d/routes", table.ID), map[string]any{
		"ipv6":         true,
		"prefix":       "2001:db8:10::/64",
		"description":  "tenant route",
		"next_hop":     "2001:db8::1",
		"interface_id": iface.ID,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create route status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	route := decode[store.Route](t, recorder)
	if route.TableID != 42 || !route.IPv6 || route.InterfaceID == nil || *route.InterfaceID != iface.ID || route.Description != "tenant route" {
		t.Fatalf("created route = %+v", route)
	}

	recorder = do(t, env.handler, http.MethodPut, fmt.Sprintf("/api/routes/%d", route.ID), map[string]any{
		"prefix":      "2001:db8:10::/64",
		"description": "updated tenant route",
		"next_hop":    "2001:db8::2",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("update route status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	updatedRoute := decode[store.Route](t, recorder)
	if updatedRoute.Description != "updated tenant route" || updatedRoute.NextHop != "2001:db8::2" {
		t.Fatalf("updated route = %+v", updatedRoute)
	}

	recorder = do(t, env.handler, http.MethodPost, fmt.Sprintf("/api/route-tables/%d/routes", table.ID), map[string]any{
		"ipv6":             true,
		"prefix":           "2001:db8:11::/64",
		"next_hop":         "2001:db8::1",
		"egress_interface": "bvi1",
		"preference":       5,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create egress route status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	egressRoute := decode[store.Route](t, recorder)
	if egressRoute.EgressInterface != "bvi1" || egressRoute.Preference == nil || *egressRoute.Preference != 5 || egressRoute.InterfaceID != nil {
		t.Fatalf("egress route = %+v", egressRoute)
	}

	recorder = do(t, env.handler, http.MethodGet, fmt.Sprintf("/api/route-tables/%d/routes?ipv6=true", table.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list routes status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	routes := decode[[]store.Route](t, recorder)
	if len(routes) != 2 || routes[0].Prefix != "2001:db8:10::/64" {
		t.Fatalf("routes = %+v", routes)
	}

	recorder = do(t, env.handler, http.MethodDelete, fmt.Sprintf("/api/route-tables/%d", table.ID), nil)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("delete assigned route table status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, env.handler, http.MethodPut, fmt.Sprintf("/api/interfaces/%d/route-tables", iface.ID), map[string]any{
		"ipv4_table_id": 0,
		"ipv6_table_id": 0,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("clear route table status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, env.handler, http.MethodDelete, fmt.Sprintf("/api/route-tables/%d", table.ID), nil)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("delete route table with routes status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, env.handler, http.MethodDelete, fmt.Sprintf("/api/routes/%d", route.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete route status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, env.handler, http.MethodDelete, fmt.Sprintf("/api/routes/%d", egressRoute.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete egress route status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, env.handler, http.MethodDelete, fmt.Sprintf("/api/route-tables/%d", table.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete route table status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestDefaultRouteTableRoutes(t *testing.T) {
	env := newTestAPI(t)
	recorder := do(t, env.handler, http.MethodPost, "/api/route-tables/0/routes", map[string]any{
		"ipv6":     false,
		"prefix":   "192.0.2.0/24",
		"next_hop": "192.0.2.1",
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create default route status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	route := decode[store.Route](t, recorder)
	if route.TableID != 0 || route.IPv6 || route.Prefix != "192.0.2.0/24" {
		t.Fatalf("created default route = %+v", route)
	}

	recorder = do(t, env.handler, http.MethodGet, "/api/route-tables/0/routes?ipv6=false", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list default routes status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	routes := decode[[]store.Route](t, recorder)
	if len(routes) != 1 || routes[0].ID != route.ID {
		t.Fatalf("default routes = %+v", routes)
	}

	recorder = do(t, env.handler, http.MethodDelete, fmt.Sprintf("/api/routes/%d", route.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete default route status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestListFIBRoutes(t *testing.T) {
	env := newTestAPI(t)
	env.networks.fibRoutes = []host.FIBRoute{{
		Prefix: "10.20.0.0/24", TableID: 0, Source: 4, SourceName: "interface", Dynamic: true, StatsIndex: 7,
		Paths: []host.FIBPath{{
			SwIfIndex: 3, InterfaceName: "vhost-test", Type: "NORMAL", Weight: 1, Proto: "FIB_API_PATH_NH_PROTO_IP4", NextHop: "10.20.0.1",
		}},
	}}
	recorder := do(t, env.handler, http.MethodGet, "/api/route-tables/0/fib?ipv6=false", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list FIB routes status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	routes := decode[[]host.FIBRoute](t, recorder)
	if len(routes) != 1 || !routes[0].Dynamic || routes[0].SourceName != "interface" {
		t.Fatalf("FIB routes = %+v", routes)
	}
	if len(routes[0].Paths) != 1 || routes[0].Paths[0].InterfaceName != "vhost-test" || routes[0].Paths[0].NextHop != "10.20.0.1" {
		t.Fatalf("FIB paths = %+v", routes[0].Paths)
	}
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
	recorder = do(t, env.handler, http.MethodPost, "/api/acls", map[string]any{
		"name":  "admin-acl",
		"title": "Admin traffic",
		"rules": []map[string]any{
			{"action": "permit", "protocol": "tcp", "source": "10.0.0.0/8", "destination": "0.0.0.0/0", "destination_port_start": 22, "destination_port_end": 22},
		},
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create second ACL status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	adminACL := decode[store.ACL](t, recorder)

	recorder = do(t, env.handler, http.MethodPut, fmt.Sprintf("/api/interfaces/%d/acls", iface.ID), map[string]any{
		"ingress_acls": []string{"web-acl", "admin-acl"},
		"egress_acls":  []string{"admin-acl"},
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("assign ACLs status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	assigned := decode[store.VirtualInterface](t, recorder)
	if assigned.IngressACLName != "web-acl" || assigned.EgressACLName != "admin-acl" {
		t.Fatalf("assigned interface = %+v", assigned)
	}
	var lists struct {
		IngressACLs []string `json:"ingress_acls"`
		EgressACLs  []string `json:"egress_acls"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &lists); err != nil {
		t.Fatalf("decode assigned ACL lists: %v", err)
	}
	if len(lists.IngressACLs) != 2 || lists.IngressACLs[0] != "web-acl" || lists.IngressACLs[1] != "admin-acl" ||
		len(lists.EgressACLs) != 1 || lists.EgressACLs[0] != "admin-acl" {
		t.Fatalf("assigned ACL lists = %+v", lists)
	}
	if len(env.networks.interfaceACLs) != 1 {
		t.Fatalf("VPP interface ACLs = %+v", env.networks.interfaceACLs)
	}
	if refs := env.networks.interfaceACLs[iface.SwIfIndex]; len(refs[0]) != 2 || len(refs[1]) != 1 {
		t.Fatalf("VPP interface ACL refs = %+v", refs)
	}

	recorder = do(t, env.handler, http.MethodDelete, fmt.Sprintf("/api/acls/%d", acl.ID), nil)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("delete assigned ACL status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, env.handler, http.MethodDelete, fmt.Sprintf("/api/acls/%d", adminACL.ID), nil)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("delete second assigned ACL status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, env.handler, http.MethodPut, fmt.Sprintf("/api/interfaces/%d/acls", iface.ID), map[string]any{
		"ingress_acls": []string{},
		"egress_acls":  []string{},
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("clear ACL status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, env.handler, http.MethodDelete, fmt.Sprintf("/api/acls/%d", acl.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete ACL status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, env.handler, http.MethodDelete, fmt.Sprintf("/api/acls/%d", adminACL.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete second ACL status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.networks.deletedACLs) != 2 {
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
	if disk.VG != "vg0" || disk.ThinPool != "rookery" || disk.Device != "/dev/vg0/web-01-root" {
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
	if created.Status != store.StatusRunning || created.VCPUs != 1 || created.MemoryMB != 512 {
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
	if body := strings.TrimSpace(recorder.Body.String()); body != "" && body != "{}" {
		t.Fatalf("delete body = %q, want empty or {}", body)
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
	if created.Status != store.StatusRunning {
		t.Fatalf("created status = %q, want running", created.Status)
	}

	// Disk changes require a stopped VM; stopping is always explicit.
	recorder = do(t, handler, http.MethodPost, "/api/vms/"+created.ID+"/stop", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("stop multi status = %d, body = %s", recorder.Code, recorder.Body.String())
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

	recorder = do(t, handler, http.MethodPost, "/api/vms/"+other.ID+"/stop", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("stop other status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

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
		{Name: "legacy-root", SizeGB: 20, SizeBytes: 20 << 30},
		{Name: "already-registered", SizeGB: 10, SizeBytes: 10 << 30},
		{Name: "legacy-data", SizeGB: 1, SizeBytes: 512 << 20},
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/storage-backends/%d/importable-disks", backend.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list importable status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	importable := decode[[]struct {
		Name               string `json:"name"`
		SizeGB             int    `json:"size_gb"`
		SizeBytes          uint64 `json:"size_bytes"`
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
	if importable[1].Name != "legacy-data" || importable[1].SizeGB != 1 ||
		importable[1].SizeBytes != (512<<20) || importable[1].Registered {
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
		imported[0].SizeBytes != (20<<30) ||
		imported[0].StorageBackendID == nil || *imported[0].StorageBackendID != backend.ID ||
		imported[0].VG != "vg-import" || imported[0].ThinPool != "import-pool" {
		t.Fatalf("imported root = %+v", imported[0])
	}
	if imported[1].Name != "legacy-data" || imported[1].SizeGB != 1 || imported[1].SizeBytes != (512<<20) {
		t.Fatalf("imported data = %+v", imported[1])
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
	if created.VG != "vg0" || created.ThinPool != "rookery" || created.Device != "/dev/vg0/vm01-root" {
		t.Fatalf("created disk location = %+v", created)
	}
	if want := fmt.Sprintf("/api/disks/%d", created.ID); recorder.Header().Get("Location") != want {
		t.Fatalf("Location = %q, want %q", recorder.Header().Get("Location"), want)
	}
	if len(env.provisioner.created) != 1 {
		t.Fatalf("provisioner calls = %+v", env.provisioner.created)
	}
	if call := env.provisioner.created[0]; call.VG != "vg0" || call.ThinPool != "rookery" || call.Name != "vm01-root" || call.SizeGB != 10 {
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
	if body := strings.TrimSpace(recorder.Body.String()); body != "" && body != "{}" {
		t.Fatalf("delete body = %q, want empty or {}", body)
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
	vm := createStoppedAPI(t, handler, "vm01")

	recorder := do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/start", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("start status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	started := decode[store.VM](t, recorder)
	if started.Status != store.StatusRunning {
		t.Fatalf("start status = %q, want running", started.Status)
	}
	if !env.vms.Running(vm.ID) || len(env.vms.started) != 2 || env.vms.started[1] != vm.ID {
		t.Fatalf("controller start state = running %v, calls %v", env.vms.Running(vm.ID), env.vms.started)
	}
	if got, err := env.store.GetVM(context.Background(), vm.ID); err != nil || got.DesiredState != store.DesiredRunning {
		t.Fatalf("desired state after start = %q, err = %v", got.DesiredState, err)
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
	if env.vms.Running(vm.ID) || len(env.vms.stopped) != 2 || env.vms.stopped[1] != vm.ID {
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
	vm := createStoppedAPI(t, handler, "vm01")

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
	vm := createStoppedAPI(t, handler, "vm01")

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
	vm := createStoppedAPI(t, handler, "vm01")

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
	if len(env.vms.stopped) != 2 || env.vms.stopped[1] != vm.ID {
		t.Fatalf("stop calls = %v, want the delete stop for %s", env.vms.stopped, vm.ID)
	}
}

func TestVMResetAndCtrlAltDel(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	vm := createStoppedAPI(t, handler, "vm01")

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
	vm := createStoppedAPI(t, handler, "vm01")

	recorder := do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/start", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("start status = %d", recorder.Code)
	}

	env.vms.resetErr = qemu.ErrMonitorUnavailable
	recorder = do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/reset", nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("reset fallback status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.vms.stopped) != 2 || env.vms.stopped[1] != vm.ID {
		t.Fatalf("stop calls = %v", env.vms.stopped)
	}
	if len(env.vms.started) != 3 || env.vms.started[2] != vm.ID {
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
	vm := createStoppedAPI(t, handler, "vm01")

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

func TestRouteDefaultLookupTableName(t *testing.T) {
	ctx := context.Background()
	env := newTestAPI(t)
	recorder := do(t, env.handler, http.MethodPost, "/api/route-tables/0/routes", map[string]any{
		"ipv6":              false,
		"prefix":            "10.10.1.0/24",
		"ipv4_lookup_table": "default",
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create route status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	route := decode[store.Route](t, recorder)
	if route.IPv4LookupTable == nil || *route.IPv4LookupTable != "default" {
		t.Fatalf("default lookup table = %+v", route.IPv4LookupTable)
	}
	if route.Preference != nil || route.Count != nil || route.NextHopTable != nil || route.Weight != nil || route.UDPEncap != nil {
		t.Fatalf("unexpected optional route fields = %+v", route)
	}

	recorder = do(t, env.handler, http.MethodPut, fmt.Sprintf("/api/routes/%d", route.ID), map[string]any{
		"prefix":   "10.10.1.0/24",
		"next_hop": "10.10.1.1",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("update route status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	updated := decode[store.Route](t, recorder)
	if updated.IPv4LookupTable != nil {
		t.Fatalf("lookup table was not cleared: %+v", updated.IPv4LookupTable)
	}

	if _, err := env.store.CreateRouteTable(ctx, store.RouteTable{Name: "lookup-v4", TableID: 600}); err != nil {
		t.Fatal(err)
	}
	recorder = do(t, env.handler, http.MethodPost, "/api/route-tables/0/routes", map[string]any{
		"ipv6":              false,
		"prefix":            "10.11.0.0/24",
		"ipv4_lookup_table": "lookup-v4",
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create named lookup route status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	named := decode[store.Route](t, recorder)
	if named.IPv4LookupTable == nil || *named.IPv4LookupTable != "lookup-v4" {
		t.Fatalf("named lookup table = %+v", named.IPv4LookupTable)
	}
	recorder = do(t, env.handler, http.MethodPost, "/api/route-tables/0/routes", map[string]any{
		"ipv6":              false,
		"prefix":            "10.12.0.0/24",
		"ipv4_lookup_table": "missing-table",
	})
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing lookup table status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestReconcileVPPInterfaceRouteTables(t *testing.T) {
	env := newTestAPI(t)
	ctx := context.Background()
	network := createNetworkAPI(t, env.handler, "lab")
	tap, err := env.store.CreateNetworkHostTap(ctx, store.NetworkHostTap{
		NetworkID: network.ID, Name: "tap-lab", SwIfIndex: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	ipv4, err := env.store.CreateRouteTable(ctx, store.RouteTable{Name: "lab-v4", TableID: 500})
	if err != nil {
		t.Fatal(err)
	}
	ipv6, err := env.store.CreateRouteTable(ctx, store.RouteTable{Name: "lab-v6", TableID: 501, IPv6: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.SetVPPInterfaceRouteTables(ctx, tap.Name, ipv4.TableID, ipv6.TableID); err != nil {
		t.Fatal(err)
	}
	env.networks.vppInterfaces = []host.VPPInterface{{Name: tap.Name, SwIfIndex: 30}}
	env.networks.interfaceRouteTables = nil

	if err := env.server.ReconcileVPPInterfaceRouteTables(ctx); err != nil {
		t.Fatalf("reconcile interface route tables: %v", err)
	}
	want := []string{"30/false/500", "30/true/501"}
	if len(env.networks.interfaceRouteTables) != len(want) {
		t.Fatalf("interface route table calls = %+v", env.networks.interfaceRouteTables)
	}
	for i := range want {
		if env.networks.interfaceRouteTables[i] != want[i] {
			t.Fatalf("interface route table calls = %+v, want %+v", env.networks.interfaceRouteTables, want)
		}
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

func TestReconcileNetworkPipes(t *testing.T) {
	env := newTestAPI(t)
	networkA := createNetworkAPI(t, env.handler, "lab-a")
	networkB := createNetworkAPICIDR(t, env.handler, "lab-b", "10.21.0.0/24", "fd00:21::/64")
	created := do(t, env.handler, http.MethodPost, "/api/network-pipes", map[string]any{
		"network_a_id": networkB.ID, "network_b_id": networkA.ID,
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create pipe status = %d, body = %s", created.Code, created.Body.String())
	}
	pipe := decode[store.NetworkPipe](t, created)
	if pipe.NetworkAID != networkA.ID || pipe.NetworkBID != networkB.ID || pipe.Instance != 1 || pipe.ParentSwIfIndex != 70 {
		t.Fatalf("created pipe = %+v", pipe)
	}

	if err := env.server.ReconcileNetworkPipes(context.Background()); err != nil {
		t.Fatalf("reconcile network pipes: %v", err)
	}
	if len(env.networks.ensuredPipes) != 1 {
		t.Fatalf("ensured pipes = %+v", env.networks.ensuredPipes)
	}
	call := env.networks.ensuredPipes[0]
	if call[0] != 1 || call[1] != networkA.BridgeDomainID || call[2] != networkB.BridgeDomainID {
		t.Fatalf("ensured pipe = %+v, networks = %d/%d", call, networkA.BridgeDomainID, networkB.BridgeDomainID)
	}
	updated, err := env.store.GetNetworkPipe(context.Background(), pipe.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ParentSwIfIndex != 70 || updated.EndpointASwIfIndex != networkA.BridgeDomainID || updated.EndpointBSwIfIndex != networkB.BridgeDomainID {
		t.Fatalf("updated pipe = %+v", updated)
	}

	deleted := do(t, env.handler, http.MethodDelete, fmt.Sprintf("/api/network-pipes/%d", pipe.ID), nil)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete pipe status = %d, body = %s", deleted.Code, deleted.Body.String())
	}
	if len(env.networks.deletedPipes) != 1 || env.networks.deletedPipes[0] != 70 {
		t.Fatalf("deleted pipe runtimes = %+v", env.networks.deletedPipes)
	}
}

func TestHostTapLifecycle(t *testing.T) {
	env := newTestAPI(t)
	network := createNetworkAPI(t, env.handler, "lab")

	for _, address := range []string{"192.168.1.10/24", "10.20.0.2", "10.20.0.0/24", "10.20.0.255/24", "10.20.0.50/16"} {
		recorder := do(t, env.handler, http.MethodPost, "/api/host/taps", map[string]any{
			"network_id":   network.ID,
			"ipv4_address": address,
		})
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("invalid address %q status = %d, body = %s", address, recorder.Code, recorder.Body.String())
		}
	}

	recorder := do(t, env.handler, http.MethodPost, "/api/host/taps", map[string]any{
		"network_id":   network.ID,
		"name":         "hostnet1",
		"ipv4_address": "10.20.0.50",
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create host tap status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	created := decode[store.NetworkHostTap](t, recorder)
	if created.ID == 0 || created.NetworkID != network.ID || created.Name != "hostnet1" ||
		created.IPv4Address != "10.20.0.50/24" || created.SwIfIndex == 0 {
		t.Fatalf("created host tap = %+v", created)
	}
	if len(env.networks.ensuredHostTaps) != 1 || env.networks.ensuredHostTaps[0] != "hostnet1" {
		t.Fatalf("ensured host taps = %v", env.networks.ensuredHostTaps)
	}

	taps := decode[[]store.NetworkHostTap](t, do(t, env.handler, http.MethodGet, "/api/host/taps", nil))
	if len(taps) != 1 || taps[0].ID != created.ID {
		t.Fatalf("host taps = %+v", taps)
	}
	filtered := decode[[]store.NetworkHostTap](t, do(t, env.handler, http.MethodGet,
		fmt.Sprintf("/api/host/taps?network_id=%d", network.ID), nil))
	if len(filtered) != 1 || filtered[0].ID != created.ID {
		t.Fatalf("filtered host taps = %+v", filtered)
	}

	if err := env.server.ReconcileNetworkHostTaps(context.Background()); err != nil {
		t.Fatalf("reconcile host taps: %v", err)
	}
	taps = decode[[]store.NetworkHostTap](t, do(t, env.handler, http.MethodGet, "/api/host/taps", nil))
	if len(taps) != 1 || taps[0].SwIfIndex != 402 {
		t.Fatalf("reconciled host taps = %+v", taps)
	}

	recorder = do(t, env.handler, http.MethodDelete, fmt.Sprintf("/api/host/taps/%d", created.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete host tap status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.networks.deletedHostTaps) != 1 || env.networks.deletedHostTaps[0] != "hostnet1" {
		t.Fatalf("deleted host taps = %v", env.networks.deletedHostTaps)
	}
	taps = decode[[]store.NetworkHostTap](t, do(t, env.handler, http.MethodGet, "/api/host/taps", nil))
	if len(taps) != 0 {
		t.Fatalf("host taps after delete = %+v", taps)
	}

	recorder = do(t, env.handler, http.MethodPost, "/api/host/taps", map[string]any{
		"network_id":   network.ID,
		"ipv4_address": "10.20.0.60",
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create default host tap status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	defaultTap := decode[store.NetworkHostTap](t, recorder)
	if defaultTap.Name != fmt.Sprintf("hostnet%d", network.ID) {
		t.Fatalf("default host tap name = %q", defaultTap.Name)
	}
	recorder = do(t, env.handler, http.MethodDelete, fmt.Sprintf("/api/networks/%d", network.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete network status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	taps = decode[[]store.NetworkHostTap](t, do(t, env.handler, http.MethodGet, "/api/host/taps", nil))
	if len(taps) != 0 {
		t.Fatalf("host taps after network delete = %+v", taps)
	}
}

func TestRunInfrastructureReconcilerAutostartsVMs(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	ctx := context.Background()

	autostart := createAPI(t, handler, "auto-start")
	manual := createAPI(t, handler, "stays-stopped")

	recorder := do(t, handler, http.MethodPost, "/api/vms/"+manual.ID+"/stop", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("stop VM status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	created, err := env.store.GetVM(ctx, autostart.ID)
	if err != nil {
		t.Fatalf("get auto-start VM: %v", err)
	}
	if created.DesiredState != store.DesiredRunning {
		t.Fatalf("desired state after start = %q, want running", created.DesiredState)
	}
	stopped, err := env.store.GetVM(ctx, manual.ID)
	if err != nil {
		t.Fatalf("get stopped VM: %v", err)
	}
	if stopped.DesiredState != store.DesiredStopped {
		t.Fatalf("desired state after stop = %q, want stopped", stopped.DesiredState)
	}

	// Simulate a host reboot: every QEMU process is gone, but the records keep
	// their desired state.
	delete(env.vms.running, autostart.ID)
	delete(env.vms.running, manual.ID)

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		env.server.RunInfrastructureReconciler(runCtx)
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, err := env.store.GetVM(ctx, autostart.ID)
		if err == nil && got.Status == store.StatusRunning && env.vms.Running(autostart.ID) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("infrastructure reconciler did not stop")
	}

	if !env.vms.Running(autostart.ID) {
		t.Fatal("VM with desired state running was not auto-started")
	}
	if env.vms.Running(manual.ID) {
		t.Fatal("explicitly stopped VM was auto-started")
	}
	restarted, err := env.store.GetVM(ctx, autostart.ID)
	if err != nil {
		t.Fatalf("get auto-started VM: %v", err)
	}
	if restarted.Status != store.StatusRunning {
		t.Fatalf("auto-started VM status = %q, want running", restarted.Status)
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

func TestReconcileHostInterfaceRecords(t *testing.T) {
	env := newTestAPI(t)
	ctx := context.Background()
	if _, err := env.store.UpsertHostInterfaceRecord(ctx, store.HostInterfaceRecord{
		NIC: "enp1s0", VPPName: "host-enp1s0", Purpose: "manual",
	}); err != nil {
		t.Fatalf("seed host interface: %v", err)
	}
	env.networks.vppInterfaces = []host.VPPInterface{{Name: "host-enp1s0", SwIfIndex: 77, Up: true}}
	if err := env.server.ReconcileHostInterfaces(ctx); err != nil {
		t.Fatalf("reconcile host interfaces: %v", err)
	}
	if !slices.Contains(env.nat.hostIfs, "enp1s0") {
		t.Fatalf("host interfaces ensured = %v, want enp1s0", env.nat.hostIfs)
	}
	record, err := env.store.GetHostInterfaceRecord(ctx, "enp1s0")
	if err != nil {
		t.Fatal(err)
	}
	if record.SwIfIndex != 77 {
		t.Fatalf("sw_if_index = %d, want 77", record.SwIfIndex)
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
	if vm.Status != store.StatusRunning {
		t.Fatalf("created vm status = %q, want running", vm.Status)
	}

	// Interface changes require a stopped VM; stopping is always explicit.
	recorder = do(t, handler, http.MethodPost, "/api/vms/"+vm.ID+"/stop", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("stop vm status = %d, body = %s", recorder.Code, recorder.Body.String())
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
		"name":                  vm.Name,
		"interface_ids":         []int64{},
		"replace_interface_ids": true,
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

func TestNetworkVRFsAppearInRouteTables(t *testing.T) {
	env := newTestAPI(t)
	network := createNetworkAPI(t, env.handler, "lab")
	recorder := do(t, env.handler, http.MethodGet, "/api/route-tables", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list route tables status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	tables := decode[[]struct {
		Name      string `json:"name"`
		TableID   uint32 `json:"table_id"`
		IPv6      bool   `json:"ipv6"`
		NetworkID int64  `json:"network_id"`
		Managed   bool   `json:"managed"`
	}](t, recorder)
	wantTable := uint32(100000 + network.ID)
	seenV4, seenV6 := false, false
	for _, table := range tables {
		if table.TableID != wantTable || table.NetworkID != network.ID {
			continue
		}
		if !table.Managed {
			t.Fatalf("network route table is not managed: %+v", table)
		}
		if table.IPv6 {
			seenV6 = true
		} else {
			seenV4 = true
		}
	}
	if !seenV4 || !seenV6 {
		t.Fatalf("network route tables missing: v4=%v v6=%v tables=%+v", seenV4, seenV6, tables)
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

	recorder := do(t, handler, http.MethodPost, "/api/networks", map[string]any{"name": "LAB", "ipv4_cidr": "10.99.0.0/24", "ipv6_cidr": "fd00:99::/64"})
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

	vm := createStoppedAPI(t, handler, "vm01")
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
	recorder := do(t, handler, http.MethodPost, "/api/networks", map[string]any{"name": "lab", "ipv4_cidr": "10.20.0.0/24", "ipv6_cidr": "fd00:20::/64"})
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
	vm := createStoppedAPI(t, handler, "vm01")

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
		{name: "dhcp vnf method not allowed", method: http.MethodPatch, target: "/api/dhcp-vnfs/1", status: http.StatusMethodNotAllowed},
		{name: "dhcp vnf leases method not allowed", method: http.MethodPost, target: "/api/dhcp-vnfs/1/leases", status: http.StatusMethodNotAllowed},
		{name: "dhcp vnf restart method not allowed", method: http.MethodGet, target: "/api/dhcp-vnfs/1/restart", status: http.StatusMethodNotAllowed},
		{name: "dns vnf child method not allowed", method: http.MethodPatch, target: "/api/dns-vnfs/1/records", status: http.StatusMethodNotAllowed},
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
	if !status.ThinPool.Ready || status.ThinPool.VG != "vg0" || status.ThinPool.Name != "rookery" {
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
					Detail: "thin pool vg0/rookery does not exist",
				},
				VG:   "vg0",
				Name: "rookery",
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

	recorder = do(t, handler, http.MethodPost, "/api/networks", map[string]any{"name": "lab", "ipv4_cidr": "10.20.0.0/24", "ipv6_cidr": "fd00:20::/64"})
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
	recorder = do(t, handler, http.MethodPost, "/api/dhcp-vnfs", map[string]any{
		"name":        "lab-dhcp",
		"network_id":  network.ID,
		"family":      store.VNFTypeDHCPv4,
		"enabled":     true,
		"gateway":     "10.20.0.1/24",
		"range_start": "10.20.0.100",
		"range_end":   "10.20.0.200",
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
				Name:            "rookery",
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

func TestVPPNodeGraph(t *testing.T) {
	env := newTestAPI(t)
	env.networks.graph = host.VPPGraph{
		Nodes: []host.VPPGraphNode{
			{ID: "ip4-input", Label: "ip4-input", Width: 2.5, Fill: 3},
			{ID: "ip4-lookup", Label: "ip4-lookup", Width: 1.5},
		},
		Edges: []host.VPPGraphEdge{{Source: "ip4-input", Target: "ip4-lookup"}},
	}
	recorder := do(t, env.handler, http.MethodGet, "/api/troubleshoot/vpp-graph", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("vpp graph status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	graph := decode[host.VPPGraph](t, recorder)
	if len(graph.Nodes) != 2 || len(graph.Edges) != 1 || graph.Edges[0].Target != "ip4-lookup" {
		t.Fatalf("vpp graph = %+v", graph)
	}

	env.networks.graphErr = errors.New("vppctl failed")
	recorder = do(t, env.handler, http.MethodGet, "/api/troubleshoot/vpp-graph", nil)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("vpp graph error status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestPacketTrace(t *testing.T) {
	env := newTestAPI(t)
	recorder := do(t, env.handler, http.MethodGet, "/api/troubleshoot/trace/nodes", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("trace nodes status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	nodes := decode[[]string](t, recorder)
	if len(nodes) != 2 || nodes[0] != "ip4-input" || nodes[1] != "ip4-lookup" {
		t.Fatalf("trace nodes = %+v", nodes)
	}

	recorder = do(t, env.handler, http.MethodPost, "/api/troubleshoot/trace", map[string]any{
		"node":  "ip4-input",
		"count": 25,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("start trace status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	started := decode[host.TraceResult](t, recorder)
	if started.Node != "ip4-input" || started.Count != 25 || started.Raw != "trace started" {
		t.Fatalf("started trace = %+v", started)
	}

	recorder = do(t, env.handler, http.MethodGet, "/api/troubleshoot/trace", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get trace status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	result := decode[host.TraceResult](t, recorder)
	if !strings.Contains(result.Raw, "ip4-input") {
		t.Fatalf("trace result = %+v", result)
	}

	recorder = do(t, env.handler, http.MethodDelete, "/api/troubleshoot/trace", nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("clear trace status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestACLInterfaceCounters(t *testing.T) {
	env := newTestAPI(t)
	env.networks.aclCounters = []host.ACLInterfaceCounters{{
		Interface: "vm-01",
		SwIfIndex: 5,
		Ingress: host.ACLDirectionCounters{
			Matched:    212,
			Denied:     212,
			ACLIndexes: []uint32{2},
		},
		Egress: host.ACLDirectionCounters{
			Matched:    34,
			Denied:     34,
			ACLIndexes: []uint32{2},
		},
	}}
	recorder := do(t, env.handler, http.MethodGet, "/api/troubleshoot/acl-counters", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("acl counters status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	counters := decode[[]host.ACLInterfaceCounters](t, recorder)
	if len(counters) != 1 || counters[0].Ingress.Matched != 212 || counters[0].Egress.Denied != 34 {
		t.Fatalf("acl counters = %+v", counters)
	}
}

func TestPacketCaptureAPI(t *testing.T) {
	env := newTestAPI(t)
	env.networks.vppInterfaces = []host.VPPInterface{
		{Name: "vm-01", SwIfIndex: 5, Type: "virtio", MAC: "02:00:00:00:00:01", Up: true},
		{Name: "host-enp1s0", SwIfIndex: 1, Type: "af_packet", Up: true},
	}
	env.networks.captureFiles = []host.PacketCaptureFile{
		{Name: "rookery-test.pcap", Size: 4, Modified: time.Unix(1700000000, 0).UTC()},
	}
	env.networks.captureData = map[string][]byte{"rookery-test.pcap": []byte("pcap")}

	recorder := do(t, env.handler, http.MethodGet, "/api/troubleshoot/capture/interfaces", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("interfaces status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	interfaces := decode[[]host.VPPInterface](t, recorder)
	if len(interfaces) != 2 || interfaces[0].Name != "vm-01" {
		t.Fatalf("interfaces = %+v", interfaces)
	}

	recorder = do(t, env.handler, http.MethodGet, "/api/troubleshoot/capture", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("overview status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	type captureOverview struct {
		Status host.PacketCaptureStatus `json:"status"`
		Files  []host.PacketCaptureFile `json:"files"`
	}
	overview := decode[captureOverview](t, recorder)
	if len(overview.Files) != 1 || overview.Files[0].Name != "rookery-test.pcap" {
		t.Fatalf("overview = %+v", overview)
	}

	recorder = do(t, env.handler, http.MethodPost, "/api/troubleshoot/capture", map[string]any{
		"interface":   "vm-01",
		"direction":   "both",
		"max_bytes":   1024 * 1024,
		"snap_length": 2048,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("start status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	status := decode[host.PacketCaptureStatus](t, recorder)
	if !status.Active || status.Interface != "vm-01" {
		t.Fatalf("start status = %+v", status)
	}

	recorder = do(t, env.handler, http.MethodPost, "/api/troubleshoot/capture/stop", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("stop status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	status = decode[host.PacketCaptureStatus](t, recorder)
	if status.Active {
		t.Fatalf("stop status = %+v", status)
	}

	recorder = do(t, env.handler, http.MethodGet, "/api/troubleshoot/capture/files", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("files status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	files := decode[[]host.PacketCaptureFile](t, recorder)
	if len(files) != 1 || files[0].Name != "rookery-test.pcap" {
		t.Fatalf("files = %+v", files)
	}

	recorder = do(t, env.handler, http.MethodGet, "/api/troubleshoot/capture/files/rookery-test.pcap", nil)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "pcap" {
		t.Fatalf("download status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
	if disposition := recorder.Header().Get("Content-Disposition"); !strings.Contains(disposition, "rookery-test.pcap") {
		t.Fatalf("download disposition = %q", disposition)
	}

	env.networks.captureStatus = host.PacketCaptureStatus{Active: true, File: "rookery-test.pcap"}
	recorder = do(t, env.handler, http.MethodDelete, "/api/troubleshoot/capture/files/rookery-test.pcap", nil)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("delete active status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	env.networks.captureStatus = host.PacketCaptureStatus{}
	recorder = do(t, env.handler, http.MethodDelete, "/api/troubleshoot/capture/files/rookery-test.pcap", nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if env.networks.deletedCapture != "rookery-test.pcap" {
		t.Fatalf("deleted capture = %q", env.networks.deletedCapture)
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

	for _, asset := range []string{"/build/app.js", "/style.css", "/icon.png", "/console.html", "/build/console.js", "/console.css", "/lightvm-console.html", "/build/lightvmConsole.js", "/build/lightvmConsole.css", "/monitor.html", "/build/monitor.js", "/novnc/core/rfb.js", "/novnc/LICENSE.txt"} {
		recorder = do(t, handler, http.MethodGet, asset, nil)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d", asset, recorder.Code)
		}
		if recorder.Body.Len() == 0 {
			t.Fatalf("%s is empty", asset)
		}
	}

	recorder = do(t, handler, http.MethodGet, "/lightvm-console.html", nil)
	if csp := recorder.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "style-src 'self' 'unsafe-inline'") {
		t.Fatalf("LightVM console CSP does not allow xterm inline styles: %q", csp)
	}
	recorder = do(t, handler, http.MethodGet, "/", nil)
	if csp := recorder.Header().Get("Content-Security-Policy"); strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("inventory CSP unexpectedly allows inline styles: %q", csp)
	}

	recorder = do(t, handler, http.MethodPost, "/", nil)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST / status = %d, want 405", recorder.Code)
	}
}
