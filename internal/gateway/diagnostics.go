package gateway

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/host"
)

// DiagnosticsRuntime exposes VPP diagnostics and packet-capture operations.
type DiagnosticsRuntime interface {
	VPPNodeGraph(ctx context.Context) (host.VPPGraph, error)
	VPPGraphNodes(ctx context.Context) ([]host.VPPGraphNode, error)
	StartTrace(ctx context.Context, node string, count uint32) (host.TraceResult, error)
	GetTrace(ctx context.Context) (host.TraceResult, error)
	ClearTrace(ctx context.Context) error
	ACLInterfaceCounters(ctx context.Context) ([]host.ACLInterfaceCounters, error)
	VPPInterfaces(ctx context.Context) ([]host.VPPInterface, error)
	StartPacketCapture(ctx context.Context, options host.PacketCaptureOptions) (host.PacketCaptureStatus, error)
	StopPacketCapture(ctx context.Context) (host.PacketCaptureStatus, error)
	PacketCaptureStatus(ctx context.Context) (host.PacketCaptureStatus, error)
	ListPacketCaptures() ([]host.PacketCaptureFile, error)
	DeletePacketCapture(name string) error
}

func (s *troubleshootService) ready() error {
	if s.runtime == nil {
		return status.Error(codes.Unavailable, "VPP network runtime is not configured")
	}
	if s.readiness != nil && !s.readiness.Status().VPP.Ready {
		return status.Error(codes.Unavailable, "VPP is not ready")
	}
	return nil
}

func (s *troubleshootService) GetVPPGraph(ctx context.Context, _ *rookeryv1.GetVPPGraphRequest) (*rookeryv1.VPPGraph, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	graph, err := s.runtime.VPPNodeGraph(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not read VPP node graph: "+err.Error())
	}
	result := &rookeryv1.VPPGraph{}
	for _, node := range graph.Nodes {
		result.Nodes = append(result.Nodes, &rookeryv1.VPPGraphNode{
			Id: node.ID, Label: node.Label, Width: node.Width, FontSize: node.FontSize, Fill: int32(node.Fill),
		})
	}
	for _, edge := range graph.Edges {
		result.Edges = append(result.Edges, &rookeryv1.VPPGraphEdge{Source: edge.Source, Target: edge.Target})
	}
	if !graph.GeneratedAt.IsZero() {
		result.GeneratedAt = timestamppb.New(graph.GeneratedAt)
	}
	return result, nil
}

func (s *troubleshootService) ListACLInterfaceCounters(ctx context.Context, _ *rookeryv1.ListACLInterfaceCountersRequest) (*rookeryv1.ListACLInterfaceCountersResponse, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	counters, err := s.runtime.ACLInterfaceCounters(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not read ACL interface counters: "+err.Error())
	}
	response := &rookeryv1.ListACLInterfaceCountersResponse{
		Interfaces: make([]*rookeryv1.ACLInterfaceCounters, 0, len(counters)),
	}
	for _, item := range counters {
		response.Interfaces = append(response.Interfaces, &rookeryv1.ACLInterfaceCounters{
			Interface: item.Interface, SwIfIndex: item.SwIfIndex,
			Ingress: aclDirectionToProto(item.Ingress), Egress: aclDirectionToProto(item.Egress),
		})
	}
	return response, nil
}

func (s *troubleshootService) ListTraceNodes(ctx context.Context, _ *rookeryv1.ListTraceNodesRequest) (*rookeryv1.ListTraceNodesResponse, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	nodes, err := s.runtime.VPPGraphNodes(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list VPP graph nodes: "+err.Error())
	}
	response := &rookeryv1.ListTraceNodesResponse{Nodes: make([]string, 0, len(nodes))}
	for _, node := range nodes {
		response.Nodes = append(response.Nodes, node.ID)
	}
	return response, nil
}

func (s *troubleshootService) StartTrace(ctx context.Context, request *rookeryv1.StartTraceRequest) (*rookeryv1.TraceResult, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	result, err := s.runtime.StartTrace(ctx, request.GetNode(), request.GetCount())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "could not start VPP trace: "+err.Error())
	}
	return traceResultToProto(result), nil
}

func (s *troubleshootService) GetTrace(ctx context.Context, _ *rookeryv1.GetTraceRequest) (*rookeryv1.TraceResult, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	result, err := s.runtime.GetTrace(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not read VPP trace: "+err.Error())
	}
	return traceResultToProto(result), nil
}

func (s *troubleshootService) ClearTrace(ctx context.Context, _ *rookeryv1.ClearTraceRequest) (*emptypb.Empty, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.runtime.ClearTrace(ctx); err != nil {
		return nil, status.Error(codes.Internal, "could not clear VPP trace: "+err.Error())
	}
	return &emptypb.Empty{}, nil
}

func traceResultToProto(value host.TraceResult) *rookeryv1.TraceResult {
	result := &rookeryv1.TraceResult{Node: value.Node, Count: value.Count, Raw: value.Raw}
	if !value.CapturedAt.IsZero() {
		result.CapturedAt = timestamppb.New(value.CapturedAt)
	}
	return result
}

func aclDirectionToProto(value host.ACLDirectionCounters) *rookeryv1.ACLDirectionCounters {
	result := &rookeryv1.ACLDirectionCounters{
		Matched: value.Matched, Denied: value.Denied, Permitted: value.Permitted,
		AclIndexes: append([]uint32(nil), value.ACLIndexes...),
	}
	for _, rule := range value.Rules {
		result.Rules = append(result.Rules, &rookeryv1.ACLRuleCounter{
			AclIndex: rule.ACLIndex, RuleIndex: rule.RuleIndex, Action: rule.Action, Matched: rule.Matched,
		})
	}
	return result
}

func (s *captureService) ready() error {
	if s.runtime == nil {
		return status.Error(codes.Unavailable, "VPP network runtime is not configured")
	}
	if s.readiness != nil && !s.readiness.Status().VPP.Ready {
		return status.Error(codes.Unavailable, "VPP is not ready")
	}
	return nil
}

func (s *captureService) ListCaptureInterfaces(ctx context.Context, _ *rookeryv1.ListCaptureInterfacesRequest) (*rookeryv1.ListCaptureInterfacesResponse, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	interfaces, err := s.runtime.VPPInterfaces(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list VPP interfaces: "+err.Error())
	}
	response := &rookeryv1.ListCaptureInterfacesResponse{Interfaces: make([]*rookeryv1.VPPInterface, 0, len(interfaces))}
	for _, item := range interfaces {
		response.Interfaces = append(response.Interfaces, &rookeryv1.VPPInterface{
			Name: item.Name, SwIfIndex: item.SwIfIndex, Type: item.Type, Mac: item.MAC,
			Mtu: uint32(item.MTU), Up: item.Up, Tag: item.Tag,
		})
	}
	return response, nil
}

func (s *captureService) GetCaptureOverview(ctx context.Context, _ *rookeryv1.GetCaptureOverviewRequest) (*rookeryv1.PacketCaptureOverview, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	statusValue, err := s.runtime.PacketCaptureStatus(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not read packet capture status: "+err.Error())
	}
	files, err := s.runtime.ListPacketCaptures()
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list packet captures: "+err.Error())
	}
	return &rookeryv1.PacketCaptureOverview{
		Status: captureStatusToProto(statusValue),
		Files:  captureFilesToProto(files),
	}, nil
}

func (s *captureService) StartCapture(ctx context.Context, request *rookeryv1.StartCaptureRequest) (*rookeryv1.PacketCaptureStatus, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	statusValue, err := s.runtime.StartPacketCapture(ctx, host.PacketCaptureOptions{
		Interface: request.GetInterface(), Direction: request.GetDirection(),
		MaxBytes: request.GetMaxBytes(), SnapLength: int(request.GetSnapLength()),
	})
	if err != nil {
		if errors.Is(err, host.ErrCaptureActive) {
			return nil, status.Error(codes.Aborted, err.Error())
		}
		return nil, status.Error(codes.Internal, "could not start packet capture: "+err.Error())
	}
	return captureStatusToProto(statusValue), nil
}

func (s *captureService) StopCapture(ctx context.Context, _ *rookeryv1.StopCaptureRequest) (*rookeryv1.PacketCaptureStatus, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	statusValue, err := s.runtime.StopPacketCapture(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not stop packet capture: "+err.Error())
	}
	return captureStatusToProto(statusValue), nil
}

func (s *captureService) ListCaptureFiles(_ context.Context, _ *rookeryv1.ListCaptureFilesRequest) (*rookeryv1.ListCaptureFilesResponse, error) {
	if s.runtime == nil {
		return nil, status.Error(codes.Unavailable, "VPP network runtime is not configured")
	}
	files, err := s.runtime.ListPacketCaptures()
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list packet captures: "+err.Error())
	}
	return &rookeryv1.ListCaptureFilesResponse{Files: captureFilesToProto(files)}, nil
}

func (s *captureService) DeleteCaptureFile(_ context.Context, request *rookeryv1.DeleteCaptureFileRequest) (*emptypb.Empty, error) {
	if s.runtime == nil {
		return nil, status.Error(codes.Unavailable, "VPP network runtime is not configured")
	}
	if err := s.runtime.DeletePacketCapture(request.GetName()); err != nil {
		switch {
		case errors.Is(err, host.ErrCaptureNotFound):
			return nil, status.Error(codes.NotFound, "packet capture not found")
		case errors.Is(err, host.ErrCaptureFileActive):
			return nil, status.Error(codes.Aborted, err.Error())
		default:
			return nil, status.Error(codes.Internal, "could not delete packet capture")
		}
	}
	return &emptypb.Empty{}, nil
}

func captureStatusToProto(value host.PacketCaptureStatus) *rookeryv1.PacketCaptureStatus {
	result := &rookeryv1.PacketCaptureStatus{
		Active: value.Active, Interface: value.Interface, Direction: value.Direction, File: value.File,
		Size: value.Size, MaxPackets: value.MaxPackets, MaxBytes: value.MaxBytes,
		SnapLength: int32(value.SnapLength), Packets: value.Packets, Buffered: value.Buffered, Raw: value.Raw,
	}
	if value.StartedAt != nil {
		result.StartedAt = timestamppb.New(*value.StartedAt)
	}
	if value.StoppedAt != nil {
		result.StoppedAt = timestamppb.New(*value.StoppedAt)
	}
	return result
}

func captureFilesToProto(files []host.PacketCaptureFile) []*rookeryv1.PacketCaptureFile {
	result := make([]*rookeryv1.PacketCaptureFile, 0, len(files))
	for _, file := range files {
		result = append(result, &rookeryv1.PacketCaptureFile{
			Name: file.Name, Size: file.Size, Modified: timestamppb.New(file.Modified),
		})
	}
	return result
}

type troubleshootService struct {
	rookeryv1.UnimplementedTroubleshootServiceServer
	runtime   DiagnosticsRuntime
	readiness Readiness
}

type captureService struct {
	rookeryv1.UnimplementedCaptureServiceServer
	runtime   DiagnosticsRuntime
	readiness Readiness
}
