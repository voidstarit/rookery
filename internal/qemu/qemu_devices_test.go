package qemu

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/voidstarit/rookery/internal/store"
)

var _ = Describe("disk device validation", func() {
	It("accepts an existing device without activating it", func() {
		path := filepath.Join(GinkgoT().TempDir(), "disk")
		Expect(os.WriteFile(path, []byte("x"), 0o600)).To(Succeed())

		called := false
		err := ensureDiskDeviceWith(context.Background(), store.VMDisk{Device: path},
			func(context.Context, string, string, string) error {
				called = true
				return nil
			}, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(called).To(BeFalse())
	})

	It("rejects a missing host device without running LVM", func() {
		called := false
		err := ensureDiskDeviceWith(context.Background(), store.VMDisk{
			Device: filepath.Join(GinkgoT().TempDir(), "missing"),
			Source: store.DiskSourceHostDevice,
			VG:     "vg0", Name: "disk",
		}, func(context.Context, string, string, string) error {
			called = true
			return nil
		}, 0)
		Expect(err).To(HaveOccurred())
		Expect(called).To(BeFalse())
	})

	It("reports an activation failure", func() {
		err := ensureDiskDeviceWith(context.Background(), store.VMDisk{
			Device: filepath.Join(GinkgoT().TempDir(), "missing"),
			VG:     "vg0", Name: "disk",
		}, func(context.Context, string, string, string) error {
			return errors.New("lvchange failed")
		}, 0)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("lvchange failed"))
	})

	It("accepts a device that appears after activation", func() {
		path := filepath.Join(GinkgoT().TempDir(), "activated")
		err := ensureDiskDeviceWith(context.Background(), store.VMDisk{
			Device: path, VG: "vg0", ThinPool: "pool", Name: "disk",
		}, func(_ context.Context, vg, pool, name string) error {
			Expect(vg).To(Equal("vg0"))
			Expect(pool).To(Equal("pool"))
			Expect(name).To(Equal("disk"))
			return os.WriteFile(path, []byte("x"), 0o600)
		}, time.Second)
		Expect(err).NotTo(HaveOccurred())
	})

	It("reports a device that never appears", func() {
		err := ensureDiskDeviceWith(context.Background(), store.VMDisk{
			Device: filepath.Join(GinkgoT().TempDir(), "missing"),
			VG:     "vg0", Name: "disk",
		}, func(context.Context, string, string, string) error { return nil }, 0)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not available"))
	})
})

var _ = Describe("validateVMDisks", func() {
	It("accepts an empty disk set", func() {
		Expect(validateVMDisks(context.Background(), nil)).To(Succeed())
	})

	It("rejects a host device that is not a block device", func() {
		hostPath := filepath.Join(GinkgoT().TempDir(), "regular")
		Expect(os.WriteFile(hostPath, []byte("x"), 0o600)).To(Succeed())
		err := validateVMDisks(context.Background(), []store.VMDisk{{
			Device: hostPath, Source: store.DiskSourceHostDevice,
		}})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not a block device"))
	})

	It("rejects an unavailable LVM disk", func() {
		err := validateVMDisks(context.Background(), []store.VMDisk{{
			Device: filepath.Join(GinkgoT().TempDir(), "missing"), VG: "vg0", Name: "disk",
		}})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not available"))
	})
})
