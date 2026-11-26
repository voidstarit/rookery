package gateway

import (
	"context"
	"errors"
	"sort"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/store"
)

// VNFWriter is the typed VNF lifecycle service implemented by internal/lifecycle.
type VNFWriter interface {
	CreateDHCPVNF(ctx context.Context, request *rookeryv1.CreateDHCPVNFRequest) (store.VNF, error)
	UpdateDHCPVNF(ctx context.Context, request *rookeryv1.UpdateDHCPVNFRequest) (store.VNF, error)
	DeleteDHCPVNF(ctx context.Context, request *rookeryv1.DeleteDHCPVNFRequest) error
	RestartDHCPVNF(ctx context.Context, request *rookeryv1.RestartDHCPVNFRequest) (store.VNF, error)
	CreateDHCPStaticAssignment(ctx context.Context, request *rookeryv1.CreateDHCPStaticAssignmentRequest) (store.DHCPStaticAssignment, error)
	UpdateDHCPStaticAssignment(ctx context.Context, request *rookeryv1.UpdateDHCPStaticAssignmentRequest) (store.DHCPStaticAssignment, error)
	DeleteDHCPStaticAssignment(ctx context.Context, request *rookeryv1.DeleteDHCPStaticAssignmentRequest) error
	CreateDHCPOption(ctx context.Context, request *rookeryv1.CreateDHCPOptionRequest) (store.DHCPOption, error)
	UpdateDHCPOption(ctx context.Context, request *rookeryv1.UpdateDHCPOptionRequest) (store.DHCPOption, error)
	DeleteDHCPOption(ctx context.Context, request *rookeryv1.DeleteDHCPOptionRequest) error

	CreateDNSVNF(ctx context.Context, request *rookeryv1.CreateDNSVNFRequest) (store.VNF, error)
	UpdateDNSVNF(ctx context.Context, request *rookeryv1.UpdateDNSVNFRequest) (store.VNF, error)
	DeleteDNSVNF(ctx context.Context, request *rookeryv1.DeleteDNSVNFRequest) error
	RestartDNSVNF(ctx context.Context, request *rookeryv1.RestartDNSVNFRequest) (store.VNF, error)
	CreateDNSVNFRecord(ctx context.Context, request *rookeryv1.CreateDNSVNFRecordRequest) (store.DNSRecord, error)
	UpdateDNSVNFRecord(ctx context.Context, request *rookeryv1.UpdateDNSVNFRecordRequest) (store.DNSRecord, error)
	DeleteDNSVNFRecord(ctx context.Context, request *rookeryv1.DeleteDNSVNFRecordRequest) error
	CreateDNSVNFForwarder(ctx context.Context, request *rookeryv1.CreateDNSVNFForwarderRequest) (store.DNSForwarder, error)
	UpdateDNSVNFForwarder(ctx context.Context, request *rookeryv1.UpdateDNSVNFForwarderRequest) (store.DNSForwarder, error)
	DeleteDNSVNFForwarder(ctx context.Context, request *rookeryv1.DeleteDNSVNFForwarderRequest) error

	CreateNAT44VNF(ctx context.Context, request *rookeryv1.CreateNAT44VNFRequest) (store.VNF, error)
	UpdateNAT44VNF(ctx context.Context, request *rookeryv1.UpdateNAT44VNFRequest) (store.VNF, error)
	DeleteNAT44VNF(ctx context.Context, request *rookeryv1.DeleteNAT44VNFRequest) error
	RestartNAT44VNF(ctx context.Context, request *rookeryv1.RestartNAT44VNFRequest) (store.VNF, error)
	CreateNAT44DNATRule(ctx context.Context, request *rookeryv1.CreateNAT44DNATRuleRequest) (store.DNATRule, error)
	UpdateNAT44DNATRule(ctx context.Context, request *rookeryv1.UpdateNAT44DNATRuleRequest) (store.DNATRule, error)
	DeleteNAT44DNATRule(ctx context.Context, request *rookeryv1.DeleteNAT44DNATRuleRequest) error

	CreateBGPVNF(ctx context.Context, request *rookeryv1.CreateBGPVNFRequest) (store.VNF, error)
	UpdateBGPVNF(ctx context.Context, request *rookeryv1.UpdateBGPVNFRequest) (store.VNF, error)
	DeleteBGPVNF(ctx context.Context, request *rookeryv1.DeleteBGPVNFRequest) error
	RestartBGPVNF(ctx context.Context, request *rookeryv1.RestartBGPVNFRequest) (store.VNF, error)
	CreateBGPVNFAdvertisedPrefix(ctx context.Context, request *rookeryv1.CreateBGPVNFAdvertisedPrefixRequest) (store.BGPPrefix, error)
	UpdateBGPVNFAdvertisedPrefix(ctx context.Context, request *rookeryv1.UpdateBGPVNFAdvertisedPrefixRequest) (store.BGPPrefix, error)
	DeleteBGPVNFAdvertisedPrefix(ctx context.Context, request *rookeryv1.DeleteBGPVNFAdvertisedPrefixRequest) error
}

// VNFRuntime supplies DHCP runtime state.
type VNFRuntime interface {
	Leases(ctx context.Context, vnfID int64) ([]store.VNFLease, error)
}

// VNFStatusProvider supplies the API's status, NAT session, BGP runtime, and
// dynamic DNS views.
type VNFStatusProvider interface {
	VNFStatus(ctx context.Context, vnf store.VNF) (status, detail string, leaseCount int)
	VNFSessions(ctx context.Context, vnf store.VNF) ([]string, error)
	VNFBGP(ctx context.Context, vnf store.VNF) (host.BGPStatus, error)
	VNFDNSRecords(ctx context.Context, vnf store.VNF) ([]host.DNSRecordView, error)
}

// vnfService serves the four typed VNF services from one shared store, status
// provider, and lifecycle writer.
type vnfService struct {
	rookeryv1.UnimplementedDHCPVNFServiceServer
	rookeryv1.UnimplementedDNSVNFServiceServer
	rookeryv1.UnimplementedNAT44VNFServiceServer
	rookeryv1.UnimplementedBGPVNFServiceServer
	store          *store.Store
	runtime        VNFRuntime
	statusProvider VNFStatusProvider
	writer         VNFWriter
}

// writeAvailable returns Unavailable when no native VNF writer is configured.
func (s *vnfService) writeAvailable() error {
	if s.writer == nil {
		return status.Error(codes.Unavailable, "VNF write runtime is not configured")
	}
	return nil
}

// vnfReadError maps a typed store read failure to NotFound for a missing or
// wrong-type VNF and to Internal otherwise.
func vnfReadError(err error) error {
	switch {
	case errors.Is(err, store.ErrVNFNotFound),
		errors.Is(err, store.ErrVNFTypeMismatch),
		errors.Is(err, store.ErrVNFChildNotFound):
		return status.Error(codes.NotFound, "vnf not found")
	default:
		return status.Error(codes.Internal, "could not read VNF")
	}
}

// dhcpStatusRecord builds the minimal hydrated record the status provider needs
// for a typed DHCP parent: identity, enabled, and the range addresses.
func dhcpStatusRecord(vnf store.DHCPVNF) store.VNF {
	return store.VNF{
		ID: vnf.ID, Name: vnf.Name, NetworkID: vnf.NetworkID, Type: vnf.Family,
		Enabled: vnf.Enabled,
		Config: store.VNFConfig{
			Gateway:    vnf.Config.Gateway,
			RangeStart: vnf.Config.RangeStart,
			RangeEnd:   vnf.Config.RangeEnd,
		},
	}
}

// identityStatusRecord builds the minimal hydrated record for a non-DHCP parent.
func identityStatusRecord(id int64, name string, networkID int64, vnfType string, enabled bool) store.VNF {
	return store.VNF{ID: id, Name: name, NetworkID: networkID, Type: vnfType, Enabled: enabled}
}

// applyVNFStatus fills status, detail, and the DHCP lease count from the status
// provider when one is configured.
func (s *vnfService) applyVNFStatus(ctx context.Context, record store.VNF) (statusValue, detail string, leaseCount int) {
	if s.statusProvider == nil {
		return "", "", 0
	}
	return s.statusProvider.VNFStatus(ctx, record)
}

// sortedRecordViews sorts merged DNS record views by name, type, and value.
func sortedRecordViews(records []*rookeryv1.DNSRecordView) {
	sort.Slice(records, func(i, j int) bool {
		left, right := records[i], records[j]
		if left.GetName() != right.GetName() {
			return left.GetName() < right.GetName()
		}
		if left.GetType() != right.GetType() {
			return left.GetType() < right.GetType()
		}
		return left.GetValue() < right.GetValue()
	})
}

type vnfStatusError interface {
	VNFStatus() (int, string)
}

// mapVNFWriteError converts an error carrying an HTTP status via VNFStatus into a
// gRPC status, falling back to a generic Internal error for untyped errors.
func mapVNFWriteError(err error) error {
	var operationError vnfStatusError
	if !errors.As(err, &operationError) {
		return status.Error(codes.Internal, "VNF operation failed")
	}
	httpStatus, message := operationError.VNFStatus()
	if message == "" {
		message = "VNF operation failed"
	}
	switch httpStatus {
	case 400:
		return status.Error(codes.InvalidArgument, message)
	case 404:
		return status.Error(codes.NotFound, message)
	case 409:
		return status.Error(codes.Aborted, message)
	case 503:
		return status.Error(codes.Unavailable, message)
	default:
		return status.Error(codes.Internal, "VNF operation failed")
	}
}
