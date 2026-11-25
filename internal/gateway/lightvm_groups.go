package gateway

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

type fleetWaker interface {
	WakeFleet()
}

type lightVMGroupService struct {
	rookeryv1.UnimplementedLightVMGroupServiceServer
	store *store.Store
	waker fleetWaker
}

func (s *lightVMGroupService) ListLightVMGroups(ctx context.Context, _ *rookeryv1.ListLightVMGroupsRequest) (*rookeryv1.ListLightVMGroupsResponse, error) {
	groups, err := s.store.ListLightVMGroups(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list LightVM groups")
	}
	response := &rookeryv1.ListLightVMGroupsResponse{Groups: make([]*rookeryv1.LightVMGroup, 0, len(groups))}
	for _, group := range groups {
		response.Groups = append(response.Groups, lightVMGroupToProto(group))
	}
	return response, nil
}

func (s *lightVMGroupService) GetLightVMGroup(ctx context.Context, request *rookeryv1.GetLightVMGroupRequest) (*rookeryv1.GetLightVMGroupResponse, error) {
	group, err := s.store.GetLightVMGroup(ctx, request.GetId())
	if err != nil {
		return nil, lightVMGroupError(err)
	}
	return &rookeryv1.GetLightVMGroupResponse{Group: lightVMGroupToProto(group)}, nil
}

func (s *lightVMGroupService) CreateLightVMGroup(ctx context.Context, request *rookeryv1.CreateLightVMGroupRequest) (*rookeryv1.LightVMGroup, error) {
	paused := false
	if request.Paused != nil {
		paused = request.GetPaused()
	}
	group, err := s.store.CreateLightVMGroup(ctx, store.LightVMGroup{
		Name: request.GetName(), Description: request.GetDescription(),
		TemplateVersionID: request.GetTemplateVersionId(),
		DesiredCount:      request.GetDesiredCount(), Paused: paused,
	})
	if err != nil {
		return nil, lightVMGroupError(err)
	}
	s.wake()
	return lightVMGroupToProto(group), nil
}

func (s *lightVMGroupService) UpdateLightVMGroup(ctx context.Context, request *rookeryv1.UpdateLightVMGroupRequest) (*rookeryv1.LightVMGroup, error) {
	update := store.LightVMGroupUpdate{}
	if request.Name != nil {
		name := request.GetName()
		update.Name = &name
	}
	if request.Description != nil {
		description := request.GetDescription()
		update.Description = &description
	}
	if request.TemplateVersionId != nil {
		versionID := request.GetTemplateVersionId()
		update.TemplateVersionID = &versionID
	}
	if request.DesiredCount != nil {
		count := request.GetDesiredCount()
		update.DesiredCount = &count
	}
	if request.Paused != nil {
		paused := request.GetPaused()
		update.Paused = &paused
	}
	group, err := s.store.UpdateLightVMGroup(ctx, request.GetId(), update)
	if err != nil {
		return nil, lightVMGroupError(err)
	}
	s.wake()
	return lightVMGroupToProto(group), nil
}

func (s *lightVMGroupService) DeleteLightVMGroup(ctx context.Context, request *rookeryv1.DeleteLightVMGroupRequest) (*rookeryv1.DeleteLightVMGroupResponse, error) {
	deleted, group, err := s.store.SetLightVMGroupDeleting(ctx, request.GetId(), request.GetForce())
	if err != nil {
		return nil, lightVMGroupError(err)
	}
	if deleted {
		return &rookeryv1.DeleteLightVMGroupResponse{State: "deleted"}, nil
	}
	_ = group
	s.wake()
	return &rookeryv1.DeleteLightVMGroupResponse{State: "deleting"}, nil
}

func (s *lightVMGroupService) StartLightVMGroupMember(ctx context.Context, request *rookeryv1.StartLightVMGroupMemberRequest) (*rookeryv1.LightVMGroupMember, error) {
	return s.setMemberPower(ctx, request.GetId(), request.GetMemberId(), store.DesiredRunning)
}

func (s *lightVMGroupService) StopLightVMGroupMember(ctx context.Context, request *rookeryv1.StopLightVMGroupMemberRequest) (*rookeryv1.LightVMGroupMember, error) {
	return s.setMemberPower(ctx, request.GetId(), request.GetMemberId(), store.DesiredStopped)
}

func (s *lightVMGroupService) setMemberPower(ctx context.Context, groupID, memberID int64, desiredState string) (*rookeryv1.LightVMGroupMember, error) {
	group, err := s.store.GetLightVMGroup(ctx, groupID)
	if err != nil {
		return nil, lightVMGroupError(err)
	}
	if desiredState == store.DesiredRunning && (group.Paused || group.State == "deleting") {
		return nil, status.Error(codes.FailedPrecondition, "group is paused or deleting")
	}
	member, err := s.store.GetGroupMember(ctx, memberID)
	if err != nil {
		return nil, lightVMGroupError(err)
	}
	if member.GroupID != groupID {
		return nil, status.Error(codes.NotFound, "group member not found")
	}
	lightVMID := ""
	if vm, err := s.store.GetLightVMByGroupMemberID(ctx, memberID); err == nil {
		lightVMID = vm.ID
	} else if !errors.Is(err, store.ErrLightVMNotFound) {
		return nil, status.Error(codes.Internal, "could not read member LightVM")
	}
	if lightVMID != "" {
		if err := s.store.SetGroupMemberPower(ctx, memberID, lightVMID, desiredState); err != nil {
			return nil, lightVMGroupError(err)
		}
	} else if err := s.store.SetGroupMemberDesiredState(ctx, memberID, desiredState); err != nil {
		return nil, lightVMGroupError(err)
	}
	s.wake()
	member, err = s.store.GetGroupMember(ctx, memberID)
	if err != nil {
		return nil, lightVMGroupError(err)
	}
	return lightVMGroupMemberToProto(member), nil
}

func (s *lightVMGroupService) wake() {
	if s.waker != nil {
		s.waker.WakeFleet()
	}
}

func lightVMGroupToProto(group store.LightVMGroup) *rookeryv1.LightVMGroup {
	message := &rookeryv1.LightVMGroup{
		Id: group.ID, Name: group.Name, Description: group.Description,
		TemplateId: group.TemplateID, TemplateName: group.TemplateName,
		TemplateVersionId: group.TemplateVersionID, TemplateVersion: group.TemplateVersion,
		DesiredCount: group.DesiredCount, ActualCount: group.ActualCount,
		RunningCount: group.RunningCount, ProvisioningCount: group.ProvisioningCount,
		RemovingCount: group.RemovingCount, Drift: group.Drift, Converged: group.Converged,
		Paused: group.Paused, State: group.State, LastError: group.LastError,
		CreatedAt: timestamp(group.CreatedAt), UpdatedAt: timestamp(group.UpdatedAt),
	}
	if !group.LastReconcileAt.IsZero() {
		message.LastReconcileAt = timestamp(group.LastReconcileAt)
	}
	for _, member := range group.Members {
		message.Members = append(message.Members, lightVMGroupMemberToProto(member))
	}
	return message
}

func lightVMGroupMemberToProto(member store.LightVMGroupMember) *rookeryv1.LightVMGroupMember {
	message := &rookeryv1.LightVMGroupMember{
		Id: member.ID, Name: member.Name, WorkloadId: member.WorkloadID,
		State: member.State, TemplateVersion: member.TemplateVersion,
		Status: member.Status, DesiredState: member.DesiredState, LastError: member.LastError,
		CreatedAt: timestamp(member.CreatedAt), UpdatedAt: timestamp(member.UpdatedAt),
	}
	if !member.NextRetryAt.IsZero() {
		message.NextRetryAt = timestamp(member.NextRetryAt)
	}
	return message
}

func lightVMGroupError(err error) error {
	switch {
	case errors.Is(err, store.ErrLightVMGroupNotFound):
		return status.Error(codes.NotFound, "LightVM group not found")
	case errors.Is(err, store.ErrLightVMGroupNameTaken):
		return status.Error(codes.AlreadyExists, "a LightVM group with that name already exists")
	case errors.Is(err, store.ErrLightVMGroupNotEmpty):
		return status.Error(codes.FailedPrecondition, "group still has members; delete them first")
	case errors.Is(err, store.ErrLightVMGroupMemberNotFound):
		return status.Error(codes.NotFound, "group member not found")
	case errors.Is(err, store.ErrLightVMTemplateVersionNotFound):
		return status.Error(codes.InvalidArgument, "template version not found")
	default:
		return status.Error(codes.Internal, fmt.Sprintf("could not save LightVM group: %v", err))
	}
}
