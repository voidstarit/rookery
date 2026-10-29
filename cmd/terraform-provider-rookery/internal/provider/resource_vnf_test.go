package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/protobuf/proto"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

func dhcpVNF() *rookeryv1.VNF {
	return &rookeryv1.VNF{
		Id:        7,
		Name:      "admin_infra_dhcpv4",
		NetworkId: 2,
		Type:      "dhcpv4",
		Enabled:   true,
		Config: &rookeryv1.VNFConfig{
			Gateway:    "10.20.0.1/16",
			RangeStart: "10.20.0.10",
			RangeEnd:   "10.20.0.100",
			LeaseTime:  "12h",
			BindIp:     "10.20.0.2/16",
		},
	}
}

// TestSetVNFStateKeepsPlannedConfigJSON reproduces the "Provider produced
// inconsistent result after apply" failure: the server normalizes the config
// (default lease_time, derived bind_ip and gateway here), so storing its
// re-encoding would not match the planned value Terraform verifies.
func TestSetVNFStateKeepsPlannedConfigJSON(t *testing.T) {
	planned := types.StringValue("{\n  \"gateway\": \"10.20.0.1/16\",\n  \"range_end\": \"10.20.0.100\",\n  \"range_start\": \"10.20.0.10\"\n}")
	state := vnfModel{ConfigJSON: planned}
	diags := setVNFState(&state, dhcpVNF())
	if diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	if !state.ConfigJSON.Equal(planned) {
		t.Fatalf("config_json = %s, want the planned value %s", state.ConfigJSON.ValueString(), planned.ValueString())
	}
}

// TestSetVNFStateKeepsStoredConfigJSON covers refresh: a stored value must
// survive the server's normalization instead of being rewritten, or every plan
// would report a config_json diff.
func TestSetVNFStateKeepsStoredConfigJSON(t *testing.T) {
	stored := types.StringValue(`{"gateway":"10.20.0.1/16","range_start":"10.20.0.10","range_end":"10.20.0.100"}`)
	state := vnfModel{ConfigJSON: stored}
	diags := setVNFState(&state, dhcpVNF())
	if diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	if !state.ConfigJSON.Equal(stored) {
		t.Fatalf("config_json = %s, want the stored value %s", state.ConfigJSON.ValueString(), stored.ValueString())
	}
}

func TestSetVNFStateEncodesConfigWithoutValue(t *testing.T) {
	for name, value := range map[string]types.String{
		"null":    types.StringNull(),
		"unknown": types.StringUnknown(),
	} {
		t.Run(name, func(t *testing.T) {
			state := vnfModel{ConfigJSON: value}
			diags := setVNFState(&state, dhcpVNF())
			if diags.HasError() {
				t.Fatalf("set state: %v", diags)
			}
			if state.ConfigJSON.IsUnknown() || state.ConfigJSON.IsNull() {
				t.Fatalf("config_json = %#v, want encoded config", state.ConfigJSON)
			}
			decoded, err := vnfConfigFromJSON(state.ConfigJSON)
			if err != nil {
				t.Fatalf("decode config_json: %v", err)
			}
			if !proto.Equal(decoded, dhcpVNF().GetConfig()) {
				t.Fatalf("config_json = %s, want equivalent of %v", state.ConfigJSON.ValueString(), dhcpVNF().GetConfig())
			}
		})
	}
}
