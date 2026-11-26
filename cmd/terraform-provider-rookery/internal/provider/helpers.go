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

// int64Value returns the value held by v, or zero when v is null or unknown.
func int64Value(v types.Int64) int64 {
	return v.ValueInt64()
}

// int32Value narrows v to int32, returning zero for null or unknown values.
func int32Value(v types.Int64) int32 {
	return int32(v.ValueInt64())
}

// stringValue returns the string held by v, or the empty string when v is null
// or unknown.
func stringValue(v types.String) string {
	return v.ValueString()
}

// int64Pointer returns a pointer to the value held by v, or nil when v is null
// or unknown so absent optional fields stay unset.
func int64Pointer(v types.Int64) *int64 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	value := v.ValueInt64()
	return &value
}

// int32Pointer returns a pointer to v narrowed to int32, or nil when v is null
// or unknown.
func int32Pointer(v types.Int64) *int32 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	value := int32(v.ValueInt64())
	return &value
}

// optionalInt64FromUint64 converts v to a Terraform int64, mapping zero to null
// so zero-valued API fields with unset semantics stay absent from state.
func optionalInt64FromUint64(v uint64) types.Int64 {
	if v == 0 {
		return types.Int64Null()
	}
	return types.Int64Value(int64(v))
}

// stringPointer returns a pointer to the string held by v, or nil when v is
// null or unknown.
func stringPointer(v types.String) *string {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	value := v.ValueString()
	return &value
}

// boolPointer returns a pointer to the boolean held by v, or nil when v is null
// or unknown.
func boolPointer(v types.Bool) *bool {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	value := v.ValueBool()
	return &value
}

// timestampString formats ts as a UTC RFC 3339 string, returning a null string
// for a nil or invalid timestamp.
func timestampString(ts *timestamppb.Timestamp) types.String {
	if ts == nil || !ts.IsValid() {
		return types.StringNull()
	}
	return types.StringValue(ts.AsTime().UTC().Format(time.RFC3339Nano))
}

// int64List converts values into a Terraform list of numbers, surfacing
// conversion problems as diagnostics.
func int64List(ctx context.Context, values []int64) (types.List, diag.Diagnostics) {
	return types.ListValueFrom(ctx, types.Int64Type, values)
}

// stringList converts values into a Terraform list of strings.
func stringList(ctx context.Context, values []string) (types.List, diag.Diagnostics) {
	return types.ListValueFrom(ctx, types.StringType, values)
}

// stringSet converts values into a Terraform set of strings, dropping
// duplicates.
func stringSet(ctx context.Context, values []string) (types.Set, diag.Diagnostics) {
	return types.SetValueFrom(ctx, types.StringType, values)
}

// stringSetValues extracts the strings stored in value, returning nil without
// diagnostics when the set is null or unknown.
func stringSetValues(ctx context.Context, value types.Set) ([]string, diag.Diagnostics) {
	if value.IsNull() || value.IsUnknown() {
		return nil, nil
	}
	var values []string
	diags := value.ElementsAs(ctx, &values, false)
	return values, diags
}

// parseID parses value as a positive decimal id after trimming spaces, returning
// an error that names what was being parsed.
func parseID(value string, what string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid %s id %q", what, value)
	}
	return id, nil
}

// optionalStringValue converts value to a Terraform string, mapping the empty
// string to null so unset attributes stay absent.
func optionalStringValue(value string) types.String {
	if value == "" {
		return types.StringNull()
	}
	return types.StringValue(value)
}

// optionalUint32Value converts a non-nil uint32 pointer to a Terraform int64,
// returning null for nil.
func optionalUint32Value(value *uint32) types.Int64 {
	if value == nil {
		return types.Int64Null()
	}
	return types.Int64Value(int64(*value))
}

// uint32Pointer narrows v to a uint32 pointer, returning nil when v is null or
// unknown.
func uint32Pointer(v types.Int64) *uint32 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	value := uint32(v.ValueInt64())
	return &value
}

// optionalInt64Value converts a non-nil int64 pointer to a Terraform int64,
// returning null for nil.
func optionalInt64Value(value *int64) types.Int64 {
	if value == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*value)
}

// optionalInt32Value converts a non-nil int32 pointer to a Terraform int64,
// returning null for nil.
func optionalInt32Value(value *int32) types.Int64 {
	if value == nil {
		return types.Int64Null()
	}
	return types.Int64Value(int64(*value))
}

// optionalBoolValue converts a non-nil bool pointer to a Terraform bool,
// returning null for nil.
func optionalBoolValue(value *bool) types.Bool {
	if value == nil {
		return types.BoolNull()
	}
	return types.BoolValue(*value)
}

// isNotFound reports whether err is a gRPC NotFound status, which callers treat
// as an entity that has disappeared.
func isNotFound(err error) bool {
	return status.Code(err) == codes.NotFound
}

// boolUseState returns a plan modifier that keeps the prior state value while
// the planned boolean is unknown.
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
