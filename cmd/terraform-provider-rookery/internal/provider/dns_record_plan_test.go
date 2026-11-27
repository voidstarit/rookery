package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

// TestDNSRecordTTLPlanModifier proves a configured TTL the API would rewrite
// fails the plan instead of being changed, while the valid range passes.
func TestDNSRecordTTLPlanModifier(t *testing.T) {
	modifier := dnsRecordTTLPlanModifier{}
	if modifier.Description(context.Background()) == "" {
		t.Fatal("Description is empty")
	}
	if modifier.MarkdownDescription(context.Background()) == "" {
		t.Fatal("MarkdownDescription is empty")
	}

	tests := []struct {
		name    string
		plan    types.Int64
		wantErr bool
	}{
		{name: "default is accepted", plan: types.Int64Value(300)},
		{name: "minimum is accepted", plan: types.Int64Value(1)},
		{name: "maximum is accepted", plan: types.Int64Value(86400)},
		{name: "zero is rejected", plan: types.Int64Value(0), wantErr: true},
		{name: "above the maximum is rejected", plan: types.Int64Value(86401), wantErr: true},
		{name: "negative is rejected", plan: types.Int64Value(-1), wantErr: true},
		{name: "null passes through", plan: types.Int64Null()},
		{name: "unknown passes through", plan: types.Int64Unknown()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := &planmodifier.Int64Response{PlanValue: test.plan}
			modifier.PlanModifyInt64(context.Background(), planmodifier.Int64Request{
				PlanValue: test.plan,
				Path:      path.Root("ttl"),
			}, response)
			if got := response.Diagnostics.HasError(); got != test.wantErr {
				t.Fatalf("diagnostics.HasError() = %v, want %v (%v)", got, test.wantErr, response.Diagnostics)
			}
			if test.wantErr {
				if summary := response.Diagnostics.Errors()[0].Summary(); summary != "Invalid DNS record TTL" {
					t.Fatalf("error summary = %q", summary)
				}
			}
		})
	}
}

// TestCanonicalDNSRecordFromModel proves the form the provider sends to the API
// is canonical, independent of the configured spelling kept in state.
func TestCanonicalDNSRecordFromModel(t *testing.T) {
	tests := []struct {
		name    string
		model   dnsRecordModel
		want    canonicalDNSRecord
		wantErr string
	}{
		{
			name: "CNAME canonicalizes the owner and target",
			model: dnsRecordModel{
				VNFID: types.Int64Value(42),
				Name:  types.StringValue("Alias.Lab.Test"),
				Type:  types.StringValue("cname"),
				Value: types.StringValue("VM1.Lab.Test"),
				TTL:   types.Int64Value(300),
			},
			want: canonicalDNSRecord{Name: "alias.lab.test.", Type: "CNAME", Value: "vm1.lab.test.", TTL: 300},
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
			want: canonicalDNSRecord{Name: "vm1.lab.test.", Type: "A", Value: "10.20.0.50", TTL: 120},
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
			want: canonicalDNSRecord{Name: "vm6.lab.test.", Type: "AAAA", Value: "fd00::50", TTL: 300},
		},
		{
			name: "wildcard name keeps its leading label",
			model: dnsRecordModel{
				VNFID: types.Int64Value(42),
				Name:  types.StringValue("*.Lab.Test"),
				Type:  types.StringValue("TXT"),
				Value: types.StringValue("  v=spf1 -all  "),
				TTL:   types.Int64Value(300),
			},
			want: canonicalDNSRecord{Name: "*.lab.test.", Type: "TXT", Value: "v=spf1 -all", TTL: 300},
		},
		{
			name: "an omitted TTL sends the API default",
			model: dnsRecordModel{
				VNFID: types.Int64Value(42),
				Name:  types.StringValue("vm1.lab.test"),
				Type:  types.StringValue("A"),
				Value: types.StringValue("10.20.0.50"),
				TTL:   types.Int64Null(),
			},
			want: canonicalDNSRecord{Name: "vm1.lab.test.", Type: "A", Value: "10.20.0.50", TTL: 300},
		},
		{
			name: "unsupported type is rejected",
			model: dnsRecordModel{
				Name:  types.StringValue("vm1.lab.test"),
				Type:  types.StringValue("SRV"),
				Value: types.StringValue("x"),
			},
			wantErr: "type must be A, AAAA, CNAME, TXT, or PTR",
		},
		{
			name: "wildcard CNAME target is rejected",
			model: dnsRecordModel{
				Name:  types.StringValue("alias.lab.test"),
				Type:  types.StringValue("CNAME"),
				Value: types.StringValue("*.lab.test"),
			},
			wantErr: "value: CNAME targets cannot be wildcards",
		},
		{
			name: "TTL above the API limit is rejected",
			model: dnsRecordModel{
				Name:  types.StringValue("vm1.lab.test"),
				Type:  types.StringValue("A"),
				Value: types.StringValue("10.20.0.50"),
				TTL:   types.Int64Value(86401),
			},
			wantErr: "ttl must be at most 86400",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := canonicalDNSRecordFromModel(test.model)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("canonicalDNSRecordFromModel: %v", err)
			}
			if got != test.want {
				t.Fatalf("canonical = %+v, want %+v", got, test.want)
			}
		})
	}
}

// TestDNSRecordCreateSendsCanonicalForm proves Create sends the canonical record
// to the API while the state keeps the configured spelling, so Terraform does
// not see an inconsistency after apply.
func TestDNSRecordCreateSendsCanonicalForm(t *testing.T) {
	ctx := context.Background()
	backend := newFakeVNFBackend()
	client := backend.client()
	recordResource := &dnsRecordResource{configuredResource: configuredResource{client: client}}
	recordSchema := resourceSchema(t, recordResource)

	model := testDNSRecordModel()
	model.Name = types.StringValue("Alias.Lab.Test")
	model.Type = types.StringValue("cname")
	model.Value = types.StringValue("VM1.Lab.Test")
	model.TTL = types.Int64Value(300)

	plan := vnfPlanFromModel(t, recordSchema, model)
	response := resource.CreateResponse{State: tfsdk.State{Schema: recordSchema}}
	recordResource.Create(ctx, resource.CreateRequest{Plan: plan, Config: configFromPlan(recordSchema, plan)}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("create: %v", response.Diagnostics)
	}

	stored := backend.records[1]
	if stored == nil {
		t.Fatal("the fake server stored no record")
	}
	if stored.GetName() != "alias.lab.test." || stored.GetType() != "CNAME" || stored.GetValue() != "vm1.lab.test." {
		t.Fatalf("stored record = %v, want the canonical form", stored)
	}

	var state dnsRecordModel
	if diags := response.State.Get(ctx, &state); diags.HasError() {
		t.Fatalf("read created state: %v", diags)
	}
	if state.Name.ValueString() != "Alias.Lab.Test" || state.Type.ValueString() != "cname" || state.Value.ValueString() != "VM1.Lab.Test" {
		t.Fatalf("state = %+v, want the configured spelling", state)
	}
	if state.ID.ValueInt64() != 1 || state.TTL.ValueInt64() != 300 {
		t.Fatalf("state identity/ttl = %+v", state)
	}
}

// TestReconcileDNSRecordState proves Read keeps the configured spelling when it
// still matches the server, fills an imported state, and overwrites real drift.
func TestReconcileDNSRecordState(t *testing.T) {
	record := &rookeryv1.DNSRecord{
		Id: 7, VnfId: 42, Name: "alias.lab.test.", Type: "CNAME", Value: "web.lab.test.", Ttl: 120,
	}

	tests := []struct {
		name  string
		state dnsRecordModel
		want  dnsRecordModel
	}{
		{
			name: "keeps the configured spelling",
			state: dnsRecordModel{
				ID: types.Int64Value(7), VNFID: types.Int64Value(42),
				Name: types.StringValue("Alias.Lab.Test"), Type: types.StringValue("cname"),
				Value: types.StringValue("Web.Lab.Test"), TTL: types.Int64Value(300),
			},
			want: dnsRecordModel{
				ID: types.Int64Value(7), VNFID: types.Int64Value(42),
				Name: types.StringValue("Alias.Lab.Test"), Type: types.StringValue("cname"),
				Value: types.StringValue("Web.Lab.Test"), TTL: types.Int64Value(120),
			},
		},
		{
			name:  "fills an imported state",
			state: dnsRecordModel{ID: types.Int64Value(7), VNFID: types.Int64Value(42)},
			want: dnsRecordModel{
				ID: types.Int64Value(7), VNFID: types.Int64Value(42),
				Name: types.StringValue("alias.lab.test."), Type: types.StringValue("CNAME"),
				Value: types.StringValue("web.lab.test."), TTL: types.Int64Value(120),
			},
		},
		{
			name: "overwrites real drift",
			state: dnsRecordModel{
				ID: types.Int64Value(7), VNFID: types.Int64Value(42),
				Name: types.StringValue("old.lab.test"), Type: types.StringValue("A"),
				Value: types.StringValue("10.0.0.1"), TTL: types.Int64Value(60),
			},
			want: dnsRecordModel{
				ID: types.Int64Value(7), VNFID: types.Int64Value(42),
				Name: types.StringValue("alias.lab.test."), Type: types.StringValue("CNAME"),
				Value: types.StringValue("web.lab.test."), TTL: types.Int64Value(120),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := reconcileDNSRecordState(test.state, record)
			if !got.Name.Equal(test.want.Name) || !got.Type.Equal(test.want.Type) ||
				!got.Value.Equal(test.want.Value) || !got.TTL.Equal(test.want.TTL) ||
				!got.ID.Equal(test.want.ID) || !got.VNFID.Equal(test.want.VNFID) {
				t.Fatalf("state = %+v, want %+v", got, test.want)
			}
		})
	}
}

// TestDNSRecordTTLDefaultIsWired proves the schema plans the API default for an
// omitted TTL and validates a configured one.
func TestDNSRecordTTLDefaultIsWired(t *testing.T) {
	var response resource.SchemaResponse
	NewDNSRecordResource().Schema(context.Background(), resource.SchemaRequest{}, &response)
	ttl, ok := response.Schema.Attributes["ttl"].(schema.Int64Attribute)
	if !ok {
		t.Fatal("ttl is not an int64 attribute")
	}
	if ttl.Default == nil {
		t.Fatal("ttl has no default")
	}
	if !ttl.IsComputed() {
		t.Fatal("a TTL default requires a computed attribute")
	}
	if len(ttl.PlanModifiers) != 1 {
		t.Fatalf("ttl has %d plan modifiers, want 1", len(ttl.PlanModifiers))
	}
}
