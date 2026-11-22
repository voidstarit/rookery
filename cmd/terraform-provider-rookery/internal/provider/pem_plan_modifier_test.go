package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestTrimSpaceStringPlanModifier(t *testing.T) {
	modifier := trimSpaceStringPlanModifier{}
	if modifier.Description(context.Background()) == "" {
		t.Fatal("Description is empty")
	}
	if modifier.MarkdownDescription(context.Background()) == "" {
		t.Fatal("MarkdownDescription is empty")
	}

	tests := []struct {
		name string
		plan types.String
		want types.String
	}{
		{name: "trims whitespace", plan: types.StringValue("  -----BEGIN CERTIFICATE-----\n"), want: types.StringValue("-----BEGIN CERTIFICATE-----")},
		{name: "null stays null", plan: types.StringNull(), want: types.StringNull()},
		{name: "unknown stays unknown", plan: types.StringUnknown(), want: types.StringUnknown()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := &planmodifier.StringResponse{PlanValue: test.plan}
			modifier.PlanModifyString(context.Background(), planmodifier.StringRequest{PlanValue: test.plan}, response)
			if !response.PlanValue.Equal(test.want) {
				t.Fatalf("PlanValue = %v, want %v", response.PlanValue, test.want)
			}
		})
	}
}
