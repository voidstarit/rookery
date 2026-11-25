package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/voidstarit/rookery/internal/store"
)

func TestLightVMTemplateAndGroupResourceCRUD(t *testing.T) {
	ctx := context.Background()
	api := newProviderTestAPI(t)
	kernel, err := api.store.CreateLightVMKernel(ctx, store.LightVMKernel{Name: "vmlinuz", Path: "/tmp/vmlinuz", SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	initramfs, err := api.store.CreateLightVMInitramfs(ctx, store.LightVMInitramfs{Name: "initramfs", Path: "/tmp/initramfs", SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}

	templateResource := &lightVMTemplateResource{}
	templateResource.client = api.client
	templateSchema := artifactSchema(t, templateResource)
	spec := lightVMTemplateSpecModel{
		VCPUs: types.Int64Value(2), MemoryMB: types.Int64Value(1024),
		CgroupClass: types.StringNull(), KernelID: types.Int64Value(kernel.ID),
		InitramfsID:      types.Int64Value(initramfs.ID),
		Image:            types.StringValue("example.test/rootfs@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"),
		Env:              types.ListNull(types.ObjectType{AttrTypes: map[string]attr.Type{"name": types.StringType, "value": types.StringType}}),
		ExecutableScript: types.StringValue("#!/bin/sh\n"),
		ExecutableArgs:   types.ListNull(types.StringType),
		Append:           types.StringValue("console=hvc0 rdinit=/init"), ConsoleEnabled: types.BoolValue(true),
		Disks:      types.ListNull(types.ObjectType{AttrTypes: map[string]attr.Type{"storage_backend_id": types.Int64Type, "size_gb": types.Int64Type, "throttle_class_id": types.Int64Type, "serial": types.StringType}}),
		Interfaces: types.ListNull(types.ObjectType{AttrTypes: map[string]attr.Type{"network_id": types.Int64Type, "ingress_acl_ids": types.ListType{ElemType: types.Int64Type}, "egress_acl_ids": types.ListType{ElemType: types.Int64Type}}}),
	}
	plan := lightVMTemplateModel{
		ID: types.Int64Null(), Name: types.StringValue("web"), Description: types.StringValue("fleet"),
		CurrentVersionID: types.Int64Null(), LatestVersionID: types.Int64Null(),
		LatestVersion: types.Int64Null(), VersionCount: types.Int64Null(), Spec: spec,
	}
	createResponse := resource.CreateResponse{State: tfsdk.State{Schema: templateSchema}}
	templateResource.Create(ctx, resource.CreateRequest{Plan: planForModel(t, templateSchema, &plan)}, &createResponse)
	if createResponse.Diagnostics.HasError() {
		t.Fatalf("template create: %v", createResponse.Diagnostics)
	}
	var created lightVMTemplateModel
	if diags := createResponse.State.Get(ctx, &created); diags.HasError() {
		t.Fatalf("template state: %v", diags)
	}
	if created.ID.ValueInt64() == 0 || created.CurrentVersionID.ValueInt64() == 0 ||
		created.LatestVersionID.ValueInt64() == 0 || created.Name.ValueString() != "web" {
		t.Fatalf("template = %+v", created)
	}

	groupResource := &lightVMGroupResource{}
	groupResource.client = api.client
	groupSchema := artifactSchema(t, groupResource)
	groupPlan := lightVMGroupModel{
		ID: types.Int64Null(), Name: types.StringValue("web-group"),
		Description: types.StringValue("fleet"), TemplateVersionID: types.Int64Value(created.CurrentVersionID.ValueInt64()),
		DesiredCount: types.Int64Value(0), Paused: types.BoolNull(),
		WaitForReady: types.BoolValue(false), Timeout: types.StringNull(),
		ForceDestroy: types.BoolValue(false), DestroyTimeout: types.StringNull(),
	}
	groupCreate := resource.CreateResponse{State: tfsdk.State{Schema: groupSchema}}
	groupResource.Create(ctx, resource.CreateRequest{Plan: planForModel(t, groupSchema, &groupPlan)}, &groupCreate)
	if groupCreate.Diagnostics.HasError() {
		t.Fatalf("group create: %v", groupCreate.Diagnostics)
	}
	var group lightVMGroupModel
	if diags := groupCreate.State.Get(ctx, &group); diags.HasError() {
		t.Fatalf("group state: %v", diags)
	}
	if group.ID.ValueInt64() == 0 || group.Name.ValueString() != "web-group" {
		t.Fatalf("group = %+v", group)
	}

	groupDelete := resource.DeleteResponse{State: stateForModel(t, groupSchema, &group)}
	groupResource.Delete(ctx, resource.DeleteRequest{State: groupDelete.State}, &groupDelete)
	if groupDelete.Diagnostics.HasError() {
		t.Fatalf("group delete: %v", groupDelete.Diagnostics)
	}

	templateDelete := resource.DeleteResponse{State: stateForModel(t, templateSchema, &created)}
	templateResource.Delete(ctx, resource.DeleteRequest{State: templateDelete.State}, &templateDelete)
	if templateDelete.Diagnostics.HasError() {
		t.Fatalf("template delete: %v", templateDelete.Diagnostics)
	}
}
