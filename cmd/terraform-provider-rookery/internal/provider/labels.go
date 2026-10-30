package provider

import (
	"context"
	"fmt"

	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

func labelsResourceAttribute() schema.SetAttribute {
	return schema.SetAttribute{
		Optional:    true,
		Computed:    true,
		ElementType: types.StringType,
		Description: "Free-form labels assigned to the entity. Any number of labels can be assigned.",
	}
}

func labelsDataSourceAttribute() dschema.SetAttribute {
	return dschema.SetAttribute{
		Computed:    true,
		ElementType: types.StringType,
		Description: "Free-form labels assigned to the entity.",
	}
}

func labelsFromSet(ctx context.Context, value types.Set) ([]string, diag.Diagnostics) {
	if value.IsNull() || value.IsUnknown() {
		return nil, nil
	}
	var labels []string
	diags := value.ElementsAs(ctx, &labels, false)
	return labels, diags
}

func labelsToSet(ctx context.Context, labels []string) (types.Set, diag.Diagnostics) {
	if labels == nil {
		labels = []string{}
	}
	return types.SetValueFrom(ctx, types.StringType, labels)
}

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

func setEntityLabels(ctx context.Context, client *Client, entityType, entityID string, labels types.Set) error {
	_, err := applyEntityLabels(ctx, client, entityType, entityID, labels)
	return err
}
