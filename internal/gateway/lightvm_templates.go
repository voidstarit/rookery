package gateway

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

var templateImageDigestPattern = regexp.MustCompile(`@sha256:[0-9a-f]{64}$`)

type lightVMTemplateService struct {
	rookeryv1.UnimplementedLightVMTemplateServiceServer
	store *store.Store
}

func (s *lightVMTemplateService) ListLightVMTemplates(ctx context.Context, _ *rookeryv1.ListLightVMTemplatesRequest) (*rookeryv1.ListLightVMTemplatesResponse, error) {
	templates, err := s.store.ListLightVMTemplates(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list LightVM templates")
	}
	response := &rookeryv1.ListLightVMTemplatesResponse{Templates: make([]*rookeryv1.LightVMTemplate, 0, len(templates))}
	for _, template := range templates {
		response.Templates = append(response.Templates, lightVMTemplateToProto(template))
	}
	return response, nil
}

func (s *lightVMTemplateService) GetLightVMTemplate(ctx context.Context, request *rookeryv1.GetLightVMTemplateRequest) (*rookeryv1.LightVMTemplate, error) {
	template, err := s.store.GetLightVMTemplate(ctx, request.GetId())
	if err != nil {
		return nil, lightVMTemplateError(err)
	}
	return lightVMTemplateToProto(template), nil
}

func (s *lightVMTemplateService) CreateLightVMTemplate(ctx context.Context, request *rookeryv1.CreateLightVMTemplateRequest) (*rookeryv1.LightVMTemplate, error) {
	spec, err := lightVMTemplateSpecFromProto(request.GetSpec())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := s.validateSpec(ctx, spec); err != nil {
		return nil, err
	}
	template, _, err := s.store.CreateLightVMTemplate(ctx, request.GetName(), request.GetDescription(), spec)
	if err != nil {
		return nil, lightVMTemplateError(err)
	}
	return lightVMTemplateToProto(template), nil
}

func (s *lightVMTemplateService) UpdateLightVMTemplate(ctx context.Context, request *rookeryv1.UpdateLightVMTemplateRequest) (*rookeryv1.LightVMTemplate, error) {
	existing, err := s.store.GetLightVMTemplate(ctx, request.GetId())
	if err != nil {
		return nil, lightVMTemplateError(err)
	}
	name := existing.Name
	description := existing.Description
	if request.Name != nil {
		name = request.GetName()
	}
	if request.Description != nil {
		description = request.GetDescription()
	}
	template, err := s.store.UpdateLightVMTemplateMetadata(ctx, request.GetId(), name, description)
	if err != nil {
		return nil, lightVMTemplateError(err)
	}
	return lightVMTemplateToProto(template), nil
}

func (s *lightVMTemplateService) DeleteLightVMTemplate(ctx context.Context, request *rookeryv1.DeleteLightVMTemplateRequest) (*emptypb.Empty, error) {
	if err := s.store.DeleteLightVMTemplate(ctx, request.GetId()); err != nil {
		return nil, lightVMTemplateError(err)
	}
	return &emptypb.Empty{}, nil
}

func (s *lightVMTemplateService) ListLightVMTemplateVersions(ctx context.Context, request *rookeryv1.ListLightVMTemplateVersionsRequest) (*rookeryv1.ListLightVMTemplateVersionsResponse, error) {
	versions, err := s.store.ListLightVMTemplateVersions(ctx, request.GetId())
	if err != nil {
		return nil, lightVMTemplateError(err)
	}
	response := &rookeryv1.ListLightVMTemplateVersionsResponse{Versions: make([]*rookeryv1.LightVMTemplateVersionSummary, 0, len(versions))}
	for _, version := range versions {
		response.Versions = append(response.Versions, lightVMTemplateVersionSummaryToProto(version))
	}
	return response, nil
}

func (s *lightVMTemplateService) GetLightVMTemplateVersion(ctx context.Context, request *rookeryv1.GetLightVMTemplateVersionRequest) (*rookeryv1.LightVMTemplateVersion, error) {
	version, err := s.store.GetLightVMTemplateVersion(ctx, request.GetId(), request.GetVersionId())
	if err != nil {
		return nil, lightVMTemplateError(err)
	}
	return lightVMTemplateVersionToProto(version), nil
}

func (s *lightVMTemplateService) CreateLightVMTemplateVersion(ctx context.Context, request *rookeryv1.CreateLightVMTemplateVersionRequest) (*rookeryv1.LightVMTemplateVersion, error) {
	spec, err := lightVMTemplateSpecFromProto(request.GetSpec())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := s.validateSpec(ctx, spec); err != nil {
		return nil, err
	}
	version, err := s.store.CreateLightVMTemplateVersion(ctx, request.GetId(), spec)
	if err != nil {
		return nil, lightVMTemplateError(err)
	}
	return lightVMTemplateVersionToProto(version), nil
}

func (s *lightVMTemplateService) validateSpec(ctx context.Context, spec store.LightVMTemplateSpec) error {
	if spec.VCPUs < 1 || spec.VCPUs > 128 {
		return status.Error(codes.InvalidArgument, "vcpus must be between 1 and 128")
	}
	if spec.MemoryMB < 128 || spec.MemoryMB > 1048576 {
		return status.Error(codes.InvalidArgument, "memory_mb must be between 128 and 1048576")
	}
	if spec.KernelID <= 0 || spec.InitramfsID <= 0 {
		return status.Error(codes.InvalidArgument, "kernel_id and initramfs_id are required")
	}
	if !templateImageDigestPattern.MatchString(strings.TrimSpace(spec.Image)) {
		return status.Error(codes.InvalidArgument, "image must be digest-qualified (@sha256:<64 hex characters>)")
	}
	if err := s.validateTemplateReferences(ctx, spec); err != nil {
		return err
	}
	return nil
}

func (s *lightVMTemplateService) validateTemplateReferences(ctx context.Context, spec store.LightVMTemplateSpec) error {
	if _, err := s.store.GetLightVMKernel(ctx, spec.KernelID); err != nil {
		if errors.Is(err, store.ErrLightVMKernelNotFound) {
			return status.Error(codes.InvalidArgument, "kernel not found")
		}
		return status.Error(codes.Internal, "could not read kernel")
	}
	if _, err := s.store.GetLightVMInitramfs(ctx, spec.InitramfsID); err != nil {
		if errors.Is(err, store.ErrLightVMInitramfsNotFound) {
			return status.Error(codes.InvalidArgument, "initramfs not found")
		}
		return status.Error(codes.Internal, "could not read initramfs")
	}
	for _, disk := range spec.Disks {
		if disk.StorageBackendID <= 0 || disk.SizeGB < 1 || disk.SizeGB > 65536 {
			return status.Error(codes.InvalidArgument, "disk backend and size are required")
		}
	}
	for _, iface := range spec.Interfaces {
		if iface.NetworkID <= 0 {
			return status.Error(codes.InvalidArgument, "interface network_id is required")
		}
	}
	return nil
}

func lightVMTemplateSpecFromProto(message *rookeryv1.LightVMTemplateSpec) (store.LightVMTemplateSpec, error) {
	if message == nil {
		return store.LightVMTemplateSpec{}, errors.New("spec is required")
	}
	spec := store.LightVMTemplateSpec{
		VCPUs:            int(message.GetVcpus()),
		MemoryMB:         int(message.GetMemoryMb()),
		CgroupClass:      strings.TrimSpace(message.GetCgroupClass()),
		KernelID:         message.GetKernelId(),
		InitramfsID:      message.GetInitramfsId(),
		Image:            strings.TrimSpace(message.GetImage()),
		ExecutablePath:   message.GetExecutablePath(),
		ExecutableArgs:   append([]string(nil), message.GetExecutableArgs()...),
		ExecutableScript: message.GetExecutableScript(),
		Append:           message.GetAppend(),
		ConsoleEnabled:   message.GetConsoleEnabled(),
	}
	for _, env := range message.GetEnv() {
		spec.Env = append(spec.Env, store.LightVMEnvVar{Name: env.GetName(), Value: env.GetValue()})
	}
	for _, console := range message.GetConsoles() {
		spec.Consoles = append(spec.Consoles, store.LightVMConsole{Name: console.GetName()})
	}
	for _, share := range message.GetVirtiofsShares() {
		if !share.GetReadOnly() {
			return store.LightVMTemplateSpec{}, errors.New("template virtiofs shares must be read-only")
		}
		spec.VirtioFSShares = append(spec.VirtioFSShares, store.LightVMVirtioFSShare{
			HostPath: share.GetHostPath(), Tag: share.GetTag(), ReadOnly: true,
		})
	}
	for _, disk := range message.GetDisks() {
		entry := store.LightVMTemplateDisk{
			StorageBackendID: disk.GetStorageBackendId(),
			SizeGB:           disk.GetSizeGb(),
			Serial:           disk.GetSerial(),
		}
		if disk.ThrottleClassId != nil {
			id := disk.GetThrottleClassId()
			entry.ThrottleClassID = &id
		}
		spec.Disks = append(spec.Disks, entry)
	}
	for _, iface := range message.GetInterfaces() {
		spec.Interfaces = append(spec.Interfaces, store.LightVMTemplateInterface{
			NetworkID:     iface.GetNetworkId(),
			IngressACLIDs: append([]int64(nil), iface.GetIngressAclIds()...),
			EgressACLIDs:  append([]int64(nil), iface.GetEgressAclIds()...),
		})
	}
	return spec, nil
}

func lightVMTemplateToProto(template store.LightVMTemplate) *rookeryv1.LightVMTemplate {
	return &rookeryv1.LightVMTemplate{
		Id: template.ID, Name: template.Name, Description: template.Description,
		LatestVersion: template.LatestVersion, LatestVersionId: template.LatestVersionID,
		VersionCount: template.VersionCount,
		CreatedAt:    timestamp(template.CreatedAt), UpdatedAt: timestamp(template.UpdatedAt),
	}
}

func lightVMTemplateVersionToProto(version store.LightVMTemplateVersion) *rookeryv1.LightVMTemplateVersion {
	return &rookeryv1.LightVMTemplateVersion{
		Id: version.ID, TemplateId: version.TemplateID, Version: version.Version,
		Spec: lightVMTemplateSpecToProto(version.Spec), CreatedAt: timestamp(version.CreatedAt),
	}
}

func lightVMTemplateVersionSummaryToProto(version store.LightVMTemplateVersionSummary) *rookeryv1.LightVMTemplateVersionSummary {
	return &rookeryv1.LightVMTemplateVersionSummary{
		Id: version.ID, TemplateId: version.TemplateID, Version: version.Version,
		Vcpus: version.VCPUs, MemoryMb: version.MemoryMB,
		DiskCount: version.DiskCount, InterfaceCount: version.InterfaceCount,
		CreatedAt: timestamp(version.CreatedAt),
	}
}

func lightVMTemplateSpecToProto(spec store.LightVMTemplateSpec) *rookeryv1.LightVMTemplateSpec {
	message := &rookeryv1.LightVMTemplateSpec{
		Vcpus: int32(spec.VCPUs), MemoryMb: int32(spec.MemoryMB),
		CgroupClass: spec.CgroupClass, KernelId: spec.KernelID, InitramfsId: spec.InitramfsID,
		Image: spec.Image, ExecutablePath: spec.ExecutablePath,
		ExecutableArgs:   append([]string(nil), spec.ExecutableArgs...),
		ExecutableScript: spec.ExecutableScript, Append: spec.Append,
		ConsoleEnabled: spec.ConsoleEnabled,
	}
	for _, env := range spec.Env {
		message.Env = append(message.Env, &rookeryv1.LightVMEnvVar{Name: env.Name, Value: env.Value})
	}
	for _, console := range spec.Consoles {
		message.Consoles = append(message.Consoles, &rookeryv1.LightVMConsole{Name: console.Name})
	}
	for _, share := range spec.VirtioFSShares {
		readOnly := share.ReadOnly
		message.VirtiofsShares = append(message.VirtiofsShares, &rookeryv1.LightVMVirtioFSShare{
			HostPath: share.HostPath, Tag: share.Tag, ReadOnly: &readOnly,
		})
	}
	for _, disk := range spec.Disks {
		entry := &rookeryv1.TemplateDiskEntry{
			StorageBackendId: disk.StorageBackendID, SizeGb: disk.SizeGB, Serial: disk.Serial,
			StorageBackendName: disk.StorageBackendName, ThrottleClassName: disk.ThrottleClassName,
		}
		if disk.ThrottleClassID != nil {
			id := *disk.ThrottleClassID
			entry.ThrottleClassId = &id
		}
		message.Disks = append(message.Disks, entry)
	}
	for _, iface := range spec.Interfaces {
		message.Interfaces = append(message.Interfaces, &rookeryv1.TemplateInterfaceEntry{
			NetworkId: iface.NetworkID, IngressAclIds: append([]int64(nil), iface.IngressACLIDs...),
			EgressAclIds: append([]int64(nil), iface.EgressACLIDs...), NetworkName: iface.NetworkName,
			IngressAclNames: append([]string(nil), iface.IngressACLNames...),
			EgressAclNames:  append([]string(nil), iface.EgressACLNames...),
		})
	}
	return message
}

func lightVMTemplateError(err error) error {
	switch {
	case errors.Is(err, store.ErrLightVMTemplateNotFound):
		return status.Error(codes.NotFound, "LightVM template not found")
	case errors.Is(err, store.ErrLightVMTemplateVersionNotFound):
		return status.Error(codes.NotFound, "LightVM template version not found")
	case errors.Is(err, store.ErrLightVMTemplateNameTaken):
		return status.Error(codes.AlreadyExists, "a LightVM template with that name already exists")
	case errors.Is(err, store.ErrLightVMTemplateInUse):
		return status.Error(codes.FailedPrecondition, "template is pinned by a group")
	default:
		return status.Error(codes.Internal, fmt.Sprintf("could not save LightVM template: %v", err))
	}
}
