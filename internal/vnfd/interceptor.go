package vnfd

import (
	"context"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// TraceInterceptor logs every VNF daemon RPC at trace level, including its
// duration, gRPC status code, and error. Enable it with -log-level trace.
func TraceInterceptor(logger zerolog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		response, err := handler(ctx, request)
		event := logger.Trace().
			Str("method", info.FullMethod).
			Dur("duration", time.Since(start))
		if err != nil {
			event = event.Str("code", status.Code(err).String()).Err(err)
		}
		event.Msg("VNF daemon RPC completed")
		return response, err
	}
}
