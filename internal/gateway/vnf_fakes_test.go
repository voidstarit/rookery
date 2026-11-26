package gateway_test

import (
	"context"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/store"
)

// fakeVNFRuntime returns one lease for every VNF.
type fakeVNFRuntime struct{}

func (fakeVNFRuntime) Leases(context.Context, int64) ([]store.VNFLease, error) {
	return []store.VNFLease{{
		ClientID: "02:00:00:00:00:01", Address: "10.20.0.150", Hostname: "host",
		Active: true,
	}}, nil
}

// fakeVNFStatusProvider reports a running VNF with two leases and canned
// dynamic DNS, NAT44, and BGP views.
type fakeVNFStatusProvider struct {
	sessions   []string
	bgp        host.BGPStatus
	dnsError   error
	dnsRecords []host.DNSRecordView
}

func (f fakeVNFStatusProvider) VNFStatus(context.Context, store.VNF) (string, string, int) {
	return "running", "", 2
}

func (f fakeVNFStatusProvider) VNFSessions(context.Context, store.VNF) ([]string, error) {
	if f.sessions == nil {
		return []string{}, nil
	}
	return f.sessions, nil
}

func (f fakeVNFStatusProvider) VNFBGP(context.Context, store.VNF) (host.BGPStatus, error) {
	return f.bgp, nil
}

func (f fakeVNFStatusProvider) VNFDNSRecords(context.Context, store.VNF) ([]host.DNSRecordView, error) {
	if f.dnsError != nil {
		return nil, f.dnsError
	}
	return f.dnsRecords, nil
}

// fakeVNFWriter records the last parent create and returns canned rows.
type fakeVNFWriter struct {
	err         error
	id          int64
	createdDHCP *rookeryv1.CreateDHCPVNFRequest
	deleted     int64
}

func (f *fakeVNFWriter) parent(id int64, vnfType string) (store.VNF, error) {
	if f.err != nil {
		return store.VNF{}, f.err
	}
	if f.id != 0 {
		id = f.id
	}
	return store.VNF{ID: id, Type: vnfType, Name: "fake", Enabled: true}, nil
}

func (f *fakeVNFWriter) CreateDHCPVNF(_ context.Context, request *rookeryv1.CreateDHCPVNFRequest) (store.VNF, error) {
	f.createdDHCP = request
	return f.parent(11, store.VNFTypeDHCPv4)
}
func (f *fakeVNFWriter) UpdateDHCPVNF(context.Context, *rookeryv1.UpdateDHCPVNFRequest) (store.VNF, error) {
	return f.parent(11, store.VNFTypeDHCPv4)
}
func (f *fakeVNFWriter) DeleteDHCPVNF(_ context.Context, request *rookeryv1.DeleteDHCPVNFRequest) error {
	f.deleted = request.GetId()
	return f.err
}
func (f *fakeVNFWriter) RestartDHCPVNF(context.Context, *rookeryv1.RestartDHCPVNFRequest) (store.VNF, error) {
	return f.parent(11, store.VNFTypeDHCPv4)
}
func (f *fakeVNFWriter) CreateDHCPStaticAssignment(_ context.Context, request *rookeryv1.CreateDHCPStaticAssignmentRequest) (store.DHCPStaticAssignment, error) {
	if f.err != nil {
		return store.DHCPStaticAssignment{}, f.err
	}
	return store.DHCPStaticAssignment{ID: 21, VNFID: request.GetVnfId(), MAC: request.GetMac(), IP: request.GetIp()}, nil
}
func (f *fakeVNFWriter) UpdateDHCPStaticAssignment(_ context.Context, request *rookeryv1.UpdateDHCPStaticAssignmentRequest) (store.DHCPStaticAssignment, error) {
	return store.DHCPStaticAssignment{ID: request.GetAssignmentId(), VNFID: request.GetVnfId(), MAC: request.GetMac(), IP: request.GetIp()}, nil
}
func (f *fakeVNFWriter) DeleteDHCPStaticAssignment(context.Context, *rookeryv1.DeleteDHCPStaticAssignmentRequest) error {
	return f.err
}
func (f *fakeVNFWriter) CreateDHCPOption(_ context.Context, request *rookeryv1.CreateDHCPOptionRequest) (store.DHCPOption, error) {
	return store.DHCPOption{ID: 31, VNFID: request.GetVnfId(), Code: uint16(request.GetCode()), Format: request.GetFormat(), Value: request.GetValue()}, nil
}
func (f *fakeVNFWriter) UpdateDHCPOption(_ context.Context, request *rookeryv1.UpdateDHCPOptionRequest) (store.DHCPOption, error) {
	return store.DHCPOption{ID: request.GetOptionId(), VNFID: request.GetVnfId(), Code: uint16(request.GetCode())}, nil
}
func (f *fakeVNFWriter) DeleteDHCPOption(context.Context, *rookeryv1.DeleteDHCPOptionRequest) error {
	return f.err
}

func (f *fakeVNFWriter) CreateDNSVNF(context.Context, *rookeryv1.CreateDNSVNFRequest) (store.VNF, error) {
	return f.parent(12, store.VNFTypeDNS)
}
func (f *fakeVNFWriter) UpdateDNSVNF(context.Context, *rookeryv1.UpdateDNSVNFRequest) (store.VNF, error) {
	return f.parent(12, store.VNFTypeDNS)
}
func (f *fakeVNFWriter) DeleteDNSVNF(context.Context, *rookeryv1.DeleteDNSVNFRequest) error {
	return f.err
}
func (f *fakeVNFWriter) RestartDNSVNF(context.Context, *rookeryv1.RestartDNSVNFRequest) (store.VNF, error) {
	return f.parent(12, store.VNFTypeDNS)
}
func (f *fakeVNFWriter) CreateDNSVNFRecord(_ context.Context, request *rookeryv1.CreateDNSVNFRecordRequest) (store.DNSRecord, error) {
	return store.DNSRecord{ID: 41, VNFID: request.GetVnfId(), Name: request.GetName(), Type: request.GetType(), Value: request.GetValue(), TTL: request.GetTtl()}, nil
}
func (f *fakeVNFWriter) UpdateDNSVNFRecord(_ context.Context, request *rookeryv1.UpdateDNSVNFRecordRequest) (store.DNSRecord, error) {
	return store.DNSRecord{ID: request.GetRecordId(), VNFID: request.GetVnfId(), Name: request.GetName()}, nil
}
func (f *fakeVNFWriter) DeleteDNSVNFRecord(context.Context, *rookeryv1.DeleteDNSVNFRecordRequest) error {
	return f.err
}
func (f *fakeVNFWriter) CreateDNSVNFForwarder(_ context.Context, request *rookeryv1.CreateDNSVNFForwarderRequest) (store.DNSForwarder, error) {
	return store.DNSForwarder{ID: 51, VNFID: request.GetVnfId(), Address: request.GetAddress(), Position: request.GetPosition()}, nil
}
func (f *fakeVNFWriter) UpdateDNSVNFForwarder(_ context.Context, request *rookeryv1.UpdateDNSVNFForwarderRequest) (store.DNSForwarder, error) {
	return store.DNSForwarder{ID: request.GetForwarderId(), VNFID: request.GetVnfId(), Address: request.GetAddress()}, nil
}
func (f *fakeVNFWriter) DeleteDNSVNFForwarder(context.Context, *rookeryv1.DeleteDNSVNFForwarderRequest) error {
	return f.err
}

func (f *fakeVNFWriter) CreateNAT44VNF(context.Context, *rookeryv1.CreateNAT44VNFRequest) (store.VNF, error) {
	return f.parent(13, store.VNFTypeNAT44)
}
func (f *fakeVNFWriter) UpdateNAT44VNF(context.Context, *rookeryv1.UpdateNAT44VNFRequest) (store.VNF, error) {
	return f.parent(13, store.VNFTypeNAT44)
}
func (f *fakeVNFWriter) DeleteNAT44VNF(context.Context, *rookeryv1.DeleteNAT44VNFRequest) error {
	return f.err
}
func (f *fakeVNFWriter) RestartNAT44VNF(context.Context, *rookeryv1.RestartNAT44VNFRequest) (store.VNF, error) {
	return f.parent(13, store.VNFTypeNAT44)
}
func (f *fakeVNFWriter) CreateNAT44DNATRule(_ context.Context, request *rookeryv1.CreateNAT44DNATRuleRequest) (store.DNATRule, error) {
	return store.DNATRule{ID: 61, VNFID: request.GetVnfId(), Protocol: request.GetProtocol(), ExternalPort: int(request.GetExternalPort()), LocalIP: request.GetLocalIp(), LocalPort: int(request.GetLocalPort())}, nil
}
func (f *fakeVNFWriter) UpdateNAT44DNATRule(_ context.Context, request *rookeryv1.UpdateNAT44DNATRuleRequest) (store.DNATRule, error) {
	return store.DNATRule{ID: request.GetRuleId(), VNFID: request.GetVnfId(), Protocol: request.GetProtocol()}, nil
}
func (f *fakeVNFWriter) DeleteNAT44DNATRule(context.Context, *rookeryv1.DeleteNAT44DNATRuleRequest) error {
	return f.err
}

func (f *fakeVNFWriter) CreateBGPVNF(context.Context, *rookeryv1.CreateBGPVNFRequest) (store.VNF, error) {
	return f.parent(14, store.VNFTypeBGP)
}
func (f *fakeVNFWriter) UpdateBGPVNF(context.Context, *rookeryv1.UpdateBGPVNFRequest) (store.VNF, error) {
	return f.parent(14, store.VNFTypeBGP)
}
func (f *fakeVNFWriter) DeleteBGPVNF(context.Context, *rookeryv1.DeleteBGPVNFRequest) error {
	return f.err
}
func (f *fakeVNFWriter) RestartBGPVNF(context.Context, *rookeryv1.RestartBGPVNFRequest) (store.VNF, error) {
	return f.parent(14, store.VNFTypeBGP)
}
func (f *fakeVNFWriter) CreateBGPVNFAdvertisedPrefix(_ context.Context, request *rookeryv1.CreateBGPVNFAdvertisedPrefixRequest) (store.BGPPrefix, error) {
	return store.BGPPrefix{ID: 71, VNFID: request.GetVnfId(), Prefix: request.GetPrefix()}, nil
}
func (f *fakeVNFWriter) UpdateBGPVNFAdvertisedPrefix(_ context.Context, request *rookeryv1.UpdateBGPVNFAdvertisedPrefixRequest) (store.BGPPrefix, error) {
	return store.BGPPrefix{ID: request.GetPrefixId(), VNFID: request.GetVnfId(), Prefix: request.GetPrefix()}, nil
}
func (f *fakeVNFWriter) DeleteBGPVNFAdvertisedPrefix(context.Context, *rookeryv1.DeleteBGPVNFAdvertisedPrefixRequest) error {
	return f.err
}
