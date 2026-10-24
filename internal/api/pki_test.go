package api_test

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/voidstarit/rookery/internal/pki"
)

func TestPKIClientCertificateEndpoints(t *testing.T) {
	env := newTestAPI(t)
	manager, err := pki.New(t.TempDir(), env.store, nil, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	env.server.ConfigurePKI(manager)
	handler := env.server.NativeHandler()

	ca := do(t, handler, http.MethodGet, "/api/pki/ca", nil)
	if ca.Code != http.StatusOK || !strings.Contains(ca.Body.String(), "BEGIN CERTIFICATE") {
		t.Fatalf("CA response = %d %q", ca.Code, ca.Body.String())
	}

	issued := do(t, handler, http.MethodPost, "/api/pki/clients", map[string]any{"name": "ops", "days": 30})
	if issued.Code != http.StatusCreated {
		t.Fatalf("issue status = %d, body = %s", issued.Code, issued.Body.String())
	}
	var created struct {
		ID             int64  `json:"id"`
		Name           string `json:"name"`
		CertificatePEM string `json:"certificate_pem"`
		PrivateKeyPEM  string `json:"private_key_pem"`
	}
	created = decode[struct {
		ID             int64  `json:"id"`
		Name           string `json:"name"`
		CertificatePEM string `json:"certificate_pem"`
		PrivateKeyPEM  string `json:"private_key_pem"`
	}](t, issued)
	if created.ID == 0 || created.Name != "ops" ||
		!strings.Contains(created.CertificatePEM, "BEGIN CERTIFICATE") ||
		!strings.Contains(created.PrivateKeyPEM, "BEGIN EC PRIVATE KEY") {
		t.Fatalf("issued certificate = %+v", created)
	}

	list := do(t, handler, http.MethodGet, "/api/pki/clients", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d", list.Code)
	}
	clients := decode[[]struct {
		ID      int64 `json:"id"`
		Revoked bool  `json:"revoked"`
	}](t, list)
	if len(clients) != 1 || clients[0].ID != created.ID || clients[0].Revoked {
		t.Fatalf("clients = %+v", clients)
	}

	revoked := do(t, handler, http.MethodDelete, "/api/pki/clients/"+strconv.FormatInt(created.ID, 10), nil)
	if revoked.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, body = %s", revoked.Code, revoked.Body.String())
	}
	list = do(t, handler, http.MethodGet, "/api/pki/clients", nil)
	clients = decode[[]struct {
		ID      int64 `json:"id"`
		Revoked bool  `json:"revoked"`
	}](t, list)
	if len(clients) != 1 || !clients[0].Revoked {
		t.Fatalf("clients after revoke = %+v", clients)
	}

	if _, err := manager.ListClientCertificates(context.Background()); err != nil {
		t.Fatal(err)
	}
}
