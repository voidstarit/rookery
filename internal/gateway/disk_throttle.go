package gateway

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/diskthrottle"
	"github.com/voidstarit/rookery/internal/store"
)

type diskThrottleService struct {
	rookeryv1.UnimplementedDiskThrottleServiceServer
	store *store.Store
}

func (s *diskThrottleService) ListDiskThrottleClasses(ctx context.Context, _ *rookeryv1.ListDiskThrottleClassesRequest) (*rookeryv1.ListDiskThrottleClassesResponse, error) {
	classes, err := s.store.ListDiskThrottleClasses(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list disk throttle classes")
	}
	response := &rookeryv1.ListDiskThrottleClassesResponse{
		Classes: make([]*rookeryv1.DiskThrottleClass, 0, len(classes)),
	}
	for _, class := range classes {
		response.Classes = append(response.Classes, diskThrottleClassToProto(class))
	}
	return response, nil
}

func (s *diskThrottleService) GetDiskThrottleClass(ctx context.Context, request *rookeryv1.GetDiskThrottleClassRequest) (*rookeryv1.DiskThrottleClass, error) {
	class, err := s.store.GetDiskThrottleClass(ctx, request.GetId())
	if err != nil {
		return nil, diskThrottleClassError(err)
	}
	return diskThrottleClassToProto(class), nil
}

func (s *diskThrottleService) CreateDiskThrottleClass(ctx context.Context, request *rookeryv1.CreateDiskThrottleClassRequest) (*rookeryv1.DiskThrottleClass, error) {
	class := store.DiskThrottleClass{
		Name:        request.GetName(),
		Description: request.GetDescription(),
		Limits:      limitsFromProto(request.GetLimits()),
	}
	if err := class.Limits.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	created, err := s.store.CreateDiskThrottleClass(ctx, class)
	if err != nil {
		return nil, diskThrottleClassError(err)
	}
	return diskThrottleClassToProto(created), nil
}

func (s *diskThrottleService) UpdateDiskThrottleClass(ctx context.Context, request *rookeryv1.UpdateDiskThrottleClassRequest) (*rookeryv1.DiskThrottleClass, error) {
	class, err := s.store.GetDiskThrottleClass(ctx, request.GetId())
	if err != nil {
		return nil, diskThrottleClassError(err)
	}
	oldLimits := class.Limits
	if request.Name != nil {
		class.Name = request.GetName()
	}
	if request.Description != nil {
		class.Description = request.GetDescription()
	}
	if request.Limits != nil {
		class.Limits = limitsFromProto(request.Limits)
	} else if class.ValidationError != "" {
		return nil, status.Error(codes.InvalidArgument, "supply valid limits to repair")
	}
	if err := class.Limits.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	updated, err := s.store.UpdateDiskThrottleClass(ctx, class)
	if err != nil {
		return nil, diskThrottleClassError(err)
	}
	log.Info().
		Int64("class_id", updated.ID).
		Interface("old_limits", oldLimits).
		Interface("new_limits", updated.Limits).
		Msg("disk throttle class updated")
	return diskThrottleClassToProto(updated), nil
}

func (s *diskThrottleService) DeleteDiskThrottleClass(ctx context.Context, request *rookeryv1.DeleteDiskThrottleClassRequest) (*emptypb.Empty, error) {
	class, err := s.store.GetDiskThrottleClass(ctx, request.GetId())
	if err != nil {
		return nil, diskThrottleClassError(err)
	}
	if err := s.store.DeleteDiskThrottleClass(ctx, request.GetId()); err != nil {
		if errors.Is(err, store.ErrDiskThrottleClassInUse) {
			return nil, s.classInUseError(ctx, class)
		}
		return nil, diskThrottleClassError(err)
	}
	return &emptypb.Empty{}, nil
}

func (s *diskThrottleService) classInUseError(ctx context.Context, class store.DiskThrottleClass) error {
	current, err := s.store.GetDiskThrottleClass(ctx, class.ID)
	if err == nil {
		class = current
	}
	names, err := s.store.ListDiskNamesByThrottleClass(ctx, class.ID, 5)
	if err != nil {
		return status.Error(codes.Internal, "could not list assigned disks")
	}
	message := "class is assigned to " + strconv.Itoa(class.DiskCount) + " disk(s)"
	if len(names) > 0 {
		message += ": " + strings.Join(names, ", ")
	}
	return status.Error(codes.Aborted, message)
}

func diskThrottleClassToProto(class store.DiskThrottleClass) *rookeryv1.DiskThrottleClass {
	return &rookeryv1.DiskThrottleClass{
		Id:              class.ID,
		Name:            class.Name,
		Description:     class.Description,
		Limits:          limitsToProto(class.Limits),
		DiskCount:       int32(class.DiskCount),
		CreatedAt:       timestamp(class.CreatedAt),
		UpdatedAt:       timestamp(class.UpdatedAt),
		ValidationError: class.ValidationError,
	}
}

func limitsToProto(limits diskthrottle.Limits) *rookeryv1.DiskThrottleLimits {
	return &rookeryv1.DiskThrottleLimits{
		BpsTotal:           limits.BPSTotal,
		BpsTotalMax:        limits.BPSTotalMax,
		BpsTotalMaxLength:  limits.BPSTotalMaxLength,
		BpsRead:            limits.BPSRead,
		BpsReadMax:         limits.BPSReadMax,
		BpsReadMaxLength:   limits.BPSReadMaxLength,
		BpsWrite:           limits.BPSWrite,
		BpsWriteMax:        limits.BPSWriteMax,
		BpsWriteMaxLength:  limits.BPSWriteMaxLength,
		IopsTotal:          limits.IOPSTotal,
		IopsTotalMax:       limits.IOPSTotalMax,
		IopsTotalMaxLength: limits.IOPSTotalMaxLength,
		IopsRead:           limits.IOPSRead,
		IopsReadMax:        limits.IOPSReadMax,
		IopsReadMaxLength:  limits.IOPSReadMaxLength,
		IopsWrite:          limits.IOPSWrite,
		IopsWriteMax:       limits.IOPSWriteMax,
		IopsWriteMaxLength: limits.IOPSWriteMaxLength,
		IopsSize:           limits.IOPSSize,
	}
}

func limitsFromProto(limits *rookeryv1.DiskThrottleLimits) diskthrottle.Limits {
	if limits == nil {
		return diskthrottle.Limits{}
	}
	return diskthrottle.Limits{
		BPSTotal:           limits.GetBpsTotal(),
		BPSTotalMax:        limits.GetBpsTotalMax(),
		BPSTotalMaxLength:  limits.GetBpsTotalMaxLength(),
		BPSRead:            limits.GetBpsRead(),
		BPSReadMax:         limits.GetBpsReadMax(),
		BPSReadMaxLength:   limits.GetBpsReadMaxLength(),
		BPSWrite:           limits.GetBpsWrite(),
		BPSWriteMax:        limits.GetBpsWriteMax(),
		BPSWriteMaxLength:  limits.GetBpsWriteMaxLength(),
		IOPSTotal:          limits.GetIopsTotal(),
		IOPSTotalMax:       limits.GetIopsTotalMax(),
		IOPSTotalMaxLength: limits.GetIopsTotalMaxLength(),
		IOPSRead:           limits.GetIopsRead(),
		IOPSReadMax:        limits.GetIopsReadMax(),
		IOPSReadMaxLength:  limits.GetIopsReadMaxLength(),
		IOPSWrite:          limits.GetIopsWrite(),
		IOPSWriteMax:       limits.GetIopsWriteMax(),
		IOPSWriteMaxLength: limits.GetIopsWriteMaxLength(),
		IOPSSize:           limits.GetIopsSize(),
	}
}

func diskThrottleClassError(err error) error {
	switch {
	case errors.Is(err, store.ErrDiskThrottleClassNotFound):
		return status.Error(codes.NotFound, "disk throttle class not found")
	case errors.Is(err, store.ErrDiskThrottleClassNameTaken):
		return status.Error(codes.AlreadyExists, "a disk throttle class with that name already exists")
	case errors.Is(err, store.ErrDiskThrottleClassInUse):
		return status.Error(codes.Aborted, "class is assigned to disks")
	case errors.Is(err, store.ErrDiskThrottleClassOnTemplate):
		return status.Error(codes.InvalidArgument, "templates cannot have a throttle class")
	case errors.Is(err, store.ErrDiskThrottleClassInvalid):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, store.ErrDiskThrottleClassInDeletion):
		return status.Error(codes.Aborted, "disk is being deleted")
	default:
		return status.Error(codes.Internal, "disk throttle class operation failed")
	}
}
