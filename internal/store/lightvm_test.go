package store_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/voidstarit/rookery/internal/store"
)

func createKernel(t *testing.T, st *store.Store, name string) store.LightVMKernel {
	t.Helper()
	kernel, err := st.CreateLightVMKernel(context.Background(), store.LightVMKernel{
		Name:        name,
		Description: "test kernel",
		Path:        "/var/lib/rookery/lightvm/kernels/" + name,
		FileName:    name + ".img",
		SizeBytes:   4096,
		Managed:     true,
	})
	if err != nil {
		t.Fatalf("create kernel %s: %v", name, err)
	}
	return kernel
}

func TestLightVMKernelCRUD(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	created := createKernel(t, st, "vmlinuz-6.12")
	if created.ID < 1 || created.Name != "vmlinuz-6.12" || !created.Managed {
		t.Fatalf("created kernel = %+v", created)
	}

	got, err := st.GetLightVMKernel(ctx, created.ID)
	if err != nil {
		t.Fatalf("get kernel: %v", err)
	}
	if got.Path != created.Path || got.FileName != created.FileName || got.SizeBytes != created.SizeBytes {
		t.Fatalf("get kernel = %+v, want %+v", got, created)
	}

	kernels, err := st.ListLightVMKernels(ctx)
	if err != nil {
		t.Fatalf("list kernels: %v", err)
	}
	if len(kernels) != 1 || kernels[0].ID != created.ID {
		t.Fatalf("list kernels = %+v", kernels)
	}

	updated, err := st.UpdateLightVMKernel(ctx, store.LightVMKernel{
		ID:          created.ID,
		Name:        "vmlinuz-6.12.4",
		Description: "updated",
		Path:        "/srv/kernels/vmlinuz",
		FileName:    "vmlinuz",
		SizeBytes:   8192,
		Managed:     false,
	})
	if err != nil {
		t.Fatalf("update kernel: %v", err)
	}
	if updated.Name != "vmlinuz-6.12.4" || updated.Path != "/srv/kernels/vmlinuz" || updated.Managed {
		t.Fatalf("updated kernel = %+v", updated)
	}

	deleted, err := st.DeleteLightVMKernel(ctx, created.ID)
	if err != nil {
		t.Fatalf("delete kernel: %v", err)
	}
	if deleted.ID != created.ID || deleted.Name != updated.Name {
		t.Fatalf("deleted kernel = %+v", deleted)
	}
	if _, err := st.GetLightVMKernel(ctx, created.ID); !errors.Is(err, store.ErrLightVMKernelNotFound) {
		t.Fatalf("get deleted kernel error = %v, want ErrLightVMKernelNotFound", err)
	}
	if _, err := st.DeleteLightVMKernel(ctx, created.ID); !errors.Is(err, store.ErrLightVMKernelNotFound) {
		t.Fatalf("delete missing kernel error = %v, want ErrLightVMKernelNotFound", err)
	}
}

func TestLightVMKernelNamesAreUnique(t *testing.T) {
	st := newTestStore(t)
	createKernel(t, st, "vmlinuz-6.12")

	_, err := st.CreateLightVMKernel(context.Background(), store.LightVMKernel{
		Name:        "VMLINUZ-6.12",
		Description: "duplicate",
		Path:        "/srv/kernels/other",
		FileName:    "other",
		SizeBytes:   1024,
	})
	if !errors.Is(err, store.ErrLightVMKernelNameTaken) {
		t.Fatalf("create duplicate kernel error = %v, want ErrLightVMKernelNameTaken", err)
	}
}

func TestLightVMKernelLabelsAreRemovedWithKernel(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	kernel := createKernel(t, st, "vmlinuz-6.12")
	entityID := strconv.FormatInt(kernel.ID, 10)

	if _, err := st.SetEntityLabels(ctx, "lightvm_kernel", entityID, []string{"lab", "boot"}); err != nil {
		t.Fatalf("set kernel labels: %v", err)
	}
	if _, err := st.DeleteLightVMKernel(ctx, kernel.ID); err != nil {
		t.Fatalf("delete kernel: %v", err)
	}
	labels, err := st.GetEntityLabels(ctx, "lightvm_kernel", entityID)
	if err != nil {
		t.Fatalf("get kernel labels: %v", err)
	}
	if len(labels.Labels) != 0 {
		t.Fatalf("labels after delete = %v, want none", labels.Labels)
	}
}

func TestLightVMKernelValidationErrors(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.GetLightVMKernel(context.Background(), 999); !errors.Is(err, store.ErrLightVMKernelNotFound) {
		t.Fatalf("get missing kernel error = %v", err)
	}
	if _, err := st.UpdateLightVMKernel(context.Background(), store.LightVMKernel{ID: 999, Name: "x"}); !errors.Is(err, store.ErrLightVMKernelNotFound) {
		t.Fatalf("update missing kernel error = %v", err)
	}
}
