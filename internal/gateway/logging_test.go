package gateway

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestErrorLogInterceptorLogsRPCCause(t *testing.T) {
	var output bytes.Buffer
	logger := zerolog.New(&output).Level(zerolog.ErrorLevel)
	interceptor := errorLogInterceptor(logger)

	_, err := interceptor(
		context.Background(),
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/rookery.v1.VMService/StartVM"},
		func(context.Context, any) (any, error) {
			return nil, status.Error(codes.Internal, "could not start VM: qemu: /dev/kvm is unavailable")
		},
	)
	if status.Code(err) != codes.Internal {
		t.Fatalf("status = %v, want Internal", status.Code(err))
	}
	logs := output.String()
	for _, want := range []string{
		"/rookery.v1.VMService/StartVM",
		"Internal",
		"/dev/kvm is unavailable",
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs do not contain %q:\n%s", want, logs)
		}
	}
}
