package provider

import (
	"context"
	"fmt"

	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

// labelsResourceAttribute returns the optional label set shared by resources,
// preserving known state while the planned value is unknown.
func labelsResourceAttribute() schema.SetAttribute {
	return schema.SetAttribute{
		Optional:      true,
		Computed:      true,
		ElementType:   types.StringType,
		Description:   "Free-form labels assigned to the entity. Any number of labels can be assigned.",
		PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
	}
}

// labelsDataSourceAttribute returns the computed label set shared by data
// sources.
func labelsDataSourceAttribute() dschema.SetAttribute {
	return dschema.SetAttribute{
		Computed:    true,
		ElementType: types.StringType,
		Description: "Free-form labels assigned to the entity.",
	}
}

// labelsFromSet extracts labels from a Terraform set, returning nil without
// diagnostics when the set is null or unknown.
func labelsFromSet(ctx context.Context, value types.Set) ([]string, diag.Diagnostics) {
	if value.IsNull() || value.IsUnknown() {
		return nil, nil
	}
	var labels []string
	diags := value.ElementsAs(ctx, &labels, false)
	return labels, diags
}

// labelsToSet converts labels to a Terraform string set, mapping nil to an empty
// set.
func labelsToSet(ctx context.Context, labels []string) (types.Set, diag.Diagnostics) {
	if labels == nil {
		labels = []string{}
	}
	return types.SetValueFrom(ctx, types.StringType, labels)
}

// applyEntityLabels replaces the labels on one entity through the API and returns
// the resulting set, which may differ from the planned labels.
func applyEntityLabels(ctx context.Context, client *Client, entityType, entityID string, planned types.Set) (types.Set, error) {
	values, diags := labelsFromSet(ctx, planned)
	if diags.HasError() {
		return types.SetNull(types.StringType), fmt.Errorf("invalid labels")
	}
	updated, err := client.Labels.SetEntityLabels(ctx, &rookeryv1.SetEntityLabelsRequest{
		EntityType: entityType,
		EntityId:   entityID,
		Labels:     values,
	})
	if err != nil {
		return types.SetNull(types.StringType), err
	}
	result, diags := labelsToSet(ctx, updated.GetLabels())
	if diags.HasError() {
		return types.SetNull(types.StringType), fmt.Errorf("could not convert labels")
	}
	return result, nil
}

// readEntityLabels fetches an entity's labels from the API and converts them to a
// Terraform set, returning a null set on error.
func readEntityLabels(ctx context.Context, client *Client, entityType, entityID string) (types.Set, error) {
	record, err := client.Labels.GetEntityLabels(ctx, &rookeryv1.GetEntityLabelsRequest{
		EntityType: entityType,
		EntityId:   entityID,
	})
	if err != nil {
		return types.SetNull(types.StringType), err
	}
	result, diags := labelsToSet(ctx, record.GetLabels())
	if diags.HasError() {
		return types.SetNull(types.StringType), fmt.Errorf("could not convert labels")
	}
	return result, nil
}

// setEntityLabels replaces an entity's labels, discarding the returned set when
// the caller only needs the error.
func setEntityLabels(ctx context.Context, client *Client, entityType, entityID string, labels types.Set) error {
	_, err := applyEntityLabels(ctx, client, entityType, entityID, labels)
	return err
}
