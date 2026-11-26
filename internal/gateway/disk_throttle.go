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

// ListDiskThrottleClasses returns every stored throttle class converted to proto, or an
// Internal status when they cannot be listed.
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

// GetDiskThrottleClass returns one throttle class by ID, mapping a missing class to
// NotFound.
func (s *diskThrottleService) GetDiskThrottleClass(ctx context.Context, request *rookeryv1.GetDiskThrottleClassRequest) (*rookeryv1.DiskThrottleClass, error) {
	class, err := s.store.GetDiskThrottleClass(ctx, request.GetId())
	if err != nil {
		return nil, diskThrottleClassError(err)
	}
	return diskThrottleClassToProto(class), nil
}

// CreateDiskThrottleClass validates the requested limits and stores a new throttle class,
// returning InvalidArgument for invalid limits and AlreadyExists for a duplicate name.
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

// UpdateDiskThrottleClass applies any supplied name, description, or limits to an existing
// class, refusing to leave a class with a stored validation error unrepaired and logging
// the old and new limits.
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

// DeleteDiskThrottleClass removes a throttle class, returning an Aborted error that names
// assigned disks when the class is still in use.
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

// classInUseError builds the Aborted error for a class deletion blocked by disk
// assignments, re-reading the class and listing up to five assigned disk names.
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

// diskThrottleClassToProto converts a stored throttle class into its proto form,
// including limits, disk count, timestamps, and any validation error.
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

// limitsToProto copies throttle limits field-by-field into the proto message, including
// the burst maxima and the IOPS size used for iops accounting.
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

// limitsFromProto copies proto throttle limits into the diskthrottle type, returning zero
// limits when the message is nil.
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

// diskThrottleClassError maps store throttle class errors to gRPC statuses: NotFound,
// AlreadyExists, Aborted for in-use or deleting disks, InvalidArgument for template or
// invalid limits, and Internal otherwise.
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
