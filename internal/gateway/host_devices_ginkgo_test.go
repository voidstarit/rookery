package gateway_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/store"
)

var _ = Describe("host block-device endpoints", func() {
	It("serves both discovery endpoints using the real host lister", func() {
		st, err := store.Open(filepath.Join(GinkgoT().TempDir(), "host-block-endpoints.db"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = st.Close() })

		gw, err := gateway.New(st, fakeReadiness{status: readyStatus()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(gw.Close)
		server := httptest.NewServer(gw.Handler)
		DeferCleanup(server.Close)

		for _, path := range []string{"/api/host/block-devices", "/api/host/block-devices/attachable"} {
			response, err := http.Get(server.URL + path)
			Expect(err).NotTo(HaveOccurred())
			Expect(response.StatusCode).To(Equal(http.StatusOK), path)
			_ = response.Body.Close()
		}
	})
})
