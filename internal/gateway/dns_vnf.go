package gateway

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

// dnsVNFToProto converts a typed DNS parent row to its API representation.
func dnsVNFToProto(vnf store.DNSVNF) *rookeryv1.DNSVNF {
	return &rookeryv1.DNSVNF{
		Id: vnf.ID, Name: vnf.Name, NetworkId: vnf.NetworkID, NetworkName: vnf.NetworkName,
		Enabled: vnf.Enabled, Forward: vnf.Config.Forward,
		RecordCount: uint32(vnf.RecordCount), ForwarderCount: uint32(vnf.ForwarderCount),
		TapSwIfIndex: vnf.TapSwIfIndex, TapHostName: vnf.TapHostName,
		CreatedAt: timestamp(vnf.CreatedAt), UpdatedAt: timestamp(vnf.UpdatedAt),
	}
}

// dnsVNFView builds a typed DNS parent with live status.
func (s *vnfService) dnsVNFView(ctx context.Context, vnf store.DNSVNF) *rookeryv1.DNSVNF {
	result := dnsVNFToProto(vnf)
	statusValue, detail, _ := s.applyVNFStatus(ctx, identityStatusRecord(vnf.ID, vnf.Name, vnf.NetworkID, store.VNFTypeDNS, vnf.Enabled))
	result.Status = statusValue
	result.StatusDetail = detail
	return result
}

// ListDNSVNFs returns DNS VNFs, optionally filtered by network.
func (s *vnfService) ListDNSVNFs(ctx context.Context, request *rookeryv1.ListDNSVNFsRequest) (*rookeryv1.ListDNSVNFsResponse, error) {
	vnfs, err := s.store.ListDNSVNFs(ctx, request.NetworkId)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list DNS VNFs")
	}
	response := &rookeryv1.ListDNSVNFsResponse{Vnfs: make([]*rookeryv1.DNSVNF, 0, len(vnfs))}
	for _, vnf := range vnfs {
		response.Vnfs = append(response.Vnfs, s.dnsVNFView(ctx, vnf))
	}
	return response, nil
}

// GetDNSVNF returns one DNS VNF.
func (s *vnfService) GetDNSVNF(ctx context.Context, request *rookeryv1.GetDNSVNFRequest) (*rookeryv1.DNSVNF, error) {
	vnf, err := s.store.GetDNSVNF(ctx, request.GetId())
	if err != nil {
		return nil, vnfReadError(err)
	}
	return s.dnsVNFView(ctx, vnf), nil
}

// CreateDNSVNF delegates to the lifecycle writer.
func (s *vnfService) CreateDNSVNF(ctx context.Context, request *rookeryv1.CreateDNSVNFRequest) (*rookeryv1.DNSVNF, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	created, err := s.writer.CreateDNSVNF(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	vnf, err := s.store.GetDNSVNF(ctx, created.ID)
	if err != nil {
		return nil, vnfReadError(err)
	}
	return s.dnsVNFView(ctx, vnf), nil
}

// UpdateDNSVNF delegates to the lifecycle writer.
func (s *vnfService) UpdateDNSVNF(ctx context.Context, request *rookeryv1.UpdateDNSVNFRequest) (*rookeryv1.DNSVNF, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	updated, err := s.writer.UpdateDNSVNF(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	vnf, err := s.store.GetDNSVNF(ctx, updated.ID)
	if err != nil {
		return nil, vnfReadError(err)
	}
	return s.dnsVNFView(ctx, vnf), nil
}

// DeleteDNSVNF delegates removal to the lifecycle writer.
func (s *vnfService) DeleteDNSVNF(ctx context.Context, request *rookeryv1.DeleteDNSVNFRequest) (*emptypb.Empty, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	if err := s.writer.DeleteDNSVNF(ctx, request); err != nil {
		return nil, mapVNFWriteError(err)
	}
	return &emptypb.Empty{}, nil
}

// RestartDNSVNF restarts a DNS VNF and returns the refreshed parent.
func (s *vnfService) RestartDNSVNF(ctx context.Context, request *rookeryv1.RestartDNSVNFRequest) (*rookeryv1.DNSVNF, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	restarted, err := s.writer.RestartDNSVNF(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	vnf, err := s.store.GetDNSVNF(ctx, restarted.ID)
	if err != nil {
		return nil, vnfReadError(err)
	}
	return s.dnsVNFView(ctx, vnf), nil
}

// ListDNSVNFRecordViews merges the configured static records with the records
// derived from DHCP leases. Static records stay visible when the daemon is
// unreachable; the dynamic failure is reported separately.
func (s *vnfService) ListDNSVNFRecordViews(ctx context.Context, request *rookeryv1.ListDNSVNFRecordViewsRequest) (*rookeryv1.ListDNSVNFRecordViewsResponse, error) {
	vnf, err := s.store.GetDNSVNF(ctx, request.GetId())
	if err != nil {
		return nil, vnfReadError(err)
	}
	records, err := s.store.ListDNSVNFRecords(ctx, vnf.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not read DNS records")
	}
	response := &rookeryv1.ListDNSVNFRecordViewsResponse{
		VnfId: vnf.ID, Records: make([]*rookeryv1.DNSRecordView, 0, len(records)),
	}
	for _, record := range records {
		response.Records = append(response.Records, &rookeryv1.DNSRecordView{
			Name: record.Name, Type: record.Type, Value: record.Value, Ttl: record.TTL, Source: "static",
		})
	}
	if s.statusProvider != nil {
		dynamic, dynamicErr := s.statusProvider.VNFDNSRecords(ctx, identityStatusRecord(vnf.ID, vnf.Name, vnf.NetworkID, store.VNFTypeDNS, vnf.Enabled))
		if dynamicErr != nil {
			s.logger.Error().Err(dynamicErr).Int64("vnf_id", vnf.ID).Msg("could not read dynamic DNS records")
			response.DynamicError = "dynamic records are unavailable"
		} else {
			for _, record := range dynamic {
				response.Records = append(response.Records, &rookeryv1.DNSRecordView{
					Name: record.Name, Type: record.Type, Value: record.Value, Ttl: record.TTL,
					Source: "dhcp", SourceVnfId: record.SourceVNFID,
				})
			}
		}
	}
	sortedRecordViews(response.Records)
	return response, nil
}

// dnsRecordToProto converts one configured record.
func dnsRecordToProto(item store.DNSRecord) *rookeryv1.DNSRecord {
	return &rookeryv1.DNSRecord{
		Id: item.ID, VnfId: item.VNFID, Name: item.Name, Type: item.Type,
		Value: item.Value, Ttl: item.TTL,
	}
}

// dnsForwarderToProto converts one forwarder.
func dnsForwarderToProto(item store.DNSForwarder) *rookeryv1.DNSForwarder {
	return &rookeryv1.DNSForwarder{
		Id: item.ID, VnfId: item.VNFID, Address: item.Address, Position: item.Position,
	}
}

// ListDNSVNFRecords returns the static records of a DNS VNF.
func (s *vnfService) ListDNSVNFRecords(ctx context.Context, request *rookeryv1.ListDNSVNFRecordsRequest) (*rookeryv1.ListDNSVNFRecordsResponse, error) {
	items, err := s.store.ListDNSVNFRecords(ctx, request.GetVnfId())
	if err != nil {
		return nil, vnfReadError(err)
	}
	response := &rookeryv1.ListDNSVNFRecordsResponse{Records: make([]*rookeryv1.DNSRecord, 0, len(items))}
	for _, item := range items {
		response.Records = append(response.Records, dnsRecordToProto(item))
	}
	return response, nil
}

// GetDNSVNFRecord returns one static record.
func (s *vnfService) GetDNSVNFRecord(ctx context.Context, request *rookeryv1.GetDNSVNFRecordRequest) (*rookeryv1.DNSRecord, error) {
	item, err := s.store.GetDNSVNFRecord(ctx, request.GetVnfId(), request.GetRecordId())
	if err != nil {
		return nil, vnfReadError(err)
	}
	return dnsRecordToProto(item), nil
}

// CreateDNSVNFRecord delegates to the lifecycle writer.
func (s *vnfService) CreateDNSVNFRecord(ctx context.Context, request *rookeryv1.CreateDNSVNFRecordRequest) (*rookeryv1.DNSRecord, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	item, err := s.writer.CreateDNSVNFRecord(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	return dnsRecordToProto(item), nil
}

// UpdateDNSVNFRecord delegates to the lifecycle writer.
func (s *vnfService) UpdateDNSVNFRecord(ctx context.Context, request *rookeryv1.UpdateDNSVNFRecordRequest) (*rookeryv1.DNSRecord, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	item, err := s.writer.UpdateDNSVNFRecord(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	return dnsRecordToProto(item), nil
}

// DeleteDNSVNFRecord delegates removal to the lifecycle writer.
func (s *vnfService) DeleteDNSVNFRecord(ctx context.Context, request *rookeryv1.DeleteDNSVNFRecordRequest) (*emptypb.Empty, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	if err := s.writer.DeleteDNSVNFRecord(ctx, request); err != nil {
		return nil, mapVNFWriteError(err)
	}
	return &emptypb.Empty{}, nil
}

// ListDNSVNFForwarders returns the ordered forwarders of a DNS VNF.
func (s *vnfService) ListDNSVNFForwarders(ctx context.Context, request *rookeryv1.ListDNSVNFForwardersRequest) (*rookeryv1.ListDNSVNFForwardersResponse, error) {
	items, err := s.store.ListDNSVNFForwarders(ctx, request.GetVnfId())
	if err != nil {
		return nil, vnfReadError(err)
	}
	response := &rookeryv1.ListDNSVNFForwardersResponse{
		Forwarders: make([]*rookeryv1.DNSForwarder, 0, len(items)),
	}
	for _, item := range items {
		response.Forwarders = append(response.Forwarders, dnsForwarderToProto(item))
	}
	return response, nil
}

// GetDNSVNFForwarder returns one forwarder.
func (s *vnfService) GetDNSVNFForwarder(ctx context.Context, request *rookeryv1.GetDNSVNFForwarderRequest) (*rookeryv1.DNSForwarder, error) {
	item, err := s.store.GetDNSVNFForwarder(ctx, request.GetVnfId(), request.GetForwarderId())
	if err != nil {
		return nil, vnfReadError(err)
	}
	return dnsForwarderToProto(item), nil
}

// CreateDNSVNFForwarder delegates to the lifecycle writer.
func (s *vnfService) CreateDNSVNFForwarder(ctx context.Context, request *rookeryv1.CreateDNSVNFForwarderRequest) (*rookeryv1.DNSForwarder, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	item, err := s.writer.CreateDNSVNFForwarder(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	return dnsForwarderToProto(item), nil
}

// UpdateDNSVNFForwarder delegates to the lifecycle writer.
func (s *vnfService) UpdateDNSVNFForwarder(ctx context.Context, request *rookeryv1.UpdateDNSVNFForwarderRequest) (*rookeryv1.DNSForwarder, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	item, err := s.writer.UpdateDNSVNFForwarder(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	return dnsForwarderToProto(item), nil
}

// DeleteDNSVNFForwarder delegates removal to the lifecycle writer.
func (s *vnfService) DeleteDNSVNFForwarder(ctx context.Context, request *rookeryv1.DeleteDNSVNFForwarderRequest) (*emptypb.Empty, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	if err := s.writer.DeleteDNSVNFForwarder(ctx, request); err != nil {
		return nil, mapVNFWriteError(err)
	}
	return &emptypb.Empty{}, nil
}
