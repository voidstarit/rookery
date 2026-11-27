package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// TestCanonicalizeDNSRecordModel covers the DNS record plan canonicalization that
// keeps a rookery_dns_record apply consistent with the form the API stores.
func TestCanonicalizeDNSRecordModel(t *testing.T) {
	tests := []struct {
		name    string
		model   dnsRecordModel
		want    dnsRecordModel
		wantErr string
	}{
		{
			name: "CNAME plans the canonical owner and target",
			model: dnsRecordModel{
				VNFID: types.Int64Value(42),
				Name:  types.StringValue("Alias.Lab.Test"),
				Type:  types.StringValue("cname"),
				Value: types.StringValue("VM1.Lab.Test"),
				TTL:   types.Int64Value(0),
			},
			want: dnsRecordModel{
				VNFID: types.Int64Value(42),
				Name:  types.StringValue("alias.lab.test."),
				Type:  types.StringValue("CNAME"),
				Value: types.StringValue("vm1.lab.test."),
				TTL:   types.Int64Value(300),
			},
		},
		{
			name: "A record trims the address",
			model: dnsRecordModel{
				VNFID: types.Int64Value(42),
				Name:  types.StringValue("vm1.lab.test"),
				Type:  types.StringValue("A"),
				Value: types.StringValue(" 10.20.0.50 "),
				TTL:   types.Int64Value(120),
			},
			want: dnsRecordModel{
				VNFID: types.Int64Value(42),
				Name:  types.StringValue("vm1.lab.test."),
				Type:  types.StringValue("A"),
				Value: types.StringValue("10.20.0.50"),
				TTL:   types.Int64Value(120),
			},
		},
		{
			name: "AAAA record compresses the address",
			model: dnsRecordModel{
				VNFID: types.Int64Value(42),
				Name:  types.StringValue("vm6.lab.test"),
				Type:  types.StringValue("AAAA"),
				Value: types.StringValue("FD00:0000::0050"),
				TTL:   types.Int64Value(300),
			},
			want: dnsRecordModel{
				VNFID: types.Int64Value(42),
				Name:  types.StringValue("vm6.lab.test."),
				Type:  types.StringValue("AAAA"),
				Value: types.StringValue("fd00::50"),
				TTL:   types.Int64Value(300),
			},
		},
		{
			name: "wildcard name keeps its leading label",
			model: dnsRecordModel{
				VNFID: types.Int64Value(42),
				Name:  types.StringValue("*.Lab.Test"),
				Type:  types.StringValue("TXT"),
				Value: types.StringValue("v=spf1 -all"),
				TTL:   types.Int64Value(300),
			},
			want: dnsRecordModel{
				VNFID: types.Int64Value(42),
				Name:  types.StringValue("*.lab.test."),
				Type:  types.StringValue("TXT"),
				Value: types.StringValue("v=spf1 -all"),
				TTL:   types.Int64Value(300),
			},
		},
		{
			name: "unknown values pass through",
			model: dnsRecordModel{
				VNFID: types.Int64Value(42),
				Name:  types.StringUnknown(),
				Type:  types.StringValue("CNAME"),
				Value: types.StringUnknown(),
				TTL:   types.Int64Unknown(),
			},
			want: dnsRecordModel{
				VNFID: types.Int64Value(42),
				Name:  types.StringUnknown(),
				Type:  types.StringValue("CNAME"),
				Value: types.StringUnknown(),
				TTL:   types.Int64Unknown(),
			},
		},
		{
			name: "unsupported type is rejected",
			model: dnsRecordModel{
				Name:  types.StringValue("vm1.lab.test"),
				Type:  types.StringValue("SRV"),
				Value: types.StringValue("x"),
			},
			want: dnsRecordModel{
				Name:  types.StringValue("vm1.lab.test."),
				Type:  types.StringValue("SRV"),
				Value: types.StringValue("x"),
			},
			wantErr: "Invalid DNS record type",
		},
		{
			name: "wildcard CNAME target is rejected",
			model: dnsRecordModel{
				Name:  types.StringValue("alias.lab.test"),
				Type:  types.StringValue("CNAME"),
				Value: types.StringValue("*.lab.test"),
			},
			want: dnsRecordModel{
				Name:  types.StringValue("alias.lab.test."),
				Type:  types.StringValue("CNAME"),
				Value: types.StringValue("*.lab.test"),
			},
			wantErr: "Invalid DNS record value",
		},
		{
			name: "TTL over the API limit is rejected",
			model: dnsRecordModel{
				Name:  types.StringValue("vm1.lab.test"),
				Type:  types.StringValue("A"),
				Value: types.StringValue("10.20.0.50"),
				TTL:   types.Int64Value(86401),
			},
			want: dnsRecordModel{
				Name:  types.StringValue("vm1.lab.test."),
				Type:  types.StringValue("A"),
				Value: types.StringValue("10.20.0.50"),
				TTL:   types.Int64Value(86401),
			},
			wantErr: "Invalid DNS record TTL",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := test.model
			diags := canonicalizeDNSRecordModel(&model)
			if test.wantErr != "" {
				if !diags.HasError() {
					t.Fatal("expected a diagnostic")
				}
				if got := diags.Errors()[0].Summary(); got != test.wantErr {
					t.Fatalf("error summary = %q, want %q", got, test.wantErr)
				}
			} else if diags.HasError() {
				t.Fatalf("unexpected diagnostic: %v", diags)
			}
			if !model.Name.Equal(test.want.Name) || !model.Type.Equal(test.want.Type) ||
				!model.Value.Equal(test.want.Value) || !model.TTL.Equal(test.want.TTL) {
				t.Fatalf("model = %+v, want %+v", model, test.want)
			}
		})
	}
}

// TestDNSRecordModifyPlan proves the resource plans a canonical record through the
// framework, including computed attributes that are still unknown at plan time.
func TestDNSRecordModifyPlan(t *testing.T) {
	ctx := context.Background()
	recordResource := NewDNSRecordResource()
	modifier, ok := recordResource.(resource.ResourceWithModifyPlan)
	if !ok {
		t.Fatal("dns record resource does not implement ResourceWithModifyPlan")
	}
	var schemaResponse resource.SchemaResponse
	recordResource.Schema(ctx, resource.SchemaRequest{}, &schemaResponse)

	plan := tfsdk.Plan{
		Schema: schemaResponse.Schema,
		Raw: tftypes.NewValue(schemaResponse.Schema.Type().TerraformType(ctx), map[string]tftypes.Value{
			"id":     tftypes.NewValue(tftypes.Number, tftypes.UnknownValue),
			"vnf_id": tftypes.NewValue(tftypes.Number, int64(42)),
			"name":   tftypes.NewValue(tftypes.String, "Alias.Lab.Test"),
			"type":   tftypes.NewValue(tftypes.String, "cname"),
			"value":  tftypes.NewValue(tftypes.String, "VM1.Lab.Test"),
			"ttl":    tftypes.NewValue(tftypes.Number, tftypes.UnknownValue),
		}),
	}
	response := &resource.ModifyPlanResponse{Plan: plan}
	modifier.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("ModifyPlan diagnostics: %v", response.Diagnostics)
	}

	var name, recordType, value types.String
	response.Plan.GetAttribute(ctx, path.Root("name"), &name)
	response.Plan.GetAttribute(ctx, path.Root("type"), &recordType)
	response.Plan.GetAttribute(ctx, path.Root("value"), &value)
	if name.ValueString() != "alias.lab.test." {
		t.Fatalf("planned name = %q, want alias.lab.test.", name.ValueString())
	}
	if recordType.ValueString() != "CNAME" {
		t.Fatalf("planned type = %q, want CNAME", recordType.ValueString())
	}
	if value.ValueString() != "vm1.lab.test." {
		t.Fatalf("planned value = %q, want vm1.lab.test.", value.ValueString())
	}
}
