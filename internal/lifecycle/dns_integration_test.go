package lifecycle

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

func TestDHCPDNSIntegrationValidation(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "dns-lifecycle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	network, err := st.CreateNetwork(ctx, store.Network{Name: "lab", BridgeDomainID: 1})
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateNetwork(ctx, store.Network{Name: "other", BridgeDomainID: 2})
	if err != nil {
		t.Fatal(err)
	}
	dnsVNF, err := st.CreateDNSVNF(ctx, store.NewDNSVNF{Name: "dns", NetworkID: network.ID})
	if err != nil {
		t.Fatal(err)
	}
	service := New(st, &fakeBackend{}, &sync.Mutex{})
	if err := service.validateDHCPDNSIntegration(ctx, network.ID, dnsVNF.ID); err != nil {
		t.Fatalf("same-network integration rejected: %v", err)
	}
	if err := service.validateDHCPDNSIntegration(ctx, other.ID, dnsVNF.ID); err == nil || !strings.Contains(err.Error(), "same network") {
		t.Fatalf("cross-network integration error = %v", err)
	}

	dhcp, err := st.CreateDHCPVNF(ctx, store.NewDHCPVNF{
		Name: "dhcp", NetworkID: network.ID, Family: store.VNFTypeDHCPv4,
		Config: store.DHCPVNFConfig{
			Gateway: "10.20.0.1/24", RangeStart: "10.20.0.100", RangeEnd: "10.20.0.200",
			DNSVNFID: dnsVNF.ID, DNSDomain: "lab.test",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteDNSVNF(ctx, &rookeryv1.DeleteDNSVNFRequest{Id: dnsVNF.ID}); err == nil || !strings.Contains(err.Error(), "used by DHCP") {
		t.Fatalf("deleting referenced DNS VNF error = %v", err)
	}
	_ = dhcp
}
