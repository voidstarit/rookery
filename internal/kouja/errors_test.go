package kouja

import (
	"errors"
	"testing"

	errdetails "google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func errorWithReason(t *testing.T, code codes.Code, reason string) error {
	t.Helper()
	base := status.New(code, reason)
	withDetails, err := base.WithDetails(&errdetails.ErrorInfo{Domain: "kouja.v1", Reason: reason})
	if err != nil {
		t.Fatal(err)
	}
	return withDetails.Err()
}

func TestClassifyCleanup(t *testing.T) {
	if ClassifyCleanup(nil) != CleanupSuccess {
		t.Fatal("nil error is not success")
	}
	if ClassifyCleanup(status.Error(codes.NotFound, "missing")) != CleanupSuccess {
		t.Fatal("NotFound is not success")
	}
	if ClassifyCleanup(errorWithReason(t, codes.NotFound, "NOT_FOUND")) != CleanupSuccess {
		t.Fatal("NOT_FOUND is not success")
	}
	for _, reason := range []string{"SESSION_ACTIVE", "SESSION_UNKNOWN", "OPERATION_IN_PROGRESS"} {
		if ClassifyCleanup(errorWithReason(t, codes.FailedPrecondition, reason)) != CleanupRetry {
			t.Fatalf("%s is not retry", reason)
		}
	}
	if ClassifyCleanup(status.Error(codes.Unavailable, "down")) != CleanupRetry {
		t.Fatal("Unavailable is not retry")
	}
	if ClassifyCleanup(status.Error(codes.InvalidArgument, "bad")) != CleanupTerminal {
		t.Fatal("InvalidArgument is not terminal")
	}
	if ClassifyError(nil) != CleanupSuccess || ClassifyError(status.Error(codes.Unavailable, "down")) != CleanupRetry {
		t.Fatal("ClassifyError mismatch")
	}
}

func TestErrorHelpers(t *testing.T) {
	notFound := errorWithReason(t, codes.NotFound, "NOT_FOUND")
	if !IsNotFound(notFound) || !IsNotFound(status.Error(codes.NotFound, "x")) {
		t.Fatal("IsNotFound() = false")
	}
	if IsNotFound(errors.New("other")) {
		t.Fatal("IsNotFound() accepted unrelated error")
	}
	if !IsSessionBlocked(errorWithReason(t, codes.FailedPrecondition, "SESSION_ACTIVE")) ||
		!IsSessionBlocked(errorWithReason(t, codes.FailedPrecondition, "SESSION_UNKNOWN")) {
		t.Fatal("IsSessionBlocked() = false")
	}
	if Reason(notFound) != "NOT_FOUND" {
		t.Fatalf("Reason() = %q", Reason(notFound))
	}
	if Reason(errors.New("other")) != "" {
		t.Fatal("Reason() returned a reason for an unrelated error")
	}
	if !IsRetryable(status.Error(codes.Unavailable, "down")) || IsRetryable(nil) {
		t.Fatal("IsRetryable mismatch")
	}
	if !IsTerminal(status.Error(codes.InvalidArgument, "bad")) || IsTerminal(nil) {
		t.Fatal("IsTerminal mismatch")
	}
	if !IsUnsupported(ErrUnsupportedProtocol) || IsUnsupported(errors.New("other")) {
		t.Fatal("IsUnsupported mismatch")
	}
}
