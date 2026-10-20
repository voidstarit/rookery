package host

import (
	"context"
	"testing"
)

func TestStartTraceValidation(t *testing.T) {
	v := &VPPNetwork{}
	if _, err := v.StartTrace(context.Background(), "ip4-input;rm", 10); err == nil {
		t.Fatal("expected invalid node error")
	}
	if _, err := v.StartTrace(context.Background(), "ip4-input", 0); err == nil {
		t.Fatal("expected invalid count error")
	}
	if _, err := v.StartTrace(context.Background(), "ip4-input", 1001); err == nil {
		t.Fatal("expected oversized count error")
	}
}
