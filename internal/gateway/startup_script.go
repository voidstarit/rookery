package gateway

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

// GetStartupScript returns the server-level script and its last execution
// result.
func (s *systemService) GetStartupScript(ctx context.Context, _ *rookeryv1.GetStartupScriptRequest) (*rookeryv1.StartupScript, error) {
	script, err := s.store.GetStartupScript(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not load startup script")
	}
	return startupScriptToProto(script), nil
}

// UpdateStartupScript stores the server-level script configuration. The script
// is not executed here; the API runs it once after startup reconciliation has
// completed.
func (s *systemService) UpdateStartupScript(ctx context.Context, request *rookeryv1.UpdateStartupScriptRequest) (*rookeryv1.StartupScript, error) {
	if request.GetEnabled() && strings.TrimSpace(request.GetScript()) == "" {
		return nil, status.Error(codes.InvalidArgument, "startup script is required when enabled")
	}
	script, err := s.store.SetStartupScript(ctx, request.GetEnabled(), request.GetScript())
	if err != nil {
		return nil, status.Error(codes.Internal, "could not update startup script")
	}
	return startupScriptToProto(script), nil
}

func timestampPtr(value *time.Time) *timestamppb.Timestamp {
	if value == nil {
		return nil
	}
	return timestamppb.New(*value)
}

func startupScriptToProto(script store.StartupScript) *rookeryv1.StartupScript {
	return &rookeryv1.StartupScript{
		Enabled:   script.Enabled,
		Script:    script.Script,
		Status:    script.Status,
		ExitCode:  int32(script.ExitCode),
		Output:    script.Output,
		RanAt:     timestampPtr(script.RanAt),
		UpdatedAt: timestamp(script.UpdatedAt),
	}
}

func startupScriptStatus(script store.StartupScript) *rookeryv1.StartupScriptStatus {
	return &rookeryv1.StartupScriptStatus{
		Enabled:  script.Enabled,
		Status:   script.Status,
		ExitCode: int32(script.ExitCode),
		RanAt:    timestampPtr(script.RanAt),
	}
}

// GetVPPStartupScript returns the VPP reconnect startup script and its last
// execution result.
func (s *systemService) GetVPPStartupScript(ctx context.Context, _ *rookeryv1.GetVPPStartupScriptRequest) (*rookeryv1.StartupScript, error) {
	script, err := s.store.GetVPPStartupScript(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not load VPP startup script")
	}
	return startupScriptToProto(script), nil
}

// UpdateVPPStartupScript stores the VPP reconnect script configuration.
func (s *systemService) UpdateVPPStartupScript(ctx context.Context, request *rookeryv1.UpdateVPPStartupScriptRequest) (*rookeryv1.StartupScript, error) {
	if request.GetEnabled() && strings.TrimSpace(request.GetScript()) == "" {
		return nil, status.Error(codes.InvalidArgument, "VPP startup script is required when enabled")
	}
	script, err := s.store.SetVPPStartupScript(ctx, request.GetEnabled(), request.GetScript())
	if err != nil {
		return nil, status.Error(codes.Internal, "could not update VPP startup script")
	}
	return startupScriptToProto(script), nil
}
