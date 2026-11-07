package store_test

import (
	"context"
	"testing"

	"github.com/voidstarit/rookery/internal/store"
)

func TestKoujaStorageBackendConfigRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	backend, err := st.CreateStorageBackend(ctx, store.StorageBackend{
		Name:   "kouja-nvme",
		Title:  "Kouja NVMe",
		Driver: store.DiskSourceKouja,
		Config: store.StorageBackendConfig{
			Kouja: &store.KoujaConfig{
				Endpoint:       "10.0.0.1:9443",
				Protocol:       "iscsi",
				StorageClass:   "nvme",
				Initiator:      "iqn.2026-11.io.example:rookery",
				HostNQN:        "nqn.2026-11.io.example:rookery",
				CAFile:         "/etc/rookery/kouja/ca.crt",
				ClientCertFile: "/etc/rookery/kouja/client.crt",
				ClientKeyFile:  "/etc/rookery/kouja/client.key",
				ServerName:     "kouja-1",
				Portal:         "10.0.0.1:3260",
			},
		},
	})
	if err != nil {
		t.Fatalf("create kouja backend: %v", err)
	}
	got, err := st.GetStorageBackend(ctx, backend.ID)
	if err != nil {
		t.Fatalf("get kouja backend: %v", err)
	}
	if got.Config.Kouja == nil || got.Config.Kouja.StorageClass != "nvme" ||
		got.Config.Kouja.HostNQN != "nqn.2026-11.io.example:rookery" {
		t.Fatalf("kouja config = %+v", got.Config)
	}
}
