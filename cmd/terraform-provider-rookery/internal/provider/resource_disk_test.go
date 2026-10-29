package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
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
