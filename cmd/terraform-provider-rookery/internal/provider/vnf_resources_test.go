package provider

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

// fakeVNFBackend is an in-memory implementation of the four typed VNF clients
// and the label client used by provider tests.
type fakeVNFBackend struct {
	next        int64
	dhcp        map[int64]*rookeryv1.DHCPVNF
	dns         map[int64]*rookeryv1.DNSVNF
	nat44       map[int64]*rookeryv1.NAT44VNF
	bgp         map[int64]*rookeryv1.BGPVNF
	assignments map[int64]*rookeryv1.DHCPStaticAssignment
	options     map[int64]*rookeryv1.DHCPOption
	records     map[int64]*rookeryv1.DNSRecord
	forwarders  map[int64]*rookeryv1.DNSForwarder
	dnat        map[int64]*rookeryv1.DNATRule
	prefixes    map[int64]*rookeryv1.BGPPrefix
	labels      map[string][]string
}

func newFakeVNFBackend() *fakeVNFBackend {
	return &fakeVNFBackend{
		dhcp:        map[int64]*rookeryv1.DHCPVNF{},
		dns:         map[int64]*rookeryv1.DNSVNF{},
		nat44:       map[int64]*rookeryv1.NAT44VNF{},
		bgp:         map[int64]*rookeryv1.BGPVNF{},
		assignments: map[int64]*rookeryv1.DHCPStaticAssignment{},
		options:     map[int64]*rookeryv1.DHCPOption{},
		records:     map[int64]*rookeryv1.DNSRecord{},
		forwarders:  map[int64]*rookeryv1.DNSForwarder{},
		dnat:        map[int64]*rookeryv1.DNATRule{},
		prefixes:    map[int64]*rookeryv1.BGPPrefix{},
		labels:      map[string][]string{},
	}
}

func (b *fakeVNFBackend) id() int64 {
	b.next++
	return b.next
}

func (b *fakeVNFBackend) client() *Client {
	return &Client{
		DHCPVNFs:  &fakeDHCPVNFClient{backend: b},
		DNSVNFs:   &fakeDNSVNFClient{backend: b},
		NAT44VNFs: &fakeNAT44VNFClient{backend: b},
		BGPVNFs:   &fakeBGPVNFClient{backend: b},
		Labels:    &fakeVNFLabelClient{backend: b},
		timeout:   time.Second,
	}
}

// seed inserts one parent of each type plus labels for data source tests.
func (b *fakeVNFBackend) seed() {
	now := timestamppb.Now()
	b.dhcp[7] = &rookeryv1.DHCPVNF{
		Id: 7, Name: "app-dhcp", NetworkId: 2, NetworkName: "app", Family: "dhcpv4", Enabled: true,
		Gateway: "10.20.0.1/16", RangeStart: "10.20.0.10", RangeEnd: "10.20.0.100", LeaseTime: "12h",
		StaticAssignmentCount: 2, OptionCount: 1, LeaseCount: 3, Status: "running",
		TapSwIfIndex: 11, TapHostName: "vnf-dhcp", CreatedAt: now, UpdatedAt: now,
	}
	b.dns[7] = &rookeryv1.DNSVNF{
		Id: 7, Name: "app-dns", NetworkId: 2, NetworkName: "app", Enabled: true, Forward: true,
		RecordCount: 2, ForwarderCount: 1, Status: "running", CreatedAt: now, UpdatedAt: now,
	}
	b.nat44[7] = &rookeryv1.NAT44VNF{
		Id: 7, Name: "app-nat", NetworkId: 2, NetworkName: "app", Enabled: true,
		Gateway: "10.20.0.1/16", OutsideInterface: "eth0", DnatRuleCount: 1, Status: "running",
		CreatedAt: now, UpdatedAt: now,
	}
	b.bgp[7] = &rookeryv1.BGPVNF{
		Id: 7, Name: "app-bgp", NetworkId: 2, NetworkName: "app", Enabled: true, Transport: "tap",
		LocalAs: 65001, RouterId: "10.20.0.2", PeerAs: 65002, PeerIp: "10.20.0.2",
		LocalIp: "10.20.0.1/24", MultihopTtl: 64, AdvertisedPrefixCount: 2, Status: "running",
		CreatedAt: now, UpdatedAt: now,
	}
	b.labels["vnf/7"] = []string{"prod"}
}

func notFoundError() error {
	return status.Error(codes.NotFound, "not found")
}

func networkName(id int64) string {
	return fmt.Sprintf("network-%d", id)
}

// --- DHCP client ---

type fakeDHCPVNFClient struct {
	rookeryv1.DHCPVNFServiceClient
	backend *fakeVNFBackend
}

func (f *fakeDHCPVNFClient) ListDHCPVNFs(context.Context, *rookeryv1.ListDHCPVNFsRequest, ...grpc.CallOption) (*rookeryv1.ListDHCPVNFsResponse, error) {
	var vnfs []*rookeryv1.DHCPVNF
	for _, vnf := range f.backend.dhcp {
		vnfs = append(vnfs, vnf)
	}
	return &rookeryv1.ListDHCPVNFsResponse{Vnfs: vnfs}, nil
}

func (f *fakeDHCPVNFClient) GetDHCPVNF(_ context.Context, request *rookeryv1.GetDHCPVNFRequest, _ ...grpc.CallOption) (*rookeryv1.DHCPVNF, error) {
	vnf, ok := f.backend.dhcp[request.GetId()]
	if !ok {
		return nil, notFoundError()
	}
	return vnf, nil
}

func (f *fakeDHCPVNFClient) CreateDHCPVNF(_ context.Context, request *rookeryv1.CreateDHCPVNFRequest, _ ...grpc.CallOption) (*rookeryv1.DHCPVNF, error) {
	enabled := true
	if request.Enabled != nil {
		enabled = request.GetEnabled()
	}
	now := timestamppb.Now()
	vnf := &rookeryv1.DHCPVNF{
		Id: f.backend.id(), Name: request.GetName(), NetworkId: request.GetNetworkId(),
		NetworkName: networkName(request.GetNetworkId()), Family: request.GetFamily(), Enabled: enabled,
		Gateway: request.GetGateway(), RangeStart: request.GetRangeStart(), RangeEnd: request.GetRangeEnd(),
		LeaseTime: request.GetLeaseTime(), Dns: request.GetDns(), BindIp: request.GetBindIp(),
		Transport: request.GetTransport(), DnsVnfId: request.GetDnsVnfId(),
		DnsDomain: request.GetDnsDomain(), DnsTtl: request.GetDnsTtl(),
		StaticAssignmentCount: 2, OptionCount: 1, LeaseCount: 3, Status: "running",
		TapSwIfIndex: 10, TapHostName: "vnf0", CreatedAt: now, UpdatedAt: now,
	}
	f.backend.dhcp[vnf.Id] = vnf
	return vnf, nil
}

func (f *fakeDHCPVNFClient) UpdateDHCPVNF(_ context.Context, request *rookeryv1.UpdateDHCPVNFRequest, _ ...grpc.CallOption) (*rookeryv1.DHCPVNF, error) {
	vnf, ok := f.backend.dhcp[request.GetId()]
	if !ok {
		return nil, notFoundError()
	}
	if request.Name != nil {
		vnf.Name = request.GetName()
	}
	if request.Enabled != nil {
		vnf.Enabled = request.GetEnabled()
	}
	if request.Gateway != nil {
		vnf.Gateway = request.GetGateway()
	}
	if request.RangeStart != nil {
		vnf.RangeStart = request.GetRangeStart()
	}
	if request.RangeEnd != nil {
		vnf.RangeEnd = request.GetRangeEnd()
	}
	if request.LeaseTime != nil {
		vnf.LeaseTime = request.GetLeaseTime()
	}
	if request.Dns != nil {
		vnf.Dns = request.GetDns()
	}
	if request.BindIp != nil {
		vnf.BindIp = request.GetBindIp()
	}
	if request.Transport != nil {
		vnf.Transport = request.GetTransport()
	}
	if request.DnsVnfId != nil {
		vnf.DnsVnfId = request.GetDnsVnfId()
	}
	if request.DnsDomain != nil {
		vnf.DnsDomain = request.GetDnsDomain()
	}
	if request.DnsTtl != nil {
		vnf.DnsTtl = request.GetDnsTtl()
	}
	vnf.UpdatedAt = timestamppb.Now()
	return vnf, nil
}

func (f *fakeDHCPVNFClient) DeleteDHCPVNF(_ context.Context, request *rookeryv1.DeleteDHCPVNFRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	if _, ok := f.backend.dhcp[request.GetId()]; !ok {
		return nil, notFoundError()
	}
	delete(f.backend.dhcp, request.GetId())
	return &emptypb.Empty{}, nil
}

func (f *fakeDHCPVNFClient) GetDHCPStaticAssignment(_ context.Context, request *rookeryv1.GetDHCPStaticAssignmentRequest, _ ...grpc.CallOption) (*rookeryv1.DHCPStaticAssignment, error) {
	assignment, ok := f.backend.assignments[request.GetAssignmentId()]
	if !ok || assignment.GetVnfId() != request.GetVnfId() {
		return nil, notFoundError()
	}
	return assignment, nil
}

func (f *fakeDHCPVNFClient) CreateDHCPStaticAssignment(_ context.Context, request *rookeryv1.CreateDHCPStaticAssignmentRequest, _ ...grpc.CallOption) (*rookeryv1.DHCPStaticAssignment, error) {
	assignment := &rookeryv1.DHCPStaticAssignment{
		Id: f.backend.id(), VnfId: request.GetVnfId(), Mac: request.GetMac(), Ip: request.GetIp(),
	}
	f.backend.assignments[assignment.Id] = assignment
	return assignment, nil
}

func (f *fakeDHCPVNFClient) UpdateDHCPStaticAssignment(_ context.Context, request *rookeryv1.UpdateDHCPStaticAssignmentRequest, _ ...grpc.CallOption) (*rookeryv1.DHCPStaticAssignment, error) {
	assignment, ok := f.backend.assignments[request.GetAssignmentId()]
	if !ok {
		return nil, notFoundError()
	}
	assignment.Mac = request.GetMac()
	assignment.Ip = request.GetIp()
	return assignment, nil
}

func (f *fakeDHCPVNFClient) DeleteDHCPStaticAssignment(_ context.Context, request *rookeryv1.DeleteDHCPStaticAssignmentRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	if _, ok := f.backend.assignments[request.GetAssignmentId()]; !ok {
		return nil, notFoundError()
	}
	delete(f.backend.assignments, request.GetAssignmentId())
	return &emptypb.Empty{}, nil
}

func (f *fakeDHCPVNFClient) GetDHCPOption(_ context.Context, request *rookeryv1.GetDHCPOptionRequest, _ ...grpc.CallOption) (*rookeryv1.DHCPOption, error) {
	option, ok := f.backend.options[request.GetOptionId()]
	if !ok || option.GetVnfId() != request.GetVnfId() {
		return nil, notFoundError()
	}
	return option, nil
}

func (f *fakeDHCPVNFClient) CreateDHCPOption(_ context.Context, request *rookeryv1.CreateDHCPOptionRequest, _ ...grpc.CallOption) (*rookeryv1.DHCPOption, error) {
	option := &rookeryv1.DHCPOption{
		Id: f.backend.id(), VnfId: request.GetVnfId(), Code: request.GetCode(),
		Format: request.GetFormat(), Value: request.GetValue(),
	}
	f.backend.options[option.Id] = option
	return option, nil
}

func (f *fakeDHCPVNFClient) UpdateDHCPOption(_ context.Context, request *rookeryv1.UpdateDHCPOptionRequest, _ ...grpc.CallOption) (*rookeryv1.DHCPOption, error) {
	option, ok := f.backend.options[request.GetOptionId()]
	if !ok {
		return nil, notFoundError()
	}
	option.Code = request.GetCode()
	option.Format = request.GetFormat()
	option.Value = request.GetValue()
	return option, nil
}

func (f *fakeDHCPVNFClient) DeleteDHCPOption(_ context.Context, request *rookeryv1.DeleteDHCPOptionRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	if _, ok := f.backend.options[request.GetOptionId()]; !ok {
		return nil, notFoundError()
	}
	delete(f.backend.options, request.GetOptionId())
	return &emptypb.Empty{}, nil
}

// --- DNS client ---

type fakeDNSVNFClient struct {
	rookeryv1.DNSVNFServiceClient
	backend *fakeVNFBackend
}

func (f *fakeDNSVNFClient) ListDNSVNFs(context.Context, *rookeryv1.ListDNSVNFsRequest, ...grpc.CallOption) (*rookeryv1.ListDNSVNFsResponse, error) {
	var vnfs []*rookeryv1.DNSVNF
	for _, vnf := range f.backend.dns {
		vnfs = append(vnfs, vnf)
	}
	return &rookeryv1.ListDNSVNFsResponse{Vnfs: vnfs}, nil
}

func (f *fakeDNSVNFClient) GetDNSVNF(_ context.Context, request *rookeryv1.GetDNSVNFRequest, _ ...grpc.CallOption) (*rookeryv1.DNSVNF, error) {
	vnf, ok := f.backend.dns[request.GetId()]
	if !ok {
		return nil, notFoundError()
	}
	return vnf, nil
}

func (f *fakeDNSVNFClient) CreateDNSVNF(_ context.Context, request *rookeryv1.CreateDNSVNFRequest, _ ...grpc.CallOption) (*rookeryv1.DNSVNF, error) {
	enabled := true
	if request.Enabled != nil {
		enabled = request.GetEnabled()
	}
	now := timestamppb.Now()
	vnf := &rookeryv1.DNSVNF{
		Id: f.backend.id(), Name: request.GetName(), NetworkId: request.GetNetworkId(),
		NetworkName: networkName(request.GetNetworkId()), Enabled: enabled, Forward: request.GetForward(),
		RecordCount: 2, ForwarderCount: 1, Status: "running",
		TapSwIfIndex: 10, TapHostName: "vnf0", CreatedAt: now, UpdatedAt: now,
	}
	f.backend.dns[vnf.Id] = vnf
	return vnf, nil
}

func (f *fakeDNSVNFClient) UpdateDNSVNF(_ context.Context, request *rookeryv1.UpdateDNSVNFRequest, _ ...grpc.CallOption) (*rookeryv1.DNSVNF, error) {
	vnf, ok := f.backend.dns[request.GetId()]
	if !ok {
		return nil, notFoundError()
	}
	if request.Name != nil {
		vnf.Name = request.GetName()
	}
	if request.Enabled != nil {
		vnf.Enabled = request.GetEnabled()
	}
	if request.Forward != nil {
		vnf.Forward = request.GetForward()
	}
	vnf.UpdatedAt = timestamppb.Now()
	return vnf, nil
}

func (f *fakeDNSVNFClient) DeleteDNSVNF(_ context.Context, request *rookeryv1.DeleteDNSVNFRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	if _, ok := f.backend.dns[request.GetId()]; !ok {
		return nil, notFoundError()
	}
	delete(f.backend.dns, request.GetId())
	return &emptypb.Empty{}, nil
}

func (f *fakeDNSVNFClient) GetDNSVNFRecord(_ context.Context, request *rookeryv1.GetDNSVNFRecordRequest, _ ...grpc.CallOption) (*rookeryv1.DNSRecord, error) {
	record, ok := f.backend.records[request.GetRecordId()]
	if !ok || record.GetVnfId() != request.GetVnfId() {
		return nil, notFoundError()
	}
	return record, nil
}

func (f *fakeDNSVNFClient) CreateDNSVNFRecord(_ context.Context, request *rookeryv1.CreateDNSVNFRecordRequest, _ ...grpc.CallOption) (*rookeryv1.DNSRecord, error) {
	record := &rookeryv1.DNSRecord{
		Id: f.backend.id(), VnfId: request.GetVnfId(), Name: request.GetName(),
		Type: request.GetType(), Value: request.GetValue(), Ttl: request.GetTtl(),
	}
	f.backend.records[record.Id] = record
	return record, nil
}

func (f *fakeDNSVNFClient) UpdateDNSVNFRecord(_ context.Context, request *rookeryv1.UpdateDNSVNFRecordRequest, _ ...grpc.CallOption) (*rookeryv1.DNSRecord, error) {
	record, ok := f.backend.records[request.GetRecordId()]
	if !ok {
		return nil, notFoundError()
	}
	record.Name = request.GetName()
	record.Type = request.GetType()
	record.Value = request.GetValue()
	record.Ttl = request.GetTtl()
	return record, nil
}

func (f *fakeDNSVNFClient) DeleteDNSVNFRecord(_ context.Context, request *rookeryv1.DeleteDNSVNFRecordRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	if _, ok := f.backend.records[request.GetRecordId()]; !ok {
		return nil, notFoundError()
	}
	delete(f.backend.records, request.GetRecordId())
	return &emptypb.Empty{}, nil
}

func (f *fakeDNSVNFClient) GetDNSVNFForwarder(_ context.Context, request *rookeryv1.GetDNSVNFForwarderRequest, _ ...grpc.CallOption) (*rookeryv1.DNSForwarder, error) {
	forwarder, ok := f.backend.forwarders[request.GetForwarderId()]
	if !ok || forwarder.GetVnfId() != request.GetVnfId() {
		return nil, notFoundError()
	}
	return forwarder, nil
}

func (f *fakeDNSVNFClient) CreateDNSVNFForwarder(_ context.Context, request *rookeryv1.CreateDNSVNFForwarderRequest, _ ...grpc.CallOption) (*rookeryv1.DNSForwarder, error) {
	forwarder := &rookeryv1.DNSForwarder{
		Id: f.backend.id(), VnfId: request.GetVnfId(), Address: request.GetAddress(),
		Position: request.GetPosition(),
	}
	f.backend.forwarders[forwarder.Id] = forwarder
	return forwarder, nil
}

func (f *fakeDNSVNFClient) UpdateDNSVNFForwarder(_ context.Context, request *rookeryv1.UpdateDNSVNFForwarderRequest, _ ...grpc.CallOption) (*rookeryv1.DNSForwarder, error) {
	forwarder, ok := f.backend.forwarders[request.GetForwarderId()]
	if !ok {
		return nil, notFoundError()
	}
	forwarder.Address = request.GetAddress()
	forwarder.Position = request.GetPosition()
	return forwarder, nil
}

func (f *fakeDNSVNFClient) DeleteDNSVNFForwarder(_ context.Context, request *rookeryv1.DeleteDNSVNFForwarderRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	if _, ok := f.backend.forwarders[request.GetForwarderId()]; !ok {
		return nil, notFoundError()
	}
	delete(f.backend.forwarders, request.GetForwarderId())
	return &emptypb.Empty{}, nil
}

// --- NAT44 client ---

type fakeNAT44VNFClient struct {
	rookeryv1.NAT44VNFServiceClient
	backend *fakeVNFBackend
}

func (f *fakeNAT44VNFClient) ListNAT44VNFs(context.Context, *rookeryv1.ListNAT44VNFsRequest, ...grpc.CallOption) (*rookeryv1.ListNAT44VNFsResponse, error) {
	var vnfs []*rookeryv1.NAT44VNF
	for _, vnf := range f.backend.nat44 {
		vnfs = append(vnfs, vnf)
	}
	return &rookeryv1.ListNAT44VNFsResponse{Vnfs: vnfs}, nil
}

func (f *fakeNAT44VNFClient) GetNAT44VNF(_ context.Context, request *rookeryv1.GetNAT44VNFRequest, _ ...grpc.CallOption) (*rookeryv1.NAT44VNF, error) {
	vnf, ok := f.backend.nat44[request.GetId()]
	if !ok {
		return nil, notFoundError()
	}
	return vnf, nil
}

func (f *fakeNAT44VNFClient) CreateNAT44VNF(_ context.Context, request *rookeryv1.CreateNAT44VNFRequest, _ ...grpc.CallOption) (*rookeryv1.NAT44VNF, error) {
	enabled := true
	if request.Enabled != nil {
		enabled = request.GetEnabled()
	}
	now := timestamppb.Now()
	vnf := &rookeryv1.NAT44VNF{
		Id: f.backend.id(), Name: request.GetName(), NetworkId: request.GetNetworkId(),
		NetworkName: networkName(request.GetNetworkId()), Enabled: enabled,
		Gateway: request.GetGateway(), OutsideInterface: request.GetOutsideInterface(),
		OutsideAddress: request.GetOutsideAddress(), OutsideGateway: request.GetOutsideGateway(),
		Nat44Pool: request.GetNat44Pool(), DnatRuleCount: 1, Status: "running",
		TapSwIfIndex: 10, TapHostName: "vnf0", CreatedAt: now, UpdatedAt: now,
	}
	f.backend.nat44[vnf.Id] = vnf
	return vnf, nil
}

func (f *fakeNAT44VNFClient) UpdateNAT44VNF(_ context.Context, request *rookeryv1.UpdateNAT44VNFRequest, _ ...grpc.CallOption) (*rookeryv1.NAT44VNF, error) {
	vnf, ok := f.backend.nat44[request.GetId()]
	if !ok {
		return nil, notFoundError()
	}
	if request.Name != nil {
		vnf.Name = request.GetName()
	}
	if request.Enabled != nil {
		vnf.Enabled = request.GetEnabled()
	}
	if request.Gateway != nil {
		vnf.Gateway = request.GetGateway()
	}
	if request.OutsideInterface != nil {
		vnf.OutsideInterface = request.GetOutsideInterface()
	}
	if request.OutsideAddress != nil {
		vnf.OutsideAddress = request.GetOutsideAddress()
	}
	if request.OutsideGateway != nil {
		vnf.OutsideGateway = request.GetOutsideGateway()
	}
	if request.Nat44Pool != nil {
		vnf.Nat44Pool = request.GetNat44Pool()
	}
	vnf.UpdatedAt = timestamppb.Now()
	return vnf, nil
}

func (f *fakeNAT44VNFClient) DeleteNAT44VNF(_ context.Context, request *rookeryv1.DeleteNAT44VNFRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	if _, ok := f.backend.nat44[request.GetId()]; !ok {
		return nil, notFoundError()
	}
	delete(f.backend.nat44, request.GetId())
	return &emptypb.Empty{}, nil
}

func (f *fakeNAT44VNFClient) GetNAT44DNATRule(_ context.Context, request *rookeryv1.GetNAT44DNATRuleRequest, _ ...grpc.CallOption) (*rookeryv1.DNATRule, error) {
	rule, ok := f.backend.dnat[request.GetRuleId()]
	if !ok || rule.GetVnfId() != request.GetVnfId() {
		return nil, notFoundError()
	}
	return rule, nil
}

func (f *fakeNAT44VNFClient) CreateNAT44DNATRule(_ context.Context, request *rookeryv1.CreateNAT44DNATRuleRequest, _ ...grpc.CallOption) (*rookeryv1.DNATRule, error) {
	rule := &rookeryv1.DNATRule{
		Id: f.backend.id(), VnfId: request.GetVnfId(), Protocol: request.GetProtocol(),
		ExternalPort: request.GetExternalPort(), LocalIp: request.GetLocalIp(),
		LocalPort: request.GetLocalPort(),
	}
	f.backend.dnat[rule.Id] = rule
	return rule, nil
}

func (f *fakeNAT44VNFClient) UpdateNAT44DNATRule(_ context.Context, request *rookeryv1.UpdateNAT44DNATRuleRequest, _ ...grpc.CallOption) (*rookeryv1.DNATRule, error) {
	rule, ok := f.backend.dnat[request.GetRuleId()]
	if !ok {
		return nil, notFoundError()
	}
	rule.Protocol = request.GetProtocol()
	rule.ExternalPort = request.GetExternalPort()
	rule.LocalIp = request.GetLocalIp()
	rule.LocalPort = request.GetLocalPort()
	return rule, nil
}

func (f *fakeNAT44VNFClient) DeleteNAT44DNATRule(_ context.Context, request *rookeryv1.DeleteNAT44DNATRuleRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	if _, ok := f.backend.dnat[request.GetRuleId()]; !ok {
		return nil, notFoundError()
	}
	delete(f.backend.dnat, request.GetRuleId())
	return &emptypb.Empty{}, nil
}

// --- BGP client ---

type fakeBGPVNFClient struct {
	rookeryv1.BGPVNFServiceClient
	backend *fakeVNFBackend
}

func (f *fakeBGPVNFClient) ListBGPVNFs(context.Context, *rookeryv1.ListBGPVNFsRequest, ...grpc.CallOption) (*rookeryv1.ListBGPVNFsResponse, error) {
	var vnfs []*rookeryv1.BGPVNF
	for _, vnf := range f.backend.bgp {
		vnfs = append(vnfs, vnf)
	}
	return &rookeryv1.ListBGPVNFsResponse{Vnfs: vnfs}, nil
}

func (f *fakeBGPVNFClient) GetBGPVNF(_ context.Context, request *rookeryv1.GetBGPVNFRequest, _ ...grpc.CallOption) (*rookeryv1.BGPVNF, error) {
	vnf, ok := f.backend.bgp[request.GetId()]
	if !ok {
		return nil, notFoundError()
	}
	return vnf, nil
}

func (f *fakeBGPVNFClient) CreateBGPVNF(_ context.Context, request *rookeryv1.CreateBGPVNFRequest, _ ...grpc.CallOption) (*rookeryv1.BGPVNF, error) {
	enabled := true
	if request.Enabled != nil {
		enabled = request.GetEnabled()
	}
	transport := request.GetTransport()
	if transport == "" {
		transport = "memif"
	}
	ttl := request.GetMultihopTtl()
	if ttl == 0 {
		ttl = 64
	}
	now := timestamppb.Now()
	vnf := &rookeryv1.BGPVNF{
		Id: f.backend.id(), Name: request.GetName(), NetworkId: request.GetNetworkId(),
		NetworkName: networkName(request.GetNetworkId()), Enabled: enabled, Transport: transport,
		LocalAs: request.GetLocalAs(), RouterId: request.GetRouterId(), PeerAs: request.GetPeerAs(),
		PeerIp: request.GetPeerIp(), LocalIp: request.GetLocalIp(), PeerGateway: request.GetPeerGateway(),
		MultihopTtl: ttl, Passive: request.GetPassive(), TargetTableId: request.GetTargetTableId(),
		NextHop: request.GetNextHop(), AdvertisedPrefixCount: 2, Status: "running",
		TapSwIfIndex: 10, TapHostName: "vnf0", CreatedAt: now, UpdatedAt: now,
	}
	f.backend.bgp[vnf.Id] = vnf
	return vnf, nil
}

func (f *fakeBGPVNFClient) UpdateBGPVNF(_ context.Context, request *rookeryv1.UpdateBGPVNFRequest, _ ...grpc.CallOption) (*rookeryv1.BGPVNF, error) {
	vnf, ok := f.backend.bgp[request.GetId()]
	if !ok {
		return nil, notFoundError()
	}
	if request.Name != nil {
		vnf.Name = request.GetName()
	}
	if request.Enabled != nil {
		vnf.Enabled = request.GetEnabled()
	}
	if request.Transport != nil {
		vnf.Transport = request.GetTransport()
	}
	if request.LocalAs != nil {
		vnf.LocalAs = request.GetLocalAs()
	}
	if request.RouterId != nil {
		vnf.RouterId = request.GetRouterId()
	}
	if request.PeerAs != nil {
		vnf.PeerAs = request.GetPeerAs()
	}
	if request.PeerIp != nil {
		vnf.PeerIp = request.GetPeerIp()
	}
	if request.LocalIp != nil {
		vnf.LocalIp = request.GetLocalIp()
	}
	if request.PeerGateway != nil {
		vnf.PeerGateway = request.GetPeerGateway()
	}
	if request.MultihopTtl != nil {
		vnf.MultihopTtl = request.GetMultihopTtl()
	}
	if request.Passive != nil {
		vnf.Passive = request.GetPassive()
	}
	if request.TargetTableId != nil {
		vnf.TargetTableId = request.GetTargetTableId()
	}
	if request.NextHop != nil {
		vnf.NextHop = request.GetNextHop()
	}
	vnf.UpdatedAt = timestamppb.Now()
	return vnf, nil
}

func (f *fakeBGPVNFClient) DeleteBGPVNF(_ context.Context, request *rookeryv1.DeleteBGPVNFRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	if _, ok := f.backend.bgp[request.GetId()]; !ok {
		return nil, notFoundError()
	}
	delete(f.backend.bgp, request.GetId())
	return &emptypb.Empty{}, nil
}

func (f *fakeBGPVNFClient) GetBGPVNFAdvertisedPrefix(_ context.Context, request *rookeryv1.GetBGPVNFAdvertisedPrefixRequest, _ ...grpc.CallOption) (*rookeryv1.BGPPrefix, error) {
	prefix, ok := f.backend.prefixes[request.GetPrefixId()]
	if !ok || prefix.GetVnfId() != request.GetVnfId() {
		return nil, notFoundError()
	}
	return prefix, nil
}

func (f *fakeBGPVNFClient) CreateBGPVNFAdvertisedPrefix(_ context.Context, request *rookeryv1.CreateBGPVNFAdvertisedPrefixRequest, _ ...grpc.CallOption) (*rookeryv1.BGPPrefix, error) {
	prefix := &rookeryv1.BGPPrefix{Id: f.backend.id(), VnfId: request.GetVnfId(), Prefix: request.GetPrefix()}
	f.backend.prefixes[prefix.Id] = prefix
	return prefix, nil
}

func (f *fakeBGPVNFClient) UpdateBGPVNFAdvertisedPrefix(_ context.Context, request *rookeryv1.UpdateBGPVNFAdvertisedPrefixRequest, _ ...grpc.CallOption) (*rookeryv1.BGPPrefix, error) {
	prefix, ok := f.backend.prefixes[request.GetPrefixId()]
	if !ok {
		return nil, notFoundError()
	}
	prefix.Prefix = request.GetPrefix()
	return prefix, nil
}

func (f *fakeBGPVNFClient) DeleteBGPVNFAdvertisedPrefix(_ context.Context, request *rookeryv1.DeleteBGPVNFAdvertisedPrefixRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	if _, ok := f.backend.prefixes[request.GetPrefixId()]; !ok {
		return nil, notFoundError()
	}
	delete(f.backend.prefixes, request.GetPrefixId())
	return &emptypb.Empty{}, nil
}

// --- label client ---

type fakeVNFLabelClient struct {
	rookeryv1.LabelServiceClient
	backend *fakeVNFBackend
}

func (f *fakeVNFLabelClient) SetEntityLabels(_ context.Context, request *rookeryv1.SetEntityLabelsRequest, _ ...grpc.CallOption) (*rookeryv1.EntityLabels, error) {
	key := request.GetEntityType() + "/" + request.GetEntityId()
	f.backend.labels[key] = append([]string{}, request.GetLabels()...)
	return &rookeryv1.EntityLabels{
		EntityType: request.GetEntityType(), EntityId: request.GetEntityId(), Labels: request.GetLabels(),
	}, nil
}

func (f *fakeVNFLabelClient) GetEntityLabels(_ context.Context, request *rookeryv1.GetEntityLabelsRequest, _ ...grpc.CallOption) (*rookeryv1.EntityLabels, error) {
	key := request.GetEntityType() + "/" + request.GetEntityId()
	return &rookeryv1.EntityLabels{
		EntityType: request.GetEntityType(), EntityId: request.GetEntityId(), Labels: f.backend.labels[key],
	}, nil
}

// --- helpers ---

func vnfPlanFromModel(t *testing.T, schema rschema.Schema, model any) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Schema: schema}
	if diags := plan.Set(context.Background(), model); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	return plan
}

func vnfStateFromModel(t *testing.T, schema rschema.Schema, model any) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: schema}
	if diags := state.Set(context.Background(), model); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	return state
}

func newModelPointer(model any) any {
	return reflect.New(reflect.TypeOf(model)).Interface()
}

func modelValue(model any) any {
	return reflect.ValueOf(model).Elem().Interface()
}

func modelAttribute(model any, attribute string) any {
	value := reflect.ValueOf(model)
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	typ := value.Type()
	for i := 0; i < typ.NumField(); i++ {
		if strings.Split(typ.Field(i).Tag.Get("tfsdk"), ",")[0] == attribute {
			return value.Field(i).Interface()
		}
	}
	return nil
}

func withModelAttribute(t *testing.T, model any, attribute string, value any) any {
	t.Helper()
	copy := reflect.New(reflect.TypeOf(model))
	copy.Elem().Set(reflect.ValueOf(model))
	typ := copy.Elem().Type()
	for i := 0; i < typ.NumField(); i++ {
		if strings.Split(typ.Field(i).Tag.Get("tfsdk"), ",")[0] == attribute {
			copy.Elem().Field(i).Set(reflect.ValueOf(value))
			return copy.Elem().Interface()
		}
	}
	t.Fatalf("%T has no %q attribute", model, attribute)
	return nil
}

func int64AttributeRequiresReplace(t *testing.T, schema rschema.Schema, attribute string, stateModel, planModel any) bool {
	t.Helper()
	attr, ok := schema.Attributes[attribute].(rschema.Int64Attribute)
	if !ok {
		t.Fatalf("%q is not an Int64Attribute", attribute)
	}
	state := vnfStateFromModel(t, schema, stateModel)
	plan := vnfPlanFromModel(t, schema, planModel)
	stateValue, _ := modelAttribute(stateModel, attribute).(types.Int64)
	planValue, _ := modelAttribute(planModel, attribute).(types.Int64)
	req := planmodifier.Int64Request{
		State:       state,
		Plan:        plan,
		Config:      tfsdk.Config{Schema: schema, Raw: plan.Raw},
		StateValue:  stateValue,
		PlanValue:   planValue,
		ConfigValue: planValue,
	}
	resp := &planmodifier.Int64Response{}
	for _, modifier := range attr.PlanModifiers {
		modifier.PlanModifyInt64(context.Background(), req, resp)
	}
	return resp.RequiresReplace
}

// --- models ---

func testDHCPVNFModel() dhcpVNFModel {
	return dhcpVNFModel{
		Name:       types.StringValue("app-dhcp"),
		NetworkID:  types.Int64Value(2),
		Family:     types.StringValue("dhcpv4"),
		Enabled:    types.BoolValue(true),
		Gateway:    types.StringValue("10.20.0.1/16"),
		RangeStart: types.StringValue("10.20.0.10"),
		RangeEnd:   types.StringValue("10.20.0.100"),
		LeaseTime:  types.StringValue("12h"),
		DNS:        types.StringValue("10.20.0.2"),
		DNSTTL:     types.Int64Value(300),
		Labels:     types.SetNull(types.StringType),
	}
}

func testDNSVNFModel() dnsVNFModel {
	return dnsVNFModel{
		Name:      types.StringValue("app-dns"),
		NetworkID: types.Int64Value(2),
		Enabled:   types.BoolValue(true),
		Forward:   types.BoolValue(true),
		Labels:    types.SetNull(types.StringType),
	}
}

func testNAT44VNFModel() nat44VNFModel {
	return nat44VNFModel{
		Name:             types.StringValue("app-nat"),
		NetworkID:        types.Int64Value(2),
		Enabled:          types.BoolValue(true),
		Gateway:          types.StringValue("10.20.0.1/16"),
		OutsideInterface: types.StringValue("eth0"),
		OutsideAddress:   types.StringValue("192.0.2.0/24"),
		OutsideGateway:   types.StringValue("192.0.2.1"),
		NAT44Pool:        types.StringValue("192.0.2.10-192.0.2.20"),
		Labels:           types.SetNull(types.StringType),
	}
}

func testBGPVNFModel() bgpVNFModel {
	return bgpVNFModel{
		Name:        types.StringValue("app-bgp"),
		NetworkID:   types.Int64Value(2),
		Enabled:     types.BoolValue(true),
		Transport:   types.StringValue("tap"),
		LocalAS:     types.Int64Value(65001),
		RouterID:    types.StringValue("10.20.0.2"),
		PeerAS:      types.Int64Value(65002),
		PeerIP:      types.StringValue("10.20.0.2"),
		LocalIP:     types.StringValue("10.20.0.1/24"),
		MultihopTTL: types.Int64Value(64),
		Password:    types.StringValue("secret"),
		Passive:     types.BoolValue(false),
		Labels:      types.SetNull(types.StringType),
	}
}

func testDHCPStaticAssignmentModel() dhcpStaticAssignmentModel {
	return dhcpStaticAssignmentModel{
		VNFID: types.Int64Value(42),
		MAC:   types.StringValue("02:00:00:00:00:01"),
		IP:    types.StringValue("10.20.0.50"),
	}
}

func testDHCPOptionModel() dhcpOptionModel {
	return dhcpOptionModel{
		VNFID:  types.Int64Value(42),
		Code:   types.Int64Value(42),
		Format: types.StringValue("string"),
		Value:  types.StringValue("ntp.lab.test"),
	}
}

func testDNSRecordModel() dnsRecordModel {
	return dnsRecordModel{
		VNFID: types.Int64Value(42),
		Name:  types.StringValue("router.lab.test"),
		Type:  types.StringValue("A"),
		Value: types.StringValue("10.20.0.1"),
		TTL:   types.Int64Value(300),
	}
}

func testDNSForwarderModel() dnsForwarderModel {
	return dnsForwarderModel{
		VNFID:    types.Int64Value(42),
		Address:  types.StringValue("10.20.0.2"),
		Position: types.Int64Value(1),
	}
}

func testNAT44DNATRuleModel() nat44DNATRuleModel {
	return nat44DNATRuleModel{
		VNFID:        types.Int64Value(42),
		Protocol:     types.StringValue("tcp"),
		ExternalPort: types.Int64Value(8080),
		LocalIP:      types.StringValue("10.20.0.10"),
		LocalPort:    types.Int64Value(80),
	}
}

func testBGPAdvertisedPrefixModel() bgpAdvertisedPrefixModel {
	return bgpAdvertisedPrefixModel{
		VNFID:  types.Int64Value(42),
		Prefix: types.StringValue("10.30.0.0/24"),
	}
}

// --- resource table ---

type vnfResourceTestCase struct {
	name         string
	newValue     func(*Client) resource.Resource
	model        func() any
	update       func(any)
	check        func(*testing.T, any)
	checkUpdated func(*testing.T, any)
	importID     string
	wantVNFID    int64
	sensitive    []string
	replaceAttrs map[string]any
}

func TestVNFResourceCRUD(t *testing.T) {
	ctx := context.Background()
	tests := []vnfResourceTestCase{
		{
			name:     "dhcp vnf",
			newValue: func(client *Client) resource.Resource { return &dhcpVNFResource{configuredResource{client: client}} },
			model:    func() any { return testDHCPVNFModel() },
			update: func(model any) {
				m := model.(*dhcpVNFModel)
				m.Name = types.StringValue("updated-dhcp")
				m.LeaseTime = types.StringValue("24h")
			},
			check:        checkDHCPVNFState,
			checkUpdated: checkDHCPVNFUpdated,
			importID:     "1",
			sensitive:    nil,
			replaceAttrs: map[string]any{"network_id": types.Int64Value(3), "family": types.StringValue("dhcpv6")},
		},
		{
			name: "dhcp static assignment",
			newValue: func(client *Client) resource.Resource {
				return &dhcpStaticAssignmentResource{configuredResource{client: client}}
			},
			model: func() any { return testDHCPStaticAssignmentModel() },
			update: func(model any) {
				m := model.(*dhcpStaticAssignmentModel)
				m.MAC = types.StringValue("02:00:00:00:00:02")
				m.IP = types.StringValue("10.20.0.51")
			},
			check:        checkDHCPStaticAssignmentState,
			checkUpdated: checkDHCPStaticAssignmentUpdated,
			importID:     "42:1",
			wantVNFID:    42,
			replaceAttrs: map[string]any{"vnf_id": types.Int64Value(43)},
		},
		{
			name:     "dhcp option",
			newValue: func(client *Client) resource.Resource { return &dhcpOptionResource{configuredResource{client: client}} },
			model:    func() any { return testDHCPOptionModel() },
			update: func(model any) {
				m := model.(*dhcpOptionModel)
				m.Code = types.Int64Value(43)
				m.Value = types.StringValue("updated")
			},
			check:        checkDHCPOptionState,
			checkUpdated: checkDHCPOptionUpdated,
			importID:     "42:1",
			wantVNFID:    42,
			sensitive:    []string{"value"},
			replaceAttrs: map[string]any{"vnf_id": types.Int64Value(43)},
		},
		{
			name:     "dns vnf",
			newValue: func(client *Client) resource.Resource { return &dnsVNFResource{configuredResource{client: client}} },
			model:    func() any { return testDNSVNFModel() },
			update: func(model any) {
				m := model.(*dnsVNFModel)
				m.Name = types.StringValue("updated-dns")
				m.Forward = types.BoolValue(false)
			},
			check:        checkDNSVNFState,
			checkUpdated: checkDNSVNFUpdated,
			importID:     "1",
			replaceAttrs: map[string]any{"network_id": types.Int64Value(3)},
		},
		{
			name:     "dns record",
			newValue: func(client *Client) resource.Resource { return &dnsRecordResource{configuredResource{client: client}} },
			model:    func() any { return testDNSRecordModel() },
			update: func(model any) {
				m := model.(*dnsRecordModel)
				m.Value = types.StringValue("10.20.0.2")
				m.TTL = types.Int64Value(60)
			},
			check:        checkDNSRecordState,
			checkUpdated: checkDNSRecordUpdated,
			importID:     "42:1",
			wantVNFID:    42,
			sensitive:    []string{"value"},
			replaceAttrs: map[string]any{"vnf_id": types.Int64Value(43)},
		},
		{
			name: "dns forwarder",
			newValue: func(client *Client) resource.Resource {
				return &dnsForwarderResource{configuredResource{client: client}}
			},
			model: func() any { return testDNSForwarderModel() },
			update: func(model any) {
				m := model.(*dnsForwarderModel)
				m.Address = types.StringValue("10.20.0.3")
				m.Position = types.Int64Value(2)
			},
			check:        checkDNSForwarderState,
			checkUpdated: checkDNSForwarderUpdated,
			importID:     "42:1",
			wantVNFID:    42,
			replaceAttrs: map[string]any{"vnf_id": types.Int64Value(43)},
		},
		{
			name:     "nat44 vnf",
			newValue: func(client *Client) resource.Resource { return &nat44VNFResource{configuredResource{client: client}} },
			model:    func() any { return testNAT44VNFModel() },
			update: func(model any) {
				m := model.(*nat44VNFModel)
				m.Name = types.StringValue("updated-nat")
				m.NAT44Pool = types.StringValue("192.0.2.30-192.0.2.40")
			},
			check:        checkNAT44VNFState,
			checkUpdated: checkNAT44VNFUpdated,
			importID:     "1",
			replaceAttrs: map[string]any{"network_id": types.Int64Value(3)},
		},
		{
			name: "nat44 dnat rule",
			newValue: func(client *Client) resource.Resource {
				return &nat44DNATRuleResource{configuredResource{client: client}}
			},
			model: func() any { return testNAT44DNATRuleModel() },
			update: func(model any) {
				m := model.(*nat44DNATRuleModel)
				m.ExternalPort = types.Int64Value(9090)
				m.LocalPort = types.Int64Value(8081)
			},
			check:        checkNAT44DNATRuleState,
			checkUpdated: checkNAT44DNATRuleUpdated,
			importID:     "42:1",
			wantVNFID:    42,
			replaceAttrs: map[string]any{"vnf_id": types.Int64Value(43)},
		},
		{
			name:     "bgp vnf",
			newValue: func(client *Client) resource.Resource { return &bgpVNFResource{configuredResource{client: client}} },
			model:    func() any { return testBGPVNFModel() },
			update: func(model any) {
				m := model.(*bgpVNFModel)
				m.Name = types.StringValue("updated-bgp")
				m.MultihopTTL = types.Int64Value(32)
			},
			check:        checkBGPVNFState,
			checkUpdated: checkBGPVNFUpdated,
			importID:     "1",
			sensitive:    []string{"password"},
			replaceAttrs: map[string]any{"network_id": types.Int64Value(3)},
		},
		{
			name: "bgp advertised prefix",
			newValue: func(client *Client) resource.Resource {
				return &bgpAdvertisedPrefixResource{configuredResource{client: client}}
			},
			model: func() any { return testBGPAdvertisedPrefixModel() },
			update: func(model any) {
				model.(*bgpAdvertisedPrefixModel).Prefix = types.StringValue("10.30.1.0/24")
			},
			check:        checkBGPAdvertisedPrefixState,
			checkUpdated: checkBGPAdvertisedPrefixUpdated,
			importID:     "42:1",
			wantVNFID:    42,
			replaceAttrs: map[string]any{"vnf_id": types.Int64Value(43)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := newFakeVNFBackend()
			client := backend.client()
			value := test.newValue(client)
			schema := resourceSchema(t, value)
			model := test.model()

			plan := vnfPlanFromModel(t, schema, model)
			createResponse := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
			value.Create(ctx, resource.CreateRequest{Plan: plan, Config: configFromPlan(schema, plan)}, &createResponse)
			if createResponse.Diagnostics.HasError() {
				t.Fatalf("create: %v", createResponse.Diagnostics)
			}
			created := newModelPointer(model)
			if diags := createResponse.State.Get(ctx, created); diags.HasError() {
				t.Fatalf("read created state: %v", diags)
			}
			test.check(t, created)

			readResponse := resource.ReadResponse{State: vnfStateFromModel(t, schema, modelValue(created))}
			value.Read(ctx, resource.ReadRequest{State: vnfStateFromModel(t, schema, modelValue(created))}, &readResponse)
			if readResponse.Diagnostics.HasError() {
				t.Fatalf("read: %v", readResponse.Diagnostics)
			}
			read := newModelPointer(model)
			if diags := readResponse.State.Get(ctx, read); diags.HasError() {
				t.Fatalf("read state: %v", diags)
			}
			test.check(t, read)

			updated := newModelPointer(model)
			if diags := createResponse.State.Get(ctx, updated); diags.HasError() {
				t.Fatalf("copy created state: %v", diags)
			}
			test.update(updated)
			updatePlan := vnfPlanFromModel(t, schema, modelValue(updated))
			updateResponse := resource.UpdateResponse{State: tfsdk.State{Schema: schema}}
			value.Update(ctx, resource.UpdateRequest{
				Plan:   updatePlan,
				Config: configFromPlan(schema, updatePlan),
				State:  vnfStateFromModel(t, schema, modelValue(created)),
			}, &updateResponse)
			if updateResponse.Diagnostics.HasError() {
				t.Fatalf("update: %v", updateResponse.Diagnostics)
			}
			updatedState := newModelPointer(model)
			if diags := updateResponse.State.Get(ctx, updatedState); diags.HasError() {
				t.Fatalf("update state: %v", diags)
			}
			test.checkUpdated(t, updatedState)

			deleteResponse := resource.DeleteResponse{State: tfsdk.State{Schema: schema}}
			value.Delete(ctx, resource.DeleteRequest{State: vnfStateFromModel(t, schema, modelValue(updated))}, &deleteResponse)
			if deleteResponse.Diagnostics.HasError() {
				t.Fatalf("delete: %v", deleteResponse.Diagnostics)
			}

			notFoundResponse := resource.ReadResponse{State: vnfStateFromModel(t, schema, modelValue(updated))}
			value.Read(ctx, resource.ReadRequest{State: vnfStateFromModel(t, schema, modelValue(updated))}, &notFoundResponse)
			if notFoundResponse.Diagnostics.HasError() {
				t.Fatalf("not-found read: %v", notFoundResponse.Diagnostics)
			}
			if !notFoundResponse.State.Raw.IsNull() {
				t.Fatal("not-found read kept the resource in state")
			}

			imported := newModelPointer(model)
			importResponse := resource.ImportStateResponse{State: vnfStateFromModel(t, schema, model)}
			importer, ok := value.(resource.ResourceWithImportState)
			if !ok {
				t.Fatalf("%T does not support import", value)
			}
			importer.ImportState(ctx, resource.ImportStateRequest{ID: test.importID}, &importResponse)
			if importResponse.Diagnostics.HasError() {
				t.Fatalf("import: %v", importResponse.Diagnostics)
			}
			if diags := importResponse.State.Get(ctx, imported); diags.HasError() {
				t.Fatalf("import state: %v", diags)
			}
			if got := modelAttribute(imported, "id").(types.Int64).ValueInt64(); got != 1 {
				t.Fatalf("import id = %d, want 1", got)
			}
			if test.wantVNFID != 0 {
				if got := modelAttribute(imported, "vnf_id").(types.Int64).ValueInt64(); got != test.wantVNFID {
					t.Fatalf("import vnf_id = %d, want %d", got, test.wantVNFID)
				}
			}

			for _, attribute := range test.sensitive {
				if !schema.Attributes[attribute].IsSensitive() {
					t.Fatalf("%q is not sensitive", attribute)
				}
			}
			for attribute, value := range test.replaceAttrs {
				stateModel := test.model()
				planModel := withModelAttribute(t, stateModel, attribute, value)
				switch typed := value.(type) {
				case types.Int64:
					if !int64AttributeRequiresReplace(t, schema, attribute, stateModel, planModel) {
						t.Fatalf("%q does not force replacement", attribute)
					}
					_ = typed
				case types.String:
					if !stringAttributeRequiresReplace(t, schema, attribute, stateModel, planModel) {
						t.Fatalf("%q does not force replacement", attribute)
					}
				default:
					t.Fatalf("unsupported replacement value %T", value)
				}
			}
		})
	}
}

func checkIDAndVNFID(t *testing.T, model any, wantVNFID int64) {
	t.Helper()
	if got := modelAttribute(model, "id").(types.Int64).ValueInt64(); got != 1 {
		t.Fatalf("id = %d, want 1", got)
	}
	if wantVNFID != 0 {
		if got := modelAttribute(model, "vnf_id").(types.Int64).ValueInt64(); got != wantVNFID {
			t.Fatalf("vnf_id = %d, want %d", got, wantVNFID)
		}
	}
}

func checkCommonVNFState(t *testing.T, model any, name string) {
	t.Helper()
	if got := modelAttribute(model, "name").(types.String).ValueString(); got != name {
		t.Fatalf("name = %q, want %q", got, name)
	}
	if got := modelAttribute(model, "network_name").(types.String).ValueString(); got == "" {
		t.Fatal("network_name is empty")
	}
	if got := modelAttribute(model, "status").(types.String).ValueString(); got != "running" {
		t.Fatalf("status = %q, want running", got)
	}
	if got := modelAttribute(model, "created_at").(types.String).ValueString(); got == "" {
		t.Fatal("created_at is empty")
	}
}

func checkDHCPVNFState(t *testing.T, model any) {
	t.Helper()
	checkCommonVNFState(t, model, "app-dhcp")
	m := model.(*dhcpVNFModel)
	if m.Family.ValueString() != "dhcpv4" || m.Gateway.ValueString() != "10.20.0.1/16" ||
		m.StaticAssignmentCount.ValueInt64() != 2 || m.OptionCount.ValueInt64() != 1 || m.LeaseCount.ValueInt64() != 3 {
		t.Fatalf("dhcp state = %+v", m)
	}
}

func checkDHCPVNFUpdated(t *testing.T, model any) {
	t.Helper()
	checkCommonVNFState(t, model, "updated-dhcp")
	if got := model.(*dhcpVNFModel).LeaseTime.ValueString(); got != "24h" {
		t.Fatalf("lease_time = %q, want 24h", got)
	}
}

func checkDNSVNFState(t *testing.T, model any) {
	t.Helper()
	checkCommonVNFState(t, model, "app-dns")
	m := model.(*dnsVNFModel)
	if !m.Forward.ValueBool() || m.RecordCount.ValueInt64() != 2 || m.ForwarderCount.ValueInt64() != 1 {
		t.Fatalf("dns state = %+v", m)
	}
}

func checkDNSVNFUpdated(t *testing.T, model any) {
	t.Helper()
	checkCommonVNFState(t, model, "updated-dns")
	if model.(*dnsVNFModel).Forward.ValueBool() {
		t.Fatal("forward was not updated")
	}
}

func checkNAT44VNFState(t *testing.T, model any) {
	t.Helper()
	checkCommonVNFState(t, model, "app-nat")
	m := model.(*nat44VNFModel)
	if m.OutsideInterface.ValueString() != "eth0" || m.DNATRuleCount.ValueInt64() != 1 {
		t.Fatalf("nat44 state = %+v", m)
	}
}

func checkNAT44VNFUpdated(t *testing.T, model any) {
	t.Helper()
	checkCommonVNFState(t, model, "updated-nat")
	if got := model.(*nat44VNFModel).NAT44Pool.ValueString(); got != "192.0.2.30-192.0.2.40" {
		t.Fatalf("nat44_pool = %q", got)
	}
}

func checkBGPVNFState(t *testing.T, model any) {
	t.Helper()
	checkCommonVNFState(t, model, "app-bgp")
	m := model.(*bgpVNFModel)
	if m.LocalAS.ValueInt64() != 65001 || m.AdvertisedPrefixCount.ValueInt64() != 2 {
		t.Fatalf("bgp state = %+v", m)
	}
	if got := m.Password.ValueString(); got != "secret" {
		t.Fatalf("password = %q, want the configured value", got)
	}
}

func checkBGPVNFUpdated(t *testing.T, model any) {
	t.Helper()
	checkCommonVNFState(t, model, "updated-bgp")
	if got := model.(*bgpVNFModel).MultihopTTL.ValueInt64(); got != 32 {
		t.Fatalf("multihop_ttl = %d, want 32", got)
	}
}

func checkDHCPStaticAssignmentState(t *testing.T, model any) {
	t.Helper()
	checkIDAndVNFID(t, model, 42)
	m := model.(*dhcpStaticAssignmentModel)
	if m.MAC.ValueString() != "02:00:00:00:00:01" || m.IP.ValueString() != "10.20.0.50" {
		t.Fatalf("assignment = %+v", m)
	}
}

func checkDHCPStaticAssignmentUpdated(t *testing.T, model any) {
	t.Helper()
	m := model.(*dhcpStaticAssignmentModel)
	if m.MAC.ValueString() != "02:00:00:00:00:02" || m.IP.ValueString() != "10.20.0.51" {
		t.Fatalf("updated assignment = %+v", m)
	}
}

func checkDHCPOptionState(t *testing.T, model any) {
	t.Helper()
	checkIDAndVNFID(t, model, 42)
	m := model.(*dhcpOptionModel)
	if m.Code.ValueInt64() != 42 || m.Format.ValueString() != "string" || m.Value.ValueString() != "ntp.lab.test" {
		t.Fatalf("option = %+v", m)
	}
}

func checkDHCPOptionUpdated(t *testing.T, model any) {
	t.Helper()
	m := model.(*dhcpOptionModel)
	if m.Code.ValueInt64() != 43 || m.Value.ValueString() != "updated" {
		t.Fatalf("updated option = %+v", m)
	}
}

func checkDNSRecordState(t *testing.T, model any) {
	t.Helper()
	checkIDAndVNFID(t, model, 42)
	m := model.(*dnsRecordModel)
	if m.Name.ValueString() != "router.lab.test" || m.Type.ValueString() != "A" || m.TTL.ValueInt64() != 300 {
		t.Fatalf("record = %+v", m)
	}
}

func checkDNSRecordUpdated(t *testing.T, model any) {
	t.Helper()
	m := model.(*dnsRecordModel)
	if m.Value.ValueString() != "10.20.0.2" || m.TTL.ValueInt64() != 60 {
		t.Fatalf("updated record = %+v", m)
	}
}

func checkDNSForwarderState(t *testing.T, model any) {
	t.Helper()
	checkIDAndVNFID(t, model, 42)
	m := model.(*dnsForwarderModel)
	if m.Address.ValueString() != "10.20.0.2" || m.Position.ValueInt64() != 1 {
		t.Fatalf("forwarder = %+v", m)
	}
}

func checkDNSForwarderUpdated(t *testing.T, model any) {
	t.Helper()
	m := model.(*dnsForwarderModel)
	if m.Address.ValueString() != "10.20.0.3" || m.Position.ValueInt64() != 2 {
		t.Fatalf("updated forwarder = %+v", m)
	}
}

func checkNAT44DNATRuleState(t *testing.T, model any) {
	t.Helper()
	checkIDAndVNFID(t, model, 42)
	m := model.(*nat44DNATRuleModel)
	if m.Protocol.ValueString() != "tcp" || m.ExternalPort.ValueInt64() != 8080 || m.LocalPort.ValueInt64() != 80 {
		t.Fatalf("dnat rule = %+v", m)
	}
}

func checkNAT44DNATRuleUpdated(t *testing.T, model any) {
	t.Helper()
	m := model.(*nat44DNATRuleModel)
	if m.ExternalPort.ValueInt64() != 9090 || m.LocalPort.ValueInt64() != 8081 {
		t.Fatalf("updated dnat rule = %+v", m)
	}
}

func checkBGPAdvertisedPrefixState(t *testing.T, model any) {
	t.Helper()
	checkIDAndVNFID(t, model, 42)
	if got := model.(*bgpAdvertisedPrefixModel).Prefix.ValueString(); got != "10.30.0.0/24" {
		t.Fatalf("prefix = %q", got)
	}
}

func checkBGPAdvertisedPrefixUpdated(t *testing.T, model any) {
	t.Helper()
	if got := model.(*bgpAdvertisedPrefixModel).Prefix.ValueString(); got != "10.30.1.0/24" {
		t.Fatalf("updated prefix = %q", got)
	}
}

func stringAttributeRequiresReplace(t *testing.T, schema rschema.Schema, attribute string, stateModel, planModel any) bool {
	t.Helper()
	attr, ok := schema.Attributes[attribute].(rschema.StringAttribute)
	if !ok {
		t.Fatalf("%q is not a StringAttribute", attribute)
	}
	state := vnfStateFromModel(t, schema, stateModel)
	plan := vnfPlanFromModel(t, schema, planModel)
	stateValue, _ := modelAttribute(stateModel, attribute).(types.String)
	planValue, _ := modelAttribute(planModel, attribute).(types.String)
	req := planmodifier.StringRequest{
		State:       state,
		Plan:        plan,
		Config:      tfsdk.Config{Schema: schema, Raw: plan.Raw},
		StateValue:  stateValue,
		PlanValue:   planValue,
		ConfigValue: planValue,
	}
	resp := &planmodifier.StringResponse{}
	for _, modifier := range attr.PlanModifiers {
		modifier.PlanModifyString(context.Background(), req, resp)
	}
	return resp.RequiresReplace
}

// --- data sources ---

type vnfDataSourceTestCase struct {
	name     string
	typeName string
	newValue func(*Client) datasource.DataSource
	model    func() any
	seedName string
}

func TestVNFDataSourceRead(t *testing.T) {
	ctx := context.Background()
	tests := []vnfDataSourceTestCase{
		{
			name:     "dhcp vnf",
			typeName: "rookery_dhcp_vnf",
			newValue: func(client *Client) datasource.DataSource {
				return &dhcpVNFDataSource{configuredDataSource{client: client}}
			},
			model:    func() any { return dhcpVNFModel{Labels: types.SetNull(types.StringType)} },
			seedName: "app-dhcp",
		},
		{
			name:     "dns vnf",
			typeName: "rookery_dns_vnf",
			newValue: func(client *Client) datasource.DataSource {
				return &dnsVNFDataSource{configuredDataSource{client: client}}
			},
			model:    func() any { return dnsVNFModel{Labels: types.SetNull(types.StringType)} },
			seedName: "app-dns",
		},
		{
			name:     "nat44 vnf",
			typeName: "rookery_nat44_vnf",
			newValue: func(client *Client) datasource.DataSource {
				return &nat44VNFDataSource{configuredDataSource{client: client}}
			},
			model:    func() any { return nat44VNFModel{Labels: types.SetNull(types.StringType)} },
			seedName: "app-nat",
		},
		{
			name:     "bgp vnf",
			typeName: "rookery_bgp_vnf",
			newValue: func(client *Client) datasource.DataSource {
				return &bgpVNFDataSource{configuredDataSource{client: client}}
			},
			model:    func() any { return bgpVNFDataModel{Labels: types.SetNull(types.StringType)} },
			seedName: "app-bgp",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := newFakeVNFBackend()
			backend.seed()
			value := test.newValue(backend.client())

			var metadata datasource.MetadataResponse
			value.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "rookery"}, &metadata)
			if metadata.TypeName != test.typeName {
				t.Fatalf("metadata type = %q, want %q", metadata.TypeName, test.typeName)
			}

			var schemaResponse datasource.SchemaResponse
			value.Schema(ctx, datasource.SchemaRequest{}, &schemaResponse)
			if schemaResponse.Diagnostics.HasError() {
				t.Fatalf("schema: %v", schemaResponse.Diagnostics)
			}
			schema := schemaResponse.Schema

			byID := withModelAttribute(t, test.model(), "id", types.Int64Value(7))
			byIDPlan := tfsdk.Plan{Schema: schema}
			if diags := byIDPlan.Set(ctx, byID); diags.HasError() {
				t.Fatalf("set by-id config: %v", diags)
			}
			byIDResponse := datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
			value.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schema, Raw: byIDPlan.Raw}}, &byIDResponse)
			if byIDResponse.Diagnostics.HasError() {
				t.Fatalf("read by id: %v", byIDResponse.Diagnostics)
			}
			byIDModel := newModelPointer(test.model())
			if diags := byIDResponse.State.Get(ctx, byIDModel); diags.HasError() {
				t.Fatalf("by-id state: %v", diags)
			}
			checkDataSourceState(t, byIDModel, test.seedName)

			byName := withModelAttribute(t, test.model(), "name", types.StringValue(strings.ToUpper(test.seedName)))
			byNamePlan := tfsdk.Plan{Schema: schema}
			if diags := byNamePlan.Set(ctx, byName); diags.HasError() {
				t.Fatalf("set by-name config: %v", diags)
			}
			byNameResponse := datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
			value.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schema, Raw: byNamePlan.Raw}}, &byNameResponse)
			if byNameResponse.Diagnostics.HasError() {
				t.Fatalf("read by name: %v", byNameResponse.Diagnostics)
			}
			byNameModel := newModelPointer(test.model())
			if diags := byNameResponse.State.Get(ctx, byNameModel); diags.HasError() {
				t.Fatalf("by-name state: %v", diags)
			}
			checkDataSourceState(t, byNameModel, test.seedName)

			both := withModelAttribute(t, test.model(), "id", types.Int64Value(7))
			both = withModelAttribute(t, both, "name", types.StringValue(test.seedName))
			bothPlan := tfsdk.Plan{Schema: schema}
			if diags := bothPlan.Set(ctx, both); diags.HasError() {
				t.Fatalf("set both config: %v", diags)
			}
			bothResponse := datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
			value.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schema, Raw: bothPlan.Raw}}, &bothResponse)
			if !bothResponse.Diagnostics.HasError() {
				t.Fatal("both id and name accepted")
			}

			missingID := withModelAttribute(t, test.model(), "id", types.Int64Value(99))
			missingIDPlan := tfsdk.Plan{Schema: schema}
			if diags := missingIDPlan.Set(ctx, missingID); diags.HasError() {
				t.Fatalf("set missing-id config: %v", diags)
			}
			missingIDResponse := datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
			value.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schema, Raw: missingIDPlan.Raw}}, &missingIDResponse)
			if !missingIDResponse.Diagnostics.HasError() {
				t.Fatal("missing id accepted")
			}

			missingName := withModelAttribute(t, test.model(), "name", types.StringValue("missing"))
			missingNamePlan := tfsdk.Plan{Schema: schema}
			if diags := missingNamePlan.Set(ctx, missingName); diags.HasError() {
				t.Fatalf("set missing-name config: %v", diags)
			}
			missingNameResponse := datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
			value.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schema, Raw: missingNamePlan.Raw}}, &missingNameResponse)
			if !missingNameResponse.Diagnostics.HasError() {
				t.Fatal("missing name accepted")
			}
		})
	}
}

func checkDataSourceState(t *testing.T, model any, name string) {
	t.Helper()
	if got := modelAttribute(model, "id").(types.Int64).ValueInt64(); got != 7 {
		t.Fatalf("id = %d, want 7", got)
	}
	if got := modelAttribute(model, "name").(types.String).ValueString(); got != name {
		t.Fatalf("name = %q, want %q", got, name)
	}
	labels, ok := modelAttribute(model, "labels").(types.Set)
	if !ok || labels.IsNull() || len(labels.Elements()) == 0 {
		t.Fatalf("labels = %#v, want the seeded label", modelAttribute(model, "labels"))
	}
}

func TestChildImportIDs(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantVNF int64
		wantID  int64
		wantErr bool
	}{
		{name: "valid", value: "7:3", wantVNF: 7, wantID: 3},
		{name: "spaces", value: " 7 : 3 ", wantVNF: 7, wantID: 3},
		{name: "missing child", value: "7", wantErr: true},
		{name: "too many parts", value: "7:3:1", wantErr: true},
		{name: "zero vnf", value: "0:3", wantErr: true},
		{name: "non numeric child", value: "7:x", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			vnfID, childID, err := childImportIDs(test.value)
			if test.wantErr {
				if err == nil {
					t.Fatalf("childImportIDs(%q) accepted", test.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("childImportIDs(%q): %v", test.value, err)
			}
			if vnfID != test.wantVNF || childID != test.wantID {
				t.Fatalf("childImportIDs(%q) = %d, %d, want %d, %d", test.value, vnfID, childID, test.wantVNF, test.wantID)
			}
		})
	}
}
