package api

import (
	"net/http"

	"github.com/voidstarit/rookery/internal/host"
)

func (s *Server) handleListHostInterfaces(w http.ResponseWriter, r *http.Request) {
	interfaces, err := host.ListHostInterfaces()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, interfaces)
}
