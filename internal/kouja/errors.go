package kouja

import (
	"errors"

	koujacli "github.com/voidstarit/kouja/gen/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// CleanupClass tells the deletion worker whether an error is terminal,
// retryable, or already represents the desired end state.
type CleanupClass int

const (
	// CleanupTerminal means the operation should enter delete_failed.
	CleanupTerminal CleanupClass = iota
	// CleanupRetry means the operation should be retried with backoff.
	CleanupRetry
	// CleanupSuccess means the desired end state already exists.
	CleanupSuccess
)

// Reason returns the kouja ErrorInfo reason when present.
func Reason(err error) string {
	if info, ok := koujacli.ErrorInfo(err); ok {
		return info.GetReason()
	}
	return ""
}

// IsNotFound reports whether an error means the resource is already gone.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	return Reason(err) == "NOT_FOUND" || status.Code(err) == codes.NotFound
}

// IsSessionBlocked reports whether kouja refused a delete because a session
// state is active or unknown. Rookery retries these with backoff.
func IsSessionBlocked(err error) bool {
	reason := Reason(err)
	return reason == "SESSION_ACTIVE" || reason == "SESSION_UNKNOWN"
}

// IsRetryable exposes the generated client's retry policy.
func IsRetryable(err error) bool {
	return err != nil && koujacli.IsRetryable(err)
}

// ClassifyCleanup maps an error to the deletion worker's policy.
func ClassifyCleanup(err error) CleanupClass {
	if err == nil {
		return CleanupSuccess
	}
	if IsNotFound(err) {
		return CleanupSuccess
	}
	if IsSessionBlocked(err) || Reason(err) == "OPERATION_IN_PROGRESS" {
		return CleanupRetry
	}
	if koujacli.IsRetryable(err) {
		return CleanupRetry
	}
	return CleanupTerminal
}

// ClassifyError returns Retry for the generated client's retryable errors and
// Terminal otherwise. It exists for callers that do not need cleanup-specific
// NOT_FOUND handling.
func ClassifyError(err error) CleanupClass {
	if err == nil {
		return CleanupSuccess
	}
	if koujacli.IsRetryable(err) {
		return CleanupRetry
	}
	return CleanupTerminal
}

// IsTerminal reports whether ClassifyCleanup returns CleanupTerminal.
func IsTerminal(err error) bool {
	return ClassifyCleanup(err) == CleanupTerminal
}

// IsUnsupported reports whether an error is an unsupported protocol.
func IsUnsupported(err error) bool {
	return errors.Is(err, ErrUnsupportedProtocol)
}
