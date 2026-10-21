package provider

import (
	"context"
	"testing"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

func TestACLRulesRoundTrip(t *testing.T) {
	ctx := context.Background()
	rules := []*rookeryv1.ACLRule{{
		Action:               "permit",
		Description:          "allow web",
		Protocol:             "tcp",
		Source:               "10.0.0.0/8",
		Destination:          "10.20.0.0/16",
		SourcePortStart:      1024,
		SourcePortEnd:        65535,
		DestinationPortStart: 80,
		DestinationPortEnd:   443,
	}}
	list, diags := aclRulesToList(ctx, rules)
	if diags.HasError() {
		t.Fatalf("to list: %v", diags)
	}
	models, diags := aclRulesFromList(ctx, list)
	if diags.HasError() {
		t.Fatalf("from list: %v", diags)
	}
	if len(models) != 1 {
		t.Fatalf("got %d rules", len(models))
	}
	got := models[0]
	if got.Action.ValueString() != "permit" ||
		got.Description.ValueString() != "allow web" ||
		got.Protocol.ValueString() != "tcp" ||
		got.Source.ValueString() != "10.0.0.0/8" ||
		got.Destination.ValueString() != "10.20.0.0/16" ||
		got.SourcePortStart.ValueInt64() != 1024 ||
		got.SourcePortEnd.ValueInt64() != 65535 ||
		got.DestinationPortStart.ValueInt64() != 80 ||
		got.DestinationPortEnd.ValueInt64() != 443 {
		t.Fatalf("rule round trip = %+v", got)
	}
	protoRules := aclRulesToProto(models)
	if len(protoRules) != 1 || protoRules[0].GetAction() != "permit" || protoRules[0].GetDestinationPortEnd() != 443 {
		t.Fatalf("proto rules = %+v", protoRules)
	}
}
