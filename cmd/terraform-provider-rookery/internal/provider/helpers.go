package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func int64Value(v types.Int64) int64 {
	return v.ValueInt64()
}

func int32Value(v types.Int64) int32 {
	return int32(v.ValueInt64())
}

func stringValue(v types.String) string {
	return v.ValueString()
}

func int64Pointer(v types.Int64) *int64 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	value := v.ValueInt64()
	return &value
}

func int32Pointer(v types.Int64) *int32 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	value := int32(v.ValueInt64())
	return &value
}

func stringPointer(v types.String) *string {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	value := v.ValueString()
	return &value
}

func boolPointer(v types.Bool) *bool {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	value := v.ValueBool()
	return &value
}

func timestampString(ts *timestamppb.Timestamp) types.String {
	if ts == nil || !ts.IsValid() {
		return types.StringNull()
	}
	return types.StringValue(ts.AsTime().UTC().Format(time.RFC3339Nano))
}

func int64List(ctx context.Context, values []int64) (types.List, diag.Diagnostics) {
	return types.ListValueFrom(ctx, types.Int64Type, values)
}

func stringList(ctx context.Context, values []string) (types.List, diag.Diagnostics) {
	return types.ListValueFrom(ctx, types.StringType, values)
}

func stringSet(ctx context.Context, values []string) (types.Set, diag.Diagnostics) {
	return types.SetValueFrom(ctx, types.StringType, values)
}

func stringSetValues(ctx context.Context, value types.Set) ([]string, diag.Diagnostics) {
	if value.IsNull() || value.IsUnknown() {
		return nil, nil
	}
	var values []string
	diags := value.ElementsAs(ctx, &values, false)
	return values, diags
}

func parseID(value string, what string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid %s id %q", what, value)
	}
	return id, nil
}

func optionalStringValue(value string) types.String {
	if value == "" {
		return types.StringNull()
	}
	return types.StringValue(value)
}

func optionalUint32Value(value *uint32) types.Int64 {
	if value == nil {
		return types.Int64Null()
	}
	return types.Int64Value(int64(*value))
}

func uint32Pointer(v types.Int64) *uint32 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	value := uint32(v.ValueInt64())
	return &value
}

func optionalInt64Value(value *int64) types.Int64 {
	if value == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*value)
}

func optionalInt32Value(value *int32) types.Int64 {
	if value == nil {
		return types.Int64Null()
	}
	return types.Int64Value(int64(*value))
}

func optionalBoolValue(value *bool) types.Bool {
	if value == nil {
		return types.BoolNull()
	}
	return types.BoolValue(*value)
}

func isNotFound(err error) bool {
	return status.Code(err) == codes.NotFound
}

func boolUseState() planmodifier.Bool {
	return boolplanmodifier.UseStateForUnknown()
}

// importInt64ID sets the state id attribute from a numeric import id. The
// framework's passthrough helper always writes a string, which is incompatible
// with the int64 id attributes used by most Rookery resources.
func importInt64ID(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, err := parseID(req.ID, "resource")
	if err != nil {
		resp.Diagnostics.AddError("Import resource", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), types.Int64Value(id))...)
}
