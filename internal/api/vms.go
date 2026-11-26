package api

import (
	"errors"
	"net/http"
	"regexp"

	"github.com/google/uuid"

	"github.com/voidstarit/rookery/internal/readiness"
)

var vmNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// parseVMID parses the request's id path value as a UUID and returns its
// canonical string form, rejecting malformed values with "invalid vm id".
func parseVMID(r *http.Request) (string, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return "", errors.New("invalid vm id")
	}
	return id.String(), nil
}

// componentReason returns a readiness component's detail, then its state, and
// finally "not ready" when neither is set.
func componentReason(status readiness.ComponentStatus) string {
	if status.Detail != "" {
		return status.Detail
	}
	if status.State != "" {
		return status.State
	}
	return "not ready"
}
