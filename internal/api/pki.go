package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/voidstarit/rookery/internal/pki"
	"github.com/voidstarit/rookery/internal/store"
)

// ConfigurePKI wires the mTLS certificate manager into the local API. The
// management endpoints are only exposed on the main listener (NativeHandler);
// the public mTLS listener serves the gateway API only.
func (s *Server) ConfigurePKI(manager *pki.Manager) {
	s.pki = manager
	s.handler = s.NativeHandler()
}

func (s *Server) pkiUnavailable(w http.ResponseWriter) bool {
	if s.pki == nil {
		writeError(w, http.StatusServiceUnavailable, "mTLS is not enabled on this server")
		return true
	}
	return false
}

func (s *Server) handlePKICA(w http.ResponseWriter, _ *http.Request) {
	if s.pkiUnavailable(w) {
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="rookery-ca.pem"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(s.pki.CAPEM())
}

type pkiIssueRequest struct {
	Name string `json:"name"`
	Days int    `json:"days"`
}

func (s *Server) handlePKIIssueClient(w http.ResponseWriter, r *http.Request) {
	if s.pkiUnavailable(w) {
		return
	}
	var request pkiIssueRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	issued, err := s.pki.IssueClientCertificate(r.Context(), request.Name, request.Days)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		ID             int64     `json:"id"`
		Name           string    `json:"name"`
		Serial         string    `json:"serial"`
		NotBefore      time.Time `json:"not_before"`
		NotAfter       time.Time `json:"not_after"`
		CertificatePEM string    `json:"certificate_pem"`
		PrivateKeyPEM  string    `json:"private_key_pem"`
		CAPEM          string    `json:"ca_pem"`
	}{
		ID: issued.ID, Name: issued.Name, Serial: issued.Serial,
		NotBefore: issued.NotBefore, NotAfter: issued.NotAfter,
		CertificatePEM: string(issued.CertificatePEM),
		PrivateKeyPEM:  string(issued.PrivateKeyPEM),
		CAPEM:          string(issued.CAPEM),
	})
}

func (s *Server) handlePKIListClients(w http.ResponseWriter, r *http.Request) {
	if s.pkiUnavailable(w) {
		return
	}
	clients, err := s.pki.ListClientCertificates(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list client certificates")
		return
	}
	writeJSON(w, http.StatusOK, clients)
}

func (s *Server) handlePKIRevokeClient(w http.ResponseWriter, r *http.Request) {
	if s.pkiUnavailable(w) {
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid client certificate id")
		return
	}
	if err := s.pki.RevokeClientCertificate(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "client certificate not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not revoke client certificate")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
