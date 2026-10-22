package gateway

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

func timestamp(value time.Time) *timestamppb.Timestamp {
	if value.IsZero() {
		return nil
	}
	return timestamppb.New(value)
}

func storeReadError(err error, notFound error, message string) error {
	if errors.Is(err, notFound) {
		return status.Error(codes.NotFound, message)
	}
	return status.Error(codes.Internal, message)
}

type diskService struct {
	rookeryv1.UnimplementedDiskServiceServer
	store     *store.Store
	runtime   DiskProvisioner
	readiness Readiness
	mu        *sync.Mutex
	logger    zerolog.Logger
}

func (s *diskService) ListDisks(ctx context.Context, _ *rookeryv1.ListDisksRequest) (*rookeryv1.ListDisksResponse, error) {
	disks, err := s.store.ListDisks(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list disks")
	}
	response := &rookeryv1.ListDisksResponse{Disks: make([]*rookeryv1.Disk, 0, len(disks))}
	for _, disk := range disks {
		response.Disks = append(response.Disks, diskToProto(disk))
	}
	return response, nil
}

func (s *diskService) GetDisk(ctx context.Context, request *rookeryv1.GetDiskRequest) (*rookeryv1.Disk, error) {
	disk, err := s.store.GetDisk(ctx, request.GetId())
	if err != nil {
		return nil, storeReadError(err, store.ErrDiskNotFound, "disk not found")
	}
	return diskToProto(disk), nil
}

func diskToProto(disk store.Disk) *rookeryv1.Disk {
	return &rookeryv1.Disk{
		Id: disk.ID, Name: disk.Name, SizeGb: int32(disk.SizeGB),
		StorageBackendId: disk.StorageBackendID, StorageBackendName: disk.StorageBackendName,
		Vg: disk.VG, ThinPool: disk.ThinPool, Device: disk.Device,
		IsTemplate: disk.IsTemplate, TemplateDiskId: disk.TemplateDiskID, TemplateDiskName: disk.TemplateDiskName,
		AttachedVmId: disk.AttachedVMID, AttachedVmName: disk.AttachedVMName,
		CreatedAt: timestamp(disk.CreatedAt), UpdatedAt: timestamp(disk.UpdatedAt),
	}
}

type mediaService struct {
	rookeryv1.UnimplementedMediaServiceServer
	store *store.Store
}

func (s *mediaService) ListMedia(ctx context.Context, _ *rookeryv1.ListMediaRequest) (*rookeryv1.ListMediaResponse, error) {
	media, err := s.store.ListMedia(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list media")
	}
	response := &rookeryv1.ListMediaResponse{Media: make([]*rookeryv1.Media, 0, len(media))}
	for _, item := range media {
		response.Media = append(response.Media, mediaToProto(item))
	}
	return response, nil
}

func (s *mediaService) GetMedia(ctx context.Context, request *rookeryv1.GetMediaRequest) (*rookeryv1.Media, error) {
	item, err := s.store.GetMedia(ctx, request.GetId())
	if err != nil {
		return nil, storeReadError(err, store.ErrMediaNotFound, "media not found")
	}
	return mediaToProto(item), nil
}

func mediaToProto(item store.Media) *rookeryv1.Media {
	return &rookeryv1.Media{
		Id: item.ID, Name: item.Name, Path: item.Path, SizeBytes: item.SizeBytes,
		CreatedAt: timestamp(item.CreatedAt), UpdatedAt: timestamp(item.UpdatedAt),
	}
}

type networkService struct {
	rookeryv1.UnimplementedNetworkServiceServer
	store     *store.Store
	runtime   NetworkRuntime
	readiness Readiness
	writer    NetworkWriter
	mu        *sync.Mutex
}

func (s *networkService) ListNetworks(ctx context.Context, _ *rookeryv1.ListNetworksRequest) (*rookeryv1.ListNetworksResponse, error) {
	networks, err := s.store.ListNetworks(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list networks")
	}
	response := &rookeryv1.ListNetworksResponse{Networks: make([]*rookeryv1.Network, 0, len(networks))}
	for _, network := range networks {
		response.Networks = append(response.Networks, networkToProto(network))
	}
	return response, nil
}

func (s *networkService) GetNetwork(ctx context.Context, request *rookeryv1.GetNetworkRequest) (*rookeryv1.Network, error) {
	network, err := s.store.GetNetwork(ctx, request.GetId())
	if err != nil {
		return nil, storeReadError(err, store.ErrNetworkNotFound, "network not found")
	}
	return networkToProto(network), nil
}

func networkToProto(network store.Network) *rookeryv1.Network {
	return &rookeryv1.Network{
		Id: network.ID, Name: network.Name, BridgeDomainId: network.BridgeDomainID,
		InterfaceCount: int32(network.InterfaceCount), BviHostName: network.BVIHostName,
		BviSwIfIndex: network.BVISwIfIndex, Ipv4Gateway: network.IPv4Gateway,
		Ipv4Cidr: network.IPv4CIDR, Ipv6Cidr: network.IPv6CIDR,
		RouteTableId: network.RouteTableID,
		CreatedAt:    timestamp(network.CreatedAt), UpdatedAt: timestamp(network.UpdatedAt),
	}
}

type interfaceService struct {
	rookeryv1.UnimplementedInterfaceServiceServer
	store       *store.Store
	runtime     ACLRuntime
	attachments InterfaceRuntime
	vms         VMController
	counters    InterfaceCounterSource
	readiness   Readiness
	resourceMu  *sync.Mutex
	runMu       *sync.Mutex
}

func (s *interfaceService) ListInterfaces(ctx context.Context, _ *rookeryv1.ListInterfacesRequest) (*rookeryv1.ListInterfacesResponse, error) {
	interfaces, err := s.store.ListInterfaces(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list interfaces")
	}
	response := &rookeryv1.ListInterfacesResponse{Interfaces: make([]*rookeryv1.VirtualInterface, 0, len(interfaces))}
	for _, iface := range interfaces {
		response.Interfaces = append(response.Interfaces, interfaceToProto(iface))
	}
	return response, nil
}

// ListInterfaceCounters returns cumulative VPP RX/TX counters for every
// registered virtual interface. Missing names (for example interfaces that
// VPP has not recreated yet after a restart) are omitted.
func (s *interfaceService) ListInterfaceCounters(ctx context.Context, _ *rookeryv1.ListInterfaceCountersRequest) (*rookeryv1.ListInterfaceCountersResponse, error) {
	if s.counters == nil {
		return nil, status.Error(codes.Unavailable, "VPP network runtime is not configured")
	}
	if err := s.vppReady(); err != nil {
		return nil, err
	}
	interfaces, err := s.store.ListInterfaces(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list interfaces")
	}
	names := make([]string, 0, len(interfaces))
	for _, iface := range interfaces {
		names = append(names, iface.Name)
	}
	counters, err := s.counters.InterfaceCounters(ctx, names)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not read VPP interface counters: "+err.Error())
	}
	sampledAt := timestamppb.New(time.Now().UTC())
	response := &rookeryv1.ListInterfaceCountersResponse{}
	for _, iface := range interfaces {
		counter, ok := counters[iface.Name]
		if !ok {
			continue
		}
		response.Interfaces = append(response.Interfaces, &rookeryv1.InterfaceCounters{
			Name: iface.Name, SwIfIndex: iface.SwIfIndex,
			RxPackets: counter.RXPackets, TxPackets: counter.TXPackets,
			RxBytes: counter.RXBytes, TxBytes: counter.TXBytes,
			SampledAt: sampledAt,
		})
	}
	return response, nil
}

func (s *interfaceService) GetInterface(ctx context.Context, request *rookeryv1.GetInterfaceRequest) (*rookeryv1.VirtualInterface, error) {
	iface, err := s.store.GetInterface(ctx, request.GetId())
	if err != nil {
		return nil, storeReadError(err, store.ErrInterfaceNotFound, "interface not found")
	}
	return interfaceToProto(iface), nil
}

func interfaceToProto(iface store.VirtualInterface) *rookeryv1.VirtualInterface {
	return &rookeryv1.VirtualInterface{
		Id: iface.ID, Name: iface.Name, NetworkId: iface.NetworkID, NetworkName: iface.NetworkName,
		SocketPath: iface.SocketPath, Mac: iface.MAC, SwIfIndex: iface.SwIfIndex,
		AttachedVmId: iface.AttachedVMID, AttachedVmName: iface.AttachedVMName,
		IngressAclId: iface.IngressACLID, IngressAclName: iface.IngressACLName,
		EgressAclId: iface.EgressACLID, EgressAclName: iface.EgressACLName,
		IngressAcls: interfaceACLNames(iface.IngressACLs),
		EgressAcls:  interfaceACLNames(iface.EgressACLs),
		Ipv4TableId: iface.IPv4TableID, Ipv6TableId: iface.IPv6TableID,
		Ipv4TableName: iface.IPv4TableName, Ipv6TableName: iface.IPv6TableName,
		CreatedAt: timestamp(iface.CreatedAt), UpdatedAt: timestamp(iface.UpdatedAt),
	}
}

func interfaceACLNames(refs []store.InterfaceACL) []string {
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		names = append(names, ref.Name)
	}
	return names
}

type aclService struct {
	rookeryv1.UnimplementedACLServiceServer
	store     *store.Store
	runtime   ACLRuntime
	readiness Readiness
}

func (s *aclService) ListACLs(ctx context.Context, _ *rookeryv1.ListACLsRequest) (*rookeryv1.ListACLsResponse, error) {
	acls, err := s.store.ListACLs(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list ACLs")
	}
	response := &rookeryv1.ListACLsResponse{Acls: make([]*rookeryv1.ACL, 0, len(acls))}
	for _, acl := range acls {
		response.Acls = append(response.Acls, aclToProto(acl))
	}
	return response, nil
}

func (s *aclService) GetACL(ctx context.Context, request *rookeryv1.GetACLRequest) (*rookeryv1.ACL, error) {
	acl, err := s.store.GetACL(ctx, request.GetId())
	if err != nil {
		return nil, storeReadError(err, store.ErrACLNotFound, "acl not found")
	}
	return aclToProto(acl), nil
}

func aclToProto(acl store.ACL) *rookeryv1.ACL {
	rules := make([]*rookeryv1.ACLRule, 0, len(acl.Rules))
	for _, rule := range acl.Rules {
		rules = append(rules, &rookeryv1.ACLRule{
			Action: rule.Action, Description: rule.Description, Protocol: rule.Protocol,
			Source: rule.Source, Destination: rule.Destination,
			SourcePortStart: int32(rule.SourcePortStart), SourcePortEnd: int32(rule.SourcePortEnd),
			DestinationPortStart: int32(rule.DestinationPortStart), DestinationPortEnd: int32(rule.DestinationPortEnd),
		})
	}
	return &rookeryv1.ACL{
		Id: acl.ID, Name: acl.Name, Title: acl.Title, Description: acl.Description,
		Rules: rules, VppIndex: acl.VPPIndex,
		CreatedAt: timestamp(acl.CreatedAt), UpdatedAt: timestamp(acl.UpdatedAt),
	}
}

type vnfService struct {
	rookeryv1.UnimplementedVNFServiceServer
	store          *store.Store
	runtime        VNFRuntime
	statusProvider VNFStatusProvider
	writer         VNFWriter
}

func (s *vnfService) ListVNFs(ctx context.Context, request *rookeryv1.ListVNFsRequest) (*rookeryv1.ListVNFsResponse, error) {
	vnfs, err := s.store.ListVNFs(ctx, request.NetworkId)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list VNFs")
	}
	response := &rookeryv1.ListVNFsResponse{Vnfs: make([]*rookeryv1.VNF, 0, len(vnfs))}
	for _, vnf := range vnfs {
		response.Vnfs = append(response.Vnfs, s.vnfView(ctx, vnf))
	}
	return response, nil
}

func (s *vnfService) GetVNF(ctx context.Context, request *rookeryv1.GetVNFRequest) (*rookeryv1.VNF, error) {
	vnf, err := s.store.GetVNF(ctx, request.GetId())
	if err != nil {
		return nil, storeReadError(err, store.ErrVNFNotFound, "vnf not found")
	}
	return s.vnfView(ctx, vnf), nil
}

func vnfToProto(vnf store.VNF) *rookeryv1.VNF {
	static := make([]*rookeryv1.DHCPStaticAssignment, 0, len(vnf.Config.StaticAssignments))
	for _, assignment := range vnf.Config.StaticAssignments {
		static = append(static, &rookeryv1.DHCPStaticAssignment{Mac: assignment.MAC, Ip: assignment.IP})
	}
	dnat := make([]*rookeryv1.DNATRule, 0, len(vnf.Config.DNATRules))
	for _, rule := range vnf.Config.DNATRules {
		dnat = append(dnat, &rookeryv1.DNATRule{
			Protocol: rule.Protocol, ExternalPort: int32(rule.ExternalPort),
			LocalIp: rule.LocalIP, LocalPort: int32(rule.LocalPort),
		})
	}
	dnsRecords := make([]*rookeryv1.DNSRecord, 0, len(vnf.Config.DNSRecords))
	for _, record := range vnf.Config.DNSRecords {
		dnsRecords = append(dnsRecords, &rookeryv1.DNSRecord{
			Name: record.Name, Type: record.Type, Value: record.Value, Ttl: record.TTL,
		})
	}
	dhcpOptions := make([]*rookeryv1.DHCPOption, 0, len(vnf.Config.DHCPOptions))
	for _, option := range vnf.Config.DHCPOptions {
		dhcpOptions = append(dhcpOptions, &rookeryv1.DHCPOption{
			Code: uint32(option.Code), Format: option.Format, Value: option.Value,
		})
	}
	return &rookeryv1.VNF{
		Id: vnf.ID, Name: vnf.Name, NetworkId: vnf.NetworkID, NetworkName: vnf.NetworkName,
		Type: vnf.Type, Enabled: vnf.Enabled,
		Config: &rookeryv1.VNFConfig{
			Gateway: vnf.Config.Gateway, RangeStart: vnf.Config.RangeStart, RangeEnd: vnf.Config.RangeEnd,
			Dns: vnf.Config.DNS, LeaseTime: vnf.Config.LeaseTime, BindIp: vnf.Config.BindIP,
			StaticAssignments: static, OutsideInterface: vnf.Config.OutsideInterface,
			OutsideAddress: vnf.Config.OutsideAddress, OutsideGateway: vnf.Config.OutsideGateway,
			Nat44Pool: vnf.Config.NAT44Pool, DnatRules: dnat,
			Transport: vnf.Config.Transport, LocalAs: vnf.Config.LocalAS, RouterId: vnf.Config.RouterID,
			PeerAs: vnf.Config.PeerAS, PeerIp: vnf.Config.PeerIP, LocalIp: vnf.Config.LocalIP,
			PeerGateway: vnf.Config.PeerGateway, MultihopTtl: vnf.Config.MultihopTTL,
			Password: vnf.Config.Password, Passive: vnf.Config.Passive, TargetTableId: vnf.Config.TargetTableID,
			AdvertisePrefixes: append([]string(nil), vnf.Config.AdvertisePrefixes...),
			NextHop:           vnf.Config.NextHop,
			DnsRecords:        dnsRecords,
			DnsForward:        vnf.Config.DNSForward,
			DnsForwarders:     append([]string(nil), vnf.Config.DNSForwarders...),
			DhcpOptions:       dhcpOptions,
		},
		TapSwIfIndex: vnf.TapSwIfIndex, TapHostName: vnf.TapHostName,
		CreatedAt: timestamp(vnf.CreatedAt), UpdatedAt: timestamp(vnf.UpdatedAt),
	}
}

type storageBackendService struct {
	rookeryv1.UnimplementedStorageBackendServiceServer
	store     *store.Store
	readiness Readiness
	disks     DiskProvisioner
	mu        *sync.Mutex
}

func (s *storageBackendService) ListStorageBackends(ctx context.Context, _ *rookeryv1.ListStorageBackendsRequest) (*rookeryv1.ListStorageBackendsResponse, error) {
	backends, err := s.store.ListStorageBackends(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list storage backends")
	}
	response := &rookeryv1.ListStorageBackendsResponse{Backends: make([]*rookeryv1.StorageBackend, 0, len(backends))}
	for _, backend := range backends {
		response.Backends = append(response.Backends, storageBackendToProto(backend))
	}
	return response, nil
}

func (s *storageBackendService) GetStorageBackend(ctx context.Context, request *rookeryv1.GetStorageBackendRequest) (*rookeryv1.StorageBackend, error) {
	backend, err := s.store.GetStorageBackend(ctx, request.GetId())
	if err != nil {
		return nil, storeReadError(err, store.ErrStorageBackendNotFound, "storage backend not found")
	}
	return storageBackendToProto(backend), nil
}

func storageBackendToProto(backend store.StorageBackend) *rookeryv1.StorageBackend {
	return &rookeryv1.StorageBackend{
		Id: backend.ID, Name: backend.Name, Title: backend.Title, Description: backend.Description,
		Driver: backend.Driver,
		Config: &rookeryv1.StorageBackendConfig{
			Vg: backend.Config.VG, ThinPool: backend.Config.ThinPool,
			SupportsTemplates:             backend.Config.SupportsTemplates,
			PreparationScript:             backend.Config.PreparationScript,
			PreparationTimeout:            backend.Config.PreparationTimeout,
			AllowTemplateDeleteWithClones: backend.Config.AllowTemplateDeleteWithClones,
		},
		DiskCount: int32(backend.DiskCount),
		CreatedAt: timestamp(backend.CreatedAt), UpdatedAt: timestamp(backend.UpdatedAt),
	}
}

type hostService struct {
	rookeryv1.UnimplementedHostServiceServer
	source VPPInterfaceSource
	store  *store.Store
	writer HostWriter
	taps   HostTapWriter
}

func (s *hostService) ListHostInterfaces(ctx context.Context, _ *rookeryv1.ListHostInterfacesRequest) (*rookeryv1.ListHostInterfacesResponse, error) {
	response, err := s.listInterfaces(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list host interfaces")
	}
	return response, nil
}
