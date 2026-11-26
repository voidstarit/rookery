package provider

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

// diskSerialsFromModel decodes a disk id to serial Terraform map, reporting a
// diagnostic for each key that is not a positive disk id.
func diskSerialsFromModel(ctx context.Context, value types.Map) (map[int64]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	if value.IsNull() || value.IsUnknown() {
		return nil, diags
	}
	var values map[string]string
	diags.Append(value.ElementsAs(ctx, &values, false)...)
	if diags.HasError() {
		return nil, diags
	}
	result := make(map[int64]string, len(values))
	for key, serial := range values {
		diskID, err := strconv.ParseInt(key, 10, 64)
		if err != nil || diskID < 1 {
			diags.AddError("Invalid disk_serials key", fmt.Sprintf("disk_serials key %q must be a positive disk id", key))
			continue
		}
		result[diskID] = serial
	}
	return result, diags
}

// diskSerialsValue builds a Terraform map of disk id to serial for the disks that
// have a serial assigned.
func diskSerialsValue(ctx context.Context, disks []*rookeryv1.VMDisk) (types.Map, diag.Diagnostics) {
	serials := make(map[string]string)
	for _, disk := range disks {
		if disk.GetSerial() != "" {
			serials[strconv.FormatInt(disk.GetId(), 10)] = disk.GetSerial()
		}
	}
	return types.MapValueFrom(ctx, types.StringType, serials)
}
