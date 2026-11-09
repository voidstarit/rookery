package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/diskthrottle"
)

type fakeDiskThrottleClient struct {
	class      *rookeryv1.DiskThrottleClass
	list       []*rookeryv1.DiskThrottleClass
	lastCreate *rookeryv1.CreateDiskThrottleClassRequest
	lastUpdate *rookeryv1.UpdateDiskThrottleClassRequest
	deleted    int64
	getErr     error
	createErr  error
	updateErr  error
	deleteErr  error
	listErr    error
}

func (f *fakeDiskThrottleClient) ListDiskThrottleClasses(context.Context, *rookeryv1.ListDiskThrottleClassesRequest, ...grpc.CallOption) (*rookeryv1.ListDiskThrottleClassesResponse, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return &rookeryv1.ListDiskThrottleClassesResponse{Classes: f.list}, nil
}

func (f *fakeDiskThrottleClient) GetDiskThrottleClass(context.Context, *rookeryv1.GetDiskThrottleClassRequest, ...grpc.CallOption) (*rookeryv1.DiskThrottleClass, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.class == nil {
		return nil, status.Error(codes.NotFound, "not found")
	}
	return f.class, nil
}

func (f *fakeDiskThrottleClient) CreateDiskThrottleClass(_ context.Context, request *rookeryv1.CreateDiskThrottleClassRequest, _ ...grpc.CallOption) (*rookeryv1.DiskThrottleClass, error) {
	f.lastCreate = request
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.class != nil {
		return f.class, nil
	}
	return &rookeryv1.DiskThrottleClass{Id: 7, Name: request.GetName(), Description: request.GetDescription(), Limits: request.GetLimits()}, nil
}

func (f *fakeDiskThrottleClient) UpdateDiskThrottleClass(_ context.Context, request *rookeryv1.UpdateDiskThrottleClassRequest, _ ...grpc.CallOption) (*rookeryv1.DiskThrottleClass, error) {
	f.lastUpdate = request
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	if f.class != nil {
		return f.class, nil
	}
	return &rookeryv1.DiskThrottleClass{Id: request.GetId(), Name: request.GetName(), Description: request.GetDescription(), Limits: request.GetLimits()}, nil
}

func (f *fakeDiskThrottleClient) DeleteDiskThrottleClass(_ context.Context, request *rookeryv1.DeleteDiskThrottleClassRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	f.deleted = request.GetId()
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	return &emptypb.Empty{}, nil
}

func TestDiskThrottleClassModelRoundTrip(t *testing.T) {
	class := &rookeryv1.DiskThrottleClass{
		Id:          7,
		Name:        "standard",
		Description: "test",
		Limits: &rookeryv1.DiskThrottleLimits{
			BpsRead: 1_000_000, IopsTotal: 500, IopsSize: 4096,
			BpsReadMax: 2_000_000, BpsReadMaxLength: 5,
		},
		DiskCount:       3,
		ValidationError: "corrupt",
	}
	model := diskThrottleClassModelFromProto(class)
	if model.ID.ValueInt64() != 7 || model.Name.ValueString() != "standard" ||
		model.ValidationError.ValueString() != "corrupt" || model.DiskCount.ValueInt64() != 3 {
		t.Fatalf("model = %+v", model)
	}
	limits := diskThrottleLimitsFromModel(model)
	want := diskthrottle.Limits{
		BPSRead: 1_000_000, BPSReadMax: 2_000_000, BPSReadMaxLength: 5,
		IOPSTotal: 500, IOPSSize: 4096,
	}
	if limits != want {
		t.Fatalf("limits = %+v, want %+v", limits, want)
	}
	if err := validateDiskThrottleClassModel(model); err != nil {
		t.Fatalf("valid model rejected: %v", err)
	}
	model.BPSRead = types.Int64Value(-1)
	if err := validateDiskThrottleClassModel(model); err == nil {
		t.Fatal("negative limit accepted")
	}
	model = diskThrottleClassModelFromProto(class)
	model.Name = types.StringValue("bad name")
	if err := validateDiskThrottleClassModel(model); err == nil {
		t.Fatal("bad name accepted")
	}
	model.Name = types.StringValue("")
	if err := validateDiskThrottleClassModel(model); err == nil {
		t.Fatal("empty name accepted")
	}
	model.Name = types.StringValue("standard")
	model.Description = types.StringValue(strings.Repeat("a", 2001))
	if err := validateDiskThrottleClassModel(model); err == nil {
		t.Fatal("long description accepted")
	}
	model.Description = types.StringValue("ok")
	model.BPSRead = types.Int64Null()
	if limits := diskThrottleLimitsFromModel(model); limits.BPSRead != 0 {
		t.Fatalf("null limit = %+v", limits)
	}
	model.Name = types.StringValue("")
	if diags := diskThrottleClassModelDiagnostics(model); !diags.HasError() {
		t.Fatal("invalid model diagnostics were empty")
	}
}

func TestCreateAndUpdateThrottleClassID(t *testing.T) {
	if value, err := createThrottleClassID(types.Int64Null(), types.Int64Unknown()); err != nil || value != 0 {
		t.Fatalf("null create = %d, %v", value, err)
	}
	if value, err := createThrottleClassID(types.Int64Unknown(), types.Int64Value(7)); err != nil || value != 7 {
		t.Fatalf("resolved create = %d, %v", value, err)
	}
	if _, err := createThrottleClassID(types.Int64Unknown(), types.Int64Unknown()); err == nil {
		t.Fatal("unresolved create accepted")
	}
	if value, err := updateThrottleClassID(types.Int64Null(), types.Int64Value(7)); err != nil || value != nil {
		t.Fatalf("null update = %v, %v", value, err)
	}
	value, err := updateThrottleClassID(types.Int64Value(0), types.Int64Value(7))
	if err != nil || value == nil || *value != 0 {
		t.Fatalf("zero update = %v, %v", value, err)
	}
	value, err = updateThrottleClassID(types.Int64Unknown(), types.Int64Value(7))
	if err != nil || value == nil || *value != 7 {
		t.Fatalf("resolved update = %v, %v", value, err)
	}
	if _, err := updateThrottleClassID(types.Int64Unknown(), types.Int64Unknown()); err == nil {
		t.Fatal("unresolved update accepted")
	}
}

func TestBuildDiskUpdateRequestThrottleClass(t *testing.T) {
	plan := diskModel{
		ID:                  types.Int64Value(3),
		IsTemplate:          types.BoolValue(false),
		ThrottleClassID:     types.Int64Value(7),
		ProvisioningScript:  types.StringNull(),
		ProvisioningTimeout: types.StringNull(),
	}
	request, err := buildDiskUpdateRequest(plan, types.Int64Value(7))
	if err != nil {
		t.Fatal(err)
	}
	if request.GetThrottleClassId() != 7 || request.GetIsTemplate() {
		t.Fatalf("request = %+v", request)
	}
	request, err = buildDiskUpdateRequest(plan, types.Int64Null())
	if err != nil || request.ThrottleClassId != nil {
		t.Fatalf("preserve request = %+v, %v", request, err)
	}
	plan.ThrottleClassID = types.Int64Unknown()
	if _, err := buildDiskUpdateRequest(plan, types.Int64Unknown()); err == nil {
		t.Fatal("unresolved update accepted")
	}
}

func TestValidateTemplateThrottleClassConfig(t *testing.T) {
	if err := validateTemplateThrottleClassConfig(true, types.Int64Null()); err != nil {
		t.Fatalf("null config rejected: %v", err)
	}
	if err := validateTemplateThrottleClassConfig(true, types.Int64Value(7)); err == nil {
		t.Fatal("positive template class accepted")
	}
	if err := validateTemplateThrottleClassConfig(true, types.Int64Unknown()); err == nil {
		t.Fatal("unknown template class accepted")
	}
	if err := validateTemplateThrottleClassConfig(false, types.Int64Value(7)); err != nil {
		t.Fatalf("non-template rejected: %v", err)
	}
}

func TestFindDiskThrottleClassByName(t *testing.T) {
	one := &rookeryv1.DiskThrottleClass{Id: 1, Name: "standard"}
	if found, err := findDiskThrottleClass([]*rookeryv1.DiskThrottleClass{one}, "STANDARD"); err != nil || found != one {
		t.Fatalf("find = %+v, %v", found, err)
	}
	if _, err := findDiskThrottleClass(nil, "missing"); err == nil {
		t.Fatal("missing class accepted")
	}
	if _, err := findDiskThrottleClass([]*rookeryv1.DiskThrottleClass{
		one, &rookeryv1.DiskThrottleClass{Id: 2, Name: "Standard"},
	}, "standard"); err == nil {
		t.Fatal("duplicate class accepted")
	}
}

func TestFakeDiskThrottleClientExposesCorruptClass(t *testing.T) {
	fake := &fakeDiskThrottleClient{class: &rookeryv1.DiskThrottleClass{
		Id: 1, Name: "corrupt", ValidationError: "decode limits: invalid",
	}}
	client := &Client{DiskThrottleClasses: fake}
	class, err := client.DiskThrottleClasses.GetDiskThrottleClass(context.Background(), &rookeryv1.GetDiskThrottleClassRequest{Id: 1})
	if err != nil {
		t.Fatal(err)
	}
	model := diskThrottleClassModelFromProto(class)
	if model.ValidationError.ValueString() == "" {
		t.Fatal("validation error was not exposed")
	}
}
