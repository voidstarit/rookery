package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func diskPlanModel(class types.Int64, isTemplate bool) diskModel {
	return diskModel{
		ID:                  types.Int64Value(7),
		Name:                types.StringValue("data"),
		SizeGB:              types.Int64Value(10),
		IsTemplate:          types.BoolValue(isTemplate),
		ThrottleClassID:     class,
		ProvisioningScript:  types.StringNull(),
		ProvisioningTimeout: types.StringNull(),
		ProvisioningJSON:    types.StringNull(),
		Labels:              types.SetNull(types.StringType),
	}
}

func TestDiskResourceModifyPlanTemplateThrottleClass(t *testing.T) {
	ctx := context.Background()
	value := &diskResource{}
	schema := resourceSchema(t, value)

	tests := []struct {
		name    string
		class   types.Int64
		wantErr bool
	}{
		{"null is allowed", types.Int64Null(), false},
		{"positive is rejected", types.Int64Value(7), true},
		{"unknown is rejected", types.Int64Unknown(), true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := diskPlanModel(test.class, true)
			plan := tfsdk.Plan{Schema: schema}
			if diags := plan.Set(ctx, model); diags.HasError() {
				t.Fatal(diags)
			}
			config := tfsdk.Config{Schema: schema, Raw: plan.Raw}
			response := resource.ModifyPlanResponse{Plan: plan}
			value.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, Config: config, State: tfsdk.State{Schema: schema}}, &response)
			if response.Diagnostics.HasError() != test.wantErr {
				t.Fatalf("diagnostics = %v, wantErr %v", response.Diagnostics, test.wantErr)
			}
		})
	}
}

func TestDiskResourceCreateAndUpdateThrottleClassErrors(t *testing.T) {
	ctx := context.Background()
	value := &diskResource{}
	schema := resourceSchema(t, value)

	createModel := diskPlanModel(types.Int64Unknown(), false)
	createPlan := tfsdk.Plan{Schema: schema}
	if diags := createPlan.Set(ctx, createModel); diags.HasError() {
		t.Fatal(diags)
	}
	createResponse := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	value.Create(ctx, resource.CreateRequest{
		Plan: createPlan, Config: tfsdk.Config{Schema: schema, Raw: createPlan.Raw},
	}, &createResponse)
	if !createResponse.Diagnostics.HasError() {
		t.Fatal("create with unresolved class accepted")
	}

	updateValue := &diskResource{configuredResource{client: &Client{}}}
	updateModel := diskPlanModel(types.Int64Unknown(), false)
	updatePlan := tfsdk.Plan{Schema: schema}
	if diags := updatePlan.Set(ctx, updateModel); diags.HasError() {
		t.Fatal(diags)
	}
	state := tfsdk.State{Schema: schema}
	if diags := state.Set(ctx, updateModel); diags.HasError() {
		t.Fatal(diags)
	}
	updateResponse := resource.UpdateResponse{State: tfsdk.State{Schema: schema}}
	updateValue.Update(ctx, resource.UpdateRequest{
		Plan: updatePlan, Config: tfsdk.Config{Schema: schema, Raw: updatePlan.Raw}, State: state,
	}, &updateResponse)
	if !updateResponse.Diagnostics.HasError() {
		t.Fatal("update with unresolved class accepted")
	}
}
