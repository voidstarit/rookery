package store_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/voidstarit/rookery/internal/store"
)

func createInitramfs(t *testing.T, st *store.Store, name string) store.LightVMInitramfs {
	t.Helper()
	image, err := st.CreateLightVMInitramfs(context.Background(), store.LightVMInitramfs{
		Name:        name,
		Description: "test initramfs",
		Path:        "/var/lib/rookery/lightvm/initramfs/" + name,
		FileName:    name + ".img",
		SizeBytes:   8192,
		Managed:     true,
	})
	if err != nil {
		t.Fatalf("create initramfs %s: %v", name, err)
	}
	return image
}

func TestLightVMInitramfsCRUD(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	created := createInitramfs(t, st, "initramfs-6.12")
	if created.ID < 1 || created.Name != "initramfs-6.12" || !created.Managed {
		t.Fatalf("created initramfs = %+v", created)
	}

	got, err := st.GetLightVMInitramfs(ctx, created.ID)
	if err != nil {
		t.Fatalf("get initramfs: %v", err)
	}
	if got.Path != created.Path || got.FileName != created.FileName || got.SizeBytes != created.SizeBytes {
		t.Fatalf("get initramfs = %+v, want %+v", got, created)
	}

	images, err := st.ListLightVMInitramfs(ctx)
	if err != nil {
		t.Fatalf("list initramfs: %v", err)
	}
	if len(images) != 1 || images[0].ID != created.ID {
		t.Fatalf("list initramfs = %+v", images)
	}

	updated, err := st.UpdateLightVMInitramfs(ctx, store.LightVMInitramfs{
		ID:          created.ID,
		Name:        "initramfs-6.12.4",
		Description: "updated",
		Path:        "/srv/initramfs/initramfs.img",
		FileName:    "initramfs.img",
		SizeBytes:   16384,
		Managed:     false,
	})
	if err != nil {
		t.Fatalf("update initramfs: %v", err)
	}
	if updated.Name != "initramfs-6.12.4" || updated.Path != "/srv/initramfs/initramfs.img" || updated.Managed {
		t.Fatalf("updated initramfs = %+v", updated)
	}

	deleted, err := st.DeleteLightVMInitramfs(ctx, created.ID)
	if err != nil {
		t.Fatalf("delete initramfs: %v", err)
	}
	if deleted.ID != created.ID || deleted.Name != updated.Name {
		t.Fatalf("deleted initramfs = %+v", deleted)
	}
	if _, err := st.GetLightVMInitramfs(ctx, created.ID); !errors.Is(err, store.ErrLightVMInitramfsNotFound) {
		t.Fatalf("get deleted initramfs error = %v, want ErrLightVMInitramfsNotFound", err)
	}
	if _, err := st.DeleteLightVMInitramfs(ctx, created.ID); !errors.Is(err, store.ErrLightVMInitramfsNotFound) {
		t.Fatalf("delete missing initramfs error = %v, want ErrLightVMInitramfsNotFound", err)
	}
}

func TestLightVMInitramfsNamesAreUnique(t *testing.T) {
	st := newTestStore(t)
	createInitramfs(t, st, "initramfs-6.12")

	_, err := st.CreateLightVMInitramfs(context.Background(), store.LightVMInitramfs{
		Name:        "INITRAMFS-6.12",
		Description: "duplicate",
		Path:        "/srv/initramfs/other",
		FileName:    "other",
		SizeBytes:   1024,
	})
	if !errors.Is(err, store.ErrLightVMInitramfsNameTaken) {
		t.Fatalf("create duplicate initramfs error = %v, want ErrLightVMInitramfsNameTaken", err)
	}
}

func TestLightVMInitramfsLabelsAreRemovedWithRecord(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	image := createInitramfs(t, st, "initramfs-6.12")
	entityID := strconv.FormatInt(image.ID, 10)

	if _, err := st.SetEntityLabels(ctx, "lightvm_initramfs", entityID, []string{"lab", "boot"}); err != nil {
		t.Fatalf("set initramfs labels: %v", err)
	}
	if _, err := st.DeleteLightVMInitramfs(ctx, image.ID); err != nil {
		t.Fatalf("delete initramfs: %v", err)
	}
	labels, err := st.GetEntityLabels(ctx, "lightvm_initramfs", entityID)
	if err != nil {
		t.Fatalf("get initramfs labels: %v", err)
	}
	if len(labels.Labels) != 0 {
		t.Fatalf("labels after delete = %v, want none", labels.Labels)
	}
}
