package host

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var traceNodePattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)

// TraceResult is the raw output of a VPP packet trace operation.
type TraceResult struct {
	Node       string    `json:"node,omitempty"`
	Count      uint32    `json:"count,omitempty"`
	Raw        string    `json:"raw"`
	CapturedAt time.Time `json:"captured_at"`
}

// StartTrace clears the existing trace and starts tracing count packets at the
// selected VPP graph node.
func (v *VPPNetwork) StartTrace(ctx context.Context, node string, count uint32) (TraceResult, error) {
	node = strings.TrimSpace(node)
	if node == "" || len(node) > 128 || !traceNodePattern.MatchString(node) {
		return TraceResult{}, errors.New("invalid VPP graph node")
	}
	if count < 1 || count > 1000 {
		return TraceResult{}, errors.New("trace count must be between 1 and 1000")
	}
	if out, err := v.runCLI(ctx, "clear", "trace"); err != nil {
		return TraceResult{}, fmt.Errorf("clear VPP trace: %w: %s", err, out)
	}
	out, err := v.runCLI(ctx, "trace", "add", node, strconv.FormatUint(uint64(count), 10))
	if err != nil {
		return TraceResult{}, fmt.Errorf("start VPP trace: %w: %s", err, out)
	}
	return TraceResult{Node: node, Count: count, Raw: out, CapturedAt: time.Now().UTC()}, nil
}

// GetTrace reads the current VPP trace buffer.
func (v *VPPNetwork) GetTrace(ctx context.Context) (TraceResult, error) {
	out, err := v.runCLIRaw(ctx, "show", "trace")
	if err != nil {
		return TraceResult{}, fmt.Errorf("show VPP trace: %w: %s", err, out)
	}
	return TraceResult{Raw: out, CapturedAt: time.Now().UTC()}, nil
}

// ClearTrace clears the VPP trace buffer.
func (v *VPPNetwork) ClearTrace(ctx context.Context) error {
	out, err := v.runCLI(ctx, "clear", "trace")
	if err != nil {
		return fmt.Errorf("clear VPP trace: %w: %s", err, out)
	}
	return nil
}
