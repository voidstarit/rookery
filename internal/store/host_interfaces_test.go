package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestHostInterfaceRecords(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "host-interfaces.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	record, err := st.UpsertHostInterfaceRecord(ctx, HostInterfaceRecord{
		NIC: "enp1s0", VPPName: "host-enp1s0", Purpose: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.NIC != "enp1s0" || record.Purpose != "manual" || record.CreatedAt.IsZero() {
		t.Fatalf("record = %+v", record)
	}
	if err := st.SetHostInterfaceRuntime(ctx, "enp1s0", 42); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.GetHostInterfaceRecord(ctx, "enp1s0")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SwIfIndex != 42 {
		t.Fatalf("sw_if_index = %d, want 42", loaded.SwIfIndex)
	}
	records, err := st.ListHostInterfaceRecords(ctx)
	if err != nil || len(records) != 1 {
		t.Fatalf("records = %+v, err = %v", records, err)
	}
	if err := st.DeleteHostInterfaceRecord(ctx, "enp1s0"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetHostInterfaceRecord(ctx, "enp1s0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete = %v, want ErrNotFound", err)
	}
}
