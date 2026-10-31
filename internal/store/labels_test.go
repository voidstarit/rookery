package store

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/voidstarit/rookery/internal/labels"
)

func TestEntityLabelsRoundTrip(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "labels.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	if _, err := st.SetEntityLabels(ctx, labels.EntityVM, "vm-1", []string{"web", "prod", "web"}); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetEntityLabels(ctx, labels.EntityVM, "vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"prod", "web"}; !reflect.DeepEqual(got.Labels, want) {
		t.Fatalf("labels = %#v, want %#v", got.Labels, want)
	}
	if _, err := st.SetEntityLabels(ctx, labels.EntityVM, "vm-1", nil); err != nil {
		t.Fatal(err)
	}
	got, err = st.GetEntityLabels(ctx, labels.EntityVM, "vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Labels) != 0 {
		t.Fatalf("cleared labels = %#v", got.Labels)
	}
}

func TestListEntityLabels(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "labels-list.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	if _, err := st.SetEntityLabels(ctx, labels.EntityDisk, "1", []string{"os", "slow"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetEntityLabels(ctx, labels.EntityDisk, "2", []string{"fast"}); err != nil {
		t.Fatal(err)
	}
	records, err := st.ListEntityLabels(ctx, labels.EntityDisk)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].EntityID != "1" || len(records[0].Labels) != 2 || records[1].EntityID != "2" {
		t.Fatalf("records = %+v", records)
	}
	if _, err := st.SetEntityLabels(ctx, "unknown", "1", []string{"bad"}); err == nil {
		t.Fatal("unknown entity type was accepted")
	}
}

func TestEntityLabelsFollowDeletedEntity(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "labels-delete.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	disk, err := st.CreateDisk(ctx, Disk{Name: "labels-disk", SizeGB: 1, VG: "vg0", ThinPool: "pool"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetEntityLabels(ctx, labels.EntityDisk, fmt.Sprintf("%d", disk.ID), []string{"web"}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteDisk(ctx, disk.ID); err != nil {
		t.Fatal(err)
	}
	records, err := st.ListEntityLabels(ctx, labels.EntityDisk)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("labels for deleted disk = %+v", records)
	}
}
