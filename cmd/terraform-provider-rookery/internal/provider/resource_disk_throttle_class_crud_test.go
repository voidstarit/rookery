package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

func testDiskThrottleClassModel() diskThrottleClassModel {
	return diskThrottleClassModel{
		ID:              types.Int64Null(),
		Name:            types.StringValue("standard"),
		Description:     types.StringValue("test class"),
		BPSTotal:        types.Int64Value(0),
		BPSTotalMax:     types.Int64Value(0),
		BPSTotalMaxLen:  types.Int64Value(0),
		BPSRead:         types.Int64Value(1_000_000),
		BPSReadMax:      types.Int64Value(0),
		BPSReadMaxLen:   types.Int64Value(0),
		BPSWrite:        types.Int64Value(0),
		BPSWriteMax:     types.Int64Value(0),
		BPSWriteMaxLen:  types.Int64Value(0),
		IOPSTotal:       types.Int64Value(0),
		IOPSTotalMax:    types.Int64Value(0),
		IOPSTotalMaxLen: types.Int64Value(0),
		IOPSRead:        types.Int64Value(0),
		IOPSReadMax:     types.Int64Value(0),
		IOPSReadMaxLen:  types.Int64Value(0),
		IOPSWrite:       types.Int64Value(0),
		IOPSWriteMax:    types.Int64Value(0),
		IOPSWriteMaxLen: types.Int64Value(0),
		IOPSSize:        types.Int64Value(0),
		ValidationError: types.StringValue(""),
		DiskCount:       types.Int64Value(0),
	}
}

func resourceSchema(t *testing.T, value resource.Resource) rschema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	value.Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema: %v", response.Diagnostics)
	}
	return response.Schema
}

func planFromModel(t *testing.T, schema rschema.Schema, model diskThrottleClassModel) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Schema: schema}
	if diags := plan.Set(context.Background(), model); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	return plan
}

func stateFromModel(t *testing.T, schema rschema.Schema, model diskThrottleClassModel) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: schema}
	if diags := state.Set(context.Background(), model); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	return state
}

func configFromPlan(schema rschema.Schema, plan tfsdk.Plan) tfsdk.Config {
	return tfsdk.Config{Schema: schema, Raw: plan.Raw}
}

func TestDiskThrottleClassResourceCRUD(t *testing.T) {
	ctx := context.Background()
	fake := &fakeDiskThrottleClient{}
	client := &Client{DiskThrottleClasses: fake}
	value := &diskThrottleClassResource{configuredResource{client: client}}
	schema := resourceSchema(t, value)

	model := testDiskThrottleClassModel()
	plan := planFromModel(t, schema, model)
	config := configFromPlan(schema, plan)
	createResponse := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	value.Create(ctx, resource.CreateRequest{Plan: plan, Config: config}, &createResponse)
	if createResponse.Diagnostics.HasError() {
		t.Fatalf("create: %v", createResponse.Diagnostics)
	}
	if fake.lastCreate.GetName() != "standard" || fake.lastCreate.GetLimits().GetBpsRead() != 1_000_000 {
		t.Fatalf("create request = %+v", fake.lastCreate)
	}
	var created diskThrottleClassModel
	if diags := createResponse.State.Get(ctx, &created); diags.HasError() {
		t.Fatalf("read created state: %v", diags)
	}
	if created.ID.ValueInt64() != 7 {
		t.Fatalf("created model = %+v", created)
	}

	fake.class = &rookeryv1.DiskThrottleClass{
		Id: 7, Name: "standard", Description: "test class",
		Limits: &rookeryv1.DiskThrottleLimits{BpsRead: 1_000_000},
	}
	readResponse := resource.ReadResponse{State: tfsdk.State{Schema: schema}}
	value.Read(ctx, resource.ReadRequest{State: stateFromModel(t, schema, created)}, &readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("read: %v", readResponse.Diagnostics)
	}

	updated := created
	updated.Description = types.StringValue("updated")
	updatePlan := planFromModel(t, schema, updated)
	updateConfig := configFromPlan(schema, updatePlan)
	updateResponse := resource.UpdateResponse{State: tfsdk.State{Schema: schema}}
	value.Update(ctx, resource.UpdateRequest{Plan: updatePlan, Config: updateConfig, State: stateFromModel(t, schema, created)}, &updateResponse)
	if updateResponse.Diagnostics.HasError() {
		t.Fatalf("update: %v", updateResponse.Diagnostics)
	}
	if fake.lastUpdate.GetDescription() != "updated" || fake.lastUpdate.GetLimits().GetBpsRead() != 1_000_000 {
		t.Fatalf("update request = %+v", fake.lastUpdate)
	}

	deleteResponse := resource.DeleteResponse{State: tfsdk.State{Schema: schema}}
	value.Delete(ctx, resource.DeleteRequest{State: stateFromModel(t, schema, created)}, &deleteResponse)
	if deleteResponse.Diagnostics.HasError() || fake.deleted != 7 {
		t.Fatalf("delete: %v, id=%d", deleteResponse.Diagnostics, fake.deleted)
	}

	importResponse := resource.ImportStateResponse{State: stateFromModel(t, schema, testDiskThrottleClassModel())}
	value.ImportState(ctx, resource.ImportStateRequest{ID: "7"}, &importResponse)
	if importResponse.Diagnostics.HasError() {
		t.Fatalf("import: %v", importResponse.Diagnostics)
	}
}

func TestDiskThrottleClassDataSourceRead(t *testing.T) {
	ctx := context.Background()
	fake := &fakeDiskThrottleClient{
		class: &rookeryv1.DiskThrottleClass{
			Id: 7, Name: "standard", Description: "test",
			Limits: &rookeryv1.DiskThrottleLimits{BpsRead: 1_000_000}, DiskCount: 2,
		},
		list: []*rookeryv1.DiskThrottleClass{
			{Id: 7, Name: "standard", Limits: &rookeryv1.DiskThrottleLimits{BpsRead: 1_000_000}},
		},
	}
	client := &Client{DiskThrottleClasses: fake}
	value := &diskThrottleClassDataSource{configuredDataSource{client: client}}
	var schemaResponse datasource.SchemaResponse
	value.Schema(ctx, datasource.SchemaRequest{}, &schemaResponse)
	if schemaResponse.Diagnostics.HasError() {
		t.Fatalf("schema: %v", schemaResponse.Diagnostics)
	}
	schema := schemaResponse.Schema

	byID := testDiskThrottleClassModel()
	byID.ID = types.Int64Value(7)
	byID.Name = types.StringNull()
	byIDPlan := tfsdk.Plan{Schema: schema}
	if diags := byIDPlan.Set(ctx, byID); diags.HasError() {
		t.Fatalf("set by-id config: %v", diags)
	}
	config := tfsdk.Config{Schema: schema, Raw: byIDPlan.Raw}
	response := datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
	value.Read(ctx, datasource.ReadRequest{Config: config}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read by id: %v", response.Diagnostics)
	}
	var model diskThrottleClassModel
	if diags := response.State.Get(ctx, &model); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	if model.ID.ValueInt64() != 7 || model.DiskCount.ValueInt64() != 2 {
		t.Fatalf("by-id model = %+v", model)
	}

	byName := testDiskThrottleClassModel()
	byName.ID = types.Int64Null()
	byName.Name = types.StringValue("STANDARD")
	byNamePlan := tfsdk.Plan{Schema: schema}
	if diags := byNamePlan.Set(ctx, byName); diags.HasError() {
		t.Fatalf("set by-name config: %v", diags)
	}
	config = tfsdk.Config{Schema: schema, Raw: byNamePlan.Raw}
	response = datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
	value.Read(ctx, datasource.ReadRequest{Config: config}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read by name: %v", response.Diagnostics)
	}
}

func TestDiskThrottleClassResourceErrors(t *testing.T) {
	ctx := context.Background()
	var metadata resource.MetadataResponse
	(&diskThrottleClassResource{}).Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "rookery"}, &metadata)
	if metadata.TypeName != "rookery_disk_throttle_class" {
		t.Fatalf("metadata type = %q", metadata.TypeName)
	}
	fake := &fakeDiskThrottleClient{}
	client := &Client{DiskThrottleClasses: fake}
	value := &diskThrottleClassResource{configuredResource{client: client}}
	schema := resourceSchema(t, value)

	invalid := testDiskThrottleClassModel()
	invalid.Name = types.StringValue("")
	invalidPlan := planFromModel(t, schema, invalid)
	createResponse := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	value.Create(ctx, resource.CreateRequest{Plan: invalidPlan, Config: configFromPlan(schema, invalidPlan)}, &createResponse)
	if !createResponse.Diagnostics.HasError() {
		t.Fatal("invalid create accepted")
	}

	noClient := &diskThrottleClassResource{}
	validPlan := planFromModel(t, schema, testDiskThrottleClassModel())
	createResponse = resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	noClient.Create(ctx, resource.CreateRequest{Plan: validPlan, Config: configFromPlan(schema, validPlan)}, &createResponse)
	if !createResponse.Diagnostics.HasError() {
		t.Fatal("create without client accepted")
	}

	fake.createErr = errors.New("boom")
	createResponse = resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	value.Create(ctx, resource.CreateRequest{Plan: validPlan, Config: configFromPlan(schema, validPlan)}, &createResponse)
	if !createResponse.Diagnostics.HasError() {
		t.Fatal("create API error accepted")
	}
	fake.createErr = nil

	state := stateFromModel(t, schema, testDiskThrottleClassModel())
	readResponse := resource.ReadResponse{State: state}
	noClient.Read(ctx, resource.ReadRequest{State: state}, &readResponse)
	if !readResponse.Diagnostics.HasError() {
		t.Fatal("read without client accepted")
	}

	fake.getErr = status.Error(codes.NotFound, "gone")
	readResponse = resource.ReadResponse{State: stateFromModel(t, schema, testDiskThrottleClassModel())}
	value.Read(ctx, resource.ReadRequest{State: state}, &readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("not-found read: %v", readResponse.Diagnostics)
	}
	fake.getErr = errors.New("boom")
	readResponse = resource.ReadResponse{State: stateFromModel(t, schema, testDiskThrottleClassModel())}
	value.Read(ctx, resource.ReadRequest{State: state}, &readResponse)
	if !readResponse.Diagnostics.HasError() {
		t.Fatal("read API error accepted")
	}
	fake.getErr = nil

	updateResponse := resource.UpdateResponse{State: tfsdk.State{Schema: schema}}
	noClient.Update(ctx, resource.UpdateRequest{
		Plan: validPlan, Config: configFromPlan(schema, validPlan), State: state,
	}, &updateResponse)
	if !updateResponse.Diagnostics.HasError() {
		t.Fatal("update without client accepted")
	}
	invalidUpdate := planFromModel(t, schema, invalid)
	updateResponse = resource.UpdateResponse{State: tfsdk.State{Schema: schema}}
	value.Update(ctx, resource.UpdateRequest{
		Plan: invalidUpdate, Config: configFromPlan(schema, invalidUpdate), State: state,
	}, &updateResponse)
	if !updateResponse.Diagnostics.HasError() {
		t.Fatal("invalid update accepted")
	}
	fake.updateErr = errors.New("boom")
	updateResponse = resource.UpdateResponse{State: tfsdk.State{Schema: schema}}
	value.Update(ctx, resource.UpdateRequest{
		Plan: validPlan, Config: configFromPlan(schema, validPlan), State: state,
	}, &updateResponse)
	if !updateResponse.Diagnostics.HasError() {
		t.Fatal("update API error accepted")
	}
	fake.updateErr = nil

	deleteResponse := resource.DeleteResponse{State: state}
	noClient.Delete(ctx, resource.DeleteRequest{State: state}, &deleteResponse)
	if !deleteResponse.Diagnostics.HasError() {
		t.Fatal("delete without client accepted")
	}
	fake.deleteErr = errors.New("boom")
	deleteResponse = resource.DeleteResponse{State: state}
	value.Delete(ctx, resource.DeleteRequest{State: state}, &deleteResponse)
	if !deleteResponse.Diagnostics.HasError() {
		t.Fatal("delete API error accepted")
	}
	fake.deleteErr = nil

	importResponse := resource.ImportStateResponse{State: stateFromModel(t, schema, testDiskThrottleClassModel())}
	value.ImportState(ctx, resource.ImportStateRequest{ID: "nope"}, &importResponse)
	if !importResponse.Diagnostics.HasError() {
		t.Fatal("invalid import id accepted")
	}
}

func TestDiskThrottleClassDataSourceErrors(t *testing.T) {
	ctx := context.Background()
	var metadata datasource.MetadataResponse
	(&diskThrottleClassDataSource{}).Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "rookery"}, &metadata)
	if metadata.TypeName != "rookery_disk_throttle_class" {
		t.Fatalf("metadata type = %q", metadata.TypeName)
	}
	fake := &fakeDiskThrottleClient{}
	client := &Client{DiskThrottleClasses: fake}
	value := &diskThrottleClassDataSource{configuredDataSource{client: client}}
	var schemaResponse datasource.SchemaResponse
	value.Schema(ctx, datasource.SchemaRequest{}, &schemaResponse)
	schema := schemaResponse.Schema

	both := testDiskThrottleClassModel()
	both.ID = types.Int64Value(7)
	both.Name = types.StringValue("standard")
	plan := tfsdk.Plan{Schema: schema}
	if diags := plan.Set(ctx, both); diags.HasError() {
		t.Fatal(diags)
	}
	response := datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
	value.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schema, Raw: plan.Raw}}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("both id and name accepted")
	}

	noClient := &diskThrottleClassDataSource{}
	byID := testDiskThrottleClassModel()
	byID.ID = types.Int64Value(7)
	byID.Name = types.StringNull()
	plan = tfsdk.Plan{Schema: schema}
	if diags := plan.Set(ctx, byID); diags.HasError() {
		t.Fatal(diags)
	}
	response = datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
	noClient.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schema, Raw: plan.Raw}}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("data source read without client accepted")
	}

	fake.getErr = errors.New("boom")
	response = datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
	value.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schema, Raw: plan.Raw}}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("data source get error accepted")
	}
	fake.getErr = nil

	byName := testDiskThrottleClassModel()
	byName.ID = types.Int64Null()
	byName.Name = types.StringValue("standard")
	plan = tfsdk.Plan{Schema: schema}
	if diags := plan.Set(ctx, byName); diags.HasError() {
		t.Fatal(diags)
	}
	fake.listErr = errors.New("boom")
	response = datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
	value.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schema, Raw: plan.Raw}}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("data source list error accepted")
	}
}
