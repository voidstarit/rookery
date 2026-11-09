package provider

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/diskthrottle"
)

var diskThrottleClassNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type diskThrottleClassModel struct {
	ID              types.Int64  `tfsdk:"id"`
	Name            types.String `tfsdk:"name"`
	Description     types.String `tfsdk:"description"`
	BPSTotal        types.Int64  `tfsdk:"bps_total"`
	BPSTotalMax     types.Int64  `tfsdk:"bps_total_max"`
	BPSTotalMaxLen  types.Int64  `tfsdk:"bps_total_max_length"`
	BPSRead         types.Int64  `tfsdk:"bps_read"`
	BPSReadMax      types.Int64  `tfsdk:"bps_read_max"`
	BPSReadMaxLen   types.Int64  `tfsdk:"bps_read_max_length"`
	BPSWrite        types.Int64  `tfsdk:"bps_write"`
	BPSWriteMax     types.Int64  `tfsdk:"bps_write_max"`
	BPSWriteMaxLen  types.Int64  `tfsdk:"bps_write_max_length"`
	IOPSTotal       types.Int64  `tfsdk:"iops_total"`
	IOPSTotalMax    types.Int64  `tfsdk:"iops_total_max"`
	IOPSTotalMaxLen types.Int64  `tfsdk:"iops_total_max_length"`
	IOPSRead        types.Int64  `tfsdk:"iops_read"`
	IOPSReadMax     types.Int64  `tfsdk:"iops_read_max"`
	IOPSReadMaxLen  types.Int64  `tfsdk:"iops_read_max_length"`
	IOPSWrite       types.Int64  `tfsdk:"iops_write"`
	IOPSWriteMax    types.Int64  `tfsdk:"iops_write_max"`
	IOPSWriteMaxLen types.Int64  `tfsdk:"iops_write_max_length"`
	IOPSSize        types.Int64  `tfsdk:"iops_size"`
	ValidationError types.String `tfsdk:"validation_error"`
	DiskCount       types.Int64  `tfsdk:"disk_count"`
}

func diskThrottleClassModelFromProto(class *rookeryv1.DiskThrottleClass) diskThrottleClassModel {
	limits := class.GetLimits()
	return diskThrottleClassModel{
		ID:              types.Int64Value(class.GetId()),
		Name:            types.StringValue(class.GetName()),
		Description:     types.StringValue(class.GetDescription()),
		BPSTotal:        types.Int64Value(limits.GetBpsTotal()),
		BPSTotalMax:     types.Int64Value(limits.GetBpsTotalMax()),
		BPSTotalMaxLen:  types.Int64Value(limits.GetBpsTotalMaxLength()),
		BPSRead:         types.Int64Value(limits.GetBpsRead()),
		BPSReadMax:      types.Int64Value(limits.GetBpsReadMax()),
		BPSReadMaxLen:   types.Int64Value(limits.GetBpsReadMaxLength()),
		BPSWrite:        types.Int64Value(limits.GetBpsWrite()),
		BPSWriteMax:     types.Int64Value(limits.GetBpsWriteMax()),
		BPSWriteMaxLen:  types.Int64Value(limits.GetBpsWriteMaxLength()),
		IOPSTotal:       types.Int64Value(limits.GetIopsTotal()),
		IOPSTotalMax:    types.Int64Value(limits.GetIopsTotalMax()),
		IOPSTotalMaxLen: types.Int64Value(limits.GetIopsTotalMaxLength()),
		IOPSRead:        types.Int64Value(limits.GetIopsRead()),
		IOPSReadMax:     types.Int64Value(limits.GetIopsReadMax()),
		IOPSReadMaxLen:  types.Int64Value(limits.GetIopsReadMaxLength()),
		IOPSWrite:       types.Int64Value(limits.GetIopsWrite()),
		IOPSWriteMax:    types.Int64Value(limits.GetIopsWriteMax()),
		IOPSWriteMaxLen: types.Int64Value(limits.GetIopsWriteMaxLength()),
		IOPSSize:        types.Int64Value(limits.GetIopsSize()),
		ValidationError: types.StringValue(class.GetValidationError()),
		DiskCount:       types.Int64Value(int64(class.GetDiskCount())),
	}
}

func diskThrottleLimitsFromModel(model diskThrottleClassModel) diskthrottle.Limits {
	value := func(item types.Int64) int64 {
		if item.IsNull() || item.IsUnknown() {
			return 0
		}
		return item.ValueInt64()
	}
	return diskthrottle.Limits{
		BPSTotal:           value(model.BPSTotal),
		BPSTotalMax:        value(model.BPSTotalMax),
		BPSTotalMaxLength:  value(model.BPSTotalMaxLen),
		BPSRead:            value(model.BPSRead),
		BPSReadMax:         value(model.BPSReadMax),
		BPSReadMaxLength:   value(model.BPSReadMaxLen),
		BPSWrite:           value(model.BPSWrite),
		BPSWriteMax:        value(model.BPSWriteMax),
		BPSWriteMaxLength:  value(model.BPSWriteMaxLen),
		IOPSTotal:          value(model.IOPSTotal),
		IOPSTotalMax:       value(model.IOPSTotalMax),
		IOPSTotalMaxLength: value(model.IOPSTotalMaxLen),
		IOPSRead:           value(model.IOPSRead),
		IOPSReadMax:        value(model.IOPSReadMax),
		IOPSReadMaxLength:  value(model.IOPSReadMaxLen),
		IOPSWrite:          value(model.IOPSWrite),
		IOPSWriteMax:       value(model.IOPSWriteMax),
		IOPSWriteMaxLength: value(model.IOPSWriteMaxLen),
		IOPSSize:           value(model.IOPSSize),
	}
}

func validateDiskThrottleClassModel(model diskThrottleClassModel) error {
	name := strings.TrimSpace(model.Name.ValueString())
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if len(name) > 127 || !diskThrottleClassNamePattern.MatchString(name) {
		return fmt.Errorf("name must match ^[A-Za-z0-9][A-Za-z0-9._-]*$ and be at most 127 characters")
	}
	if len(strings.TrimSpace(model.Description.ValueString())) > 2000 {
		return fmt.Errorf("description must be at most 2000 characters")
	}
	limits := diskThrottleLimitsFromModel(model)
	for field, value := range map[string]int64{
		"bps_total": limits.BPSTotal, "bps_total_max": limits.BPSTotalMax,
		"bps_total_max_length": limits.BPSTotalMaxLength,
		"bps_read":             limits.BPSRead, "bps_read_max": limits.BPSReadMax,
		"bps_read_max_length": limits.BPSReadMaxLength,
		"bps_write":           limits.BPSWrite, "bps_write_max": limits.BPSWriteMax,
		"bps_write_max_length": limits.BPSWriteMaxLength,
		"iops_total":           limits.IOPSTotal, "iops_total_max": limits.IOPSTotalMax,
		"iops_total_max_length": limits.IOPSTotalMaxLength,
		"iops_read":             limits.IOPSRead, "iops_read_max": limits.IOPSReadMax,
		"iops_read_max_length": limits.IOPSReadMaxLength,
		"iops_write":           limits.IOPSWrite, "iops_write_max": limits.IOPSWriteMax,
		"iops_write_max_length": limits.IOPSWriteMaxLength,
		"iops_size":             limits.IOPSSize,
	} {
		if value < 0 {
			return fmt.Errorf("%s must not be negative", field)
		}
	}
	return nil
}

func diskThrottleClassModelDiagnostics(model diskThrottleClassModel) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if err := validateDiskThrottleClassModel(model); err != nil {
		diagnostics.AddError("Invalid disk throttle class", err.Error())
	}
	return diagnostics
}

func diskThrottleClassToProto(model diskThrottleClassModel) *rookeryv1.DiskThrottleClass {
	limits := diskThrottleLimitsFromModel(model)
	return &rookeryv1.DiskThrottleClass{
		Id:          model.ID.ValueInt64(),
		Name:        strings.TrimSpace(model.Name.ValueString()),
		Description: strings.TrimSpace(model.Description.ValueString()),
		Limits: &rookeryv1.DiskThrottleLimits{
			BpsTotal:           limits.BPSTotal,
			BpsTotalMax:        limits.BPSTotalMax,
			BpsTotalMaxLength:  limits.BPSTotalMaxLength,
			BpsRead:            limits.BPSRead,
			BpsReadMax:         limits.BPSReadMax,
			BpsReadMaxLength:   limits.BPSReadMaxLength,
			BpsWrite:           limits.BPSWrite,
			BpsWriteMax:        limits.BPSWriteMax,
			BpsWriteMaxLength:  limits.BPSWriteMaxLength,
			IopsTotal:          limits.IOPSTotal,
			IopsTotalMax:       limits.IOPSTotalMax,
			IopsTotalMaxLength: limits.IOPSTotalMaxLength,
			IopsRead:           limits.IOPSRead,
			IopsReadMax:        limits.IOPSReadMax,
			IopsReadMaxLength:  limits.IOPSReadMaxLength,
			IopsWrite:          limits.IOPSWrite,
			IopsWriteMax:       limits.IOPSWriteMax,
			IopsWriteMaxLength: limits.IOPSWriteMaxLength,
			IopsSize:           limits.IOPSSize,
		},
		DiskCount:       int32(model.DiskCount.ValueInt64()),
		ValidationError: model.ValidationError.ValueString(),
	}
}
