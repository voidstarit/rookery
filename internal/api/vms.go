package api

import (
	"errors"
	"net/http"
	"regexp"

	"github.com/google/uuid"

	"github.com/voidstarit/rookery/internal/readiness"
)

var vmNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func parseVMID(r *http.Request) (string, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return "", errors.New("invalid vm id")
	}
	return id.String(), nil
}

func componentReason(status readiness.ComponentStatus) string {
	if status.Detail != "" {
		return status.Detail
	}
	if status.State != "" {
		return status.State
	}
	return "not ready"
}
