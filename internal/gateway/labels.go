package gateway

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/labels"
	"github.com/voidstarit/rookery/internal/store"
)

type labelService struct {
	rookeryv1.UnimplementedLabelServiceServer
	store *store.Store
}

// ListEntityLabels returns labels for every entity of a type.
func (s *labelService) ListEntityLabels(ctx context.Context, request *rookeryv1.ListEntityLabelsRequest) (*rookeryv1.ListEntityLabelsResponse, error) {
	entityType := strings.TrimSpace(request.GetEntityType())
	if !labels.ValidEntityType(entityType) {
		return nil, status.Errorf(codes.InvalidArgument, "unknown entity type %q", entityType)
	}
	records, err := s.store.ListEntityLabels(ctx, entityType)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list entity labels")
	}
	response := &rookeryv1.ListEntityLabelsResponse{Entities: make([]*rookeryv1.EntityLabels, 0, len(records))}
	for _, record := range records {
		response.Entities = append(response.Entities, entityLabelsToProto(record))
	}
	return response, nil
}

// GetEntityLabels returns labels for one entity.
func (s *labelService) GetEntityLabels(ctx context.Context, request *rookeryv1.GetEntityLabelsRequest) (*rookeryv1.EntityLabels, error) {
	entityType := strings.TrimSpace(request.GetEntityType())
	if !labels.ValidEntityType(entityType) {
		return nil, status.Errorf(codes.InvalidArgument, "unknown entity type %q", entityType)
	}
	record, err := s.store.GetEntityLabels(ctx, entityType, request.GetEntityId())
	if err != nil {
		return nil, status.Error(codes.Internal, "could not read entity labels")
	}
	return entityLabelsToProto(record), nil
}

// SetEntityLabels replaces the complete label set for one entity.
func (s *labelService) SetEntityLabels(ctx context.Context, request *rookeryv1.SetEntityLabelsRequest) (*rookeryv1.EntityLabels, error) {
	entityType := strings.TrimSpace(request.GetEntityType())
	if !labels.ValidEntityType(entityType) {
		return nil, status.Errorf(codes.InvalidArgument, "unknown entity type %q", entityType)
	}
	normalized, err := labels.Normalize(request.GetLabels())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	record, err := s.store.SetEntityLabels(ctx, entityType, request.GetEntityId(), normalized)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not save entity labels")
	}
	return entityLabelsToProto(record), nil
}

func entityLabelsToProto(record store.EntityLabels) *rookeryv1.EntityLabels {
	return &rookeryv1.EntityLabels{
		EntityType: record.EntityType,
		EntityId:   record.EntityID,
		Labels:     append([]string(nil), record.Labels...),
	}
}
