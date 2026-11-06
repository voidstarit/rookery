package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

func TestDiskProvisioningFromJSON(t *testing.T) {
	value, err := diskProvisioningFromJSON(types.StringValue(`{"hostname":"web-01","ssh_authorized_keys":["ssh-ed25519 AAAA"]}`))
	if err != nil {
		t.Fatal(err)
	}
	properties := value.AsMap()
	if properties["hostname"] != "web-01" {
		t.Fatalf("properties = %+v", properties)
	}
	if keys, ok := properties["ssh_authorized_keys"].([]any); !ok || len(keys) != 1 {
		t.Fatalf("ssh keys = %+v", properties["ssh_authorized_keys"])
	}

	if value, err := diskProvisioningFromJSON(types.StringNull()); err != nil || value != nil {
		t.Fatalf("null JSON = %+v, %v", value, err)
	}
	if _, err := diskProvisioningFromJSON(types.StringValue(`[]`)); err == nil {
		t.Fatal("expected array provisioning_json to be rejected")
	}
}

func TestOptionalInt64FromUint64(t *testing.T) {
	if value := optionalInt64FromUint64(0); !value.IsNull() {
		t.Fatalf("zero value = %v, want null", value)
	}
	if value := optionalInt64FromUint64(42); value.ValueInt64() != 42 {
		t.Fatalf("value = %v, want 42", value)
	}
}

func TestSetDiskStateHostDevice(t *testing.T) {
	var state diskModel
	setDiskState(&state, &rookeryv1.Disk{
		Id: 1, Name: "host-data", SizeGb: 10, SizeBytes: 10 << 30,
		Device: "/dev/mapper/data", Source: "host-device",
	})
	if state.SizeBytes.ValueInt64() != 10<<30 {
		t.Fatalf("size bytes = %v", state.SizeBytes)
	}
	if state.Source.ValueString() != "host-device" || state.HostDevice.ValueString() != "/dev/mapper/data" {
		t.Fatalf("host state = %+v", state)
	}

	setDiskState(&state, &rookeryv1.Disk{Id: 2, Name: "lvm", Source: "lvm-thin"})
	if !state.HostDevice.IsNull() {
		t.Fatalf("lvm host device = %v, want null", state.HostDevice)
	}
	if !state.SizeBytes.IsNull() {
		t.Fatalf("lvm size bytes = %v, want null", state.SizeBytes)
	}
}
