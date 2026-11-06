package store_test

import (
	"context"
	"errors"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/voidstarit/rookery/internal/store"
)

func newGinkgoStore() *store.Store {
	st, err := store.Open(filepath.Join(GinkgoT().TempDir(), "test.db"))
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = st.Close() })
	return st
}

var _ = Describe("host device disks", func() {
	It("stores, attaches, and releases a passthrough disk", func() {
		ctx := context.Background()
		st := newGinkgoStore()

		disk, err := st.CreateDisk(ctx, store.Disk{
			Name:      "host-data",
			SizeGB:    21,
			SizeBytes: (20 << 30) + 1,
			Device:    "/dev/mapper/data",
			Source:    store.DiskSourceHostDevice,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(disk.Source).To(Equal(store.DiskSourceHostDevice))
		Expect(disk.Device).To(Equal("/dev/mapper/data"))
		Expect(disk.SizeBytes).To(Equal(uint64((20 << 30) + 1)))
		Expect(disk.SizeGB).To(Equal(21))

		_, err = st.SetDiskTemplate(ctx, disk.ID, true)
		Expect(err).To(HaveOccurred())

		vm, err := st.CreateVM(ctx, store.VM{
			Name: "host-vm", Status: store.StatusStopped, VCPUs: 1, MemoryMB: 512,
			DiskIDs: []int64{disk.ID},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(vm.Disks).To(HaveLen(1))
		Expect(vm.Disks[0].Source).To(Equal(store.DiskSourceHostDevice))
		Expect(vm.Disks[0].Device).To(Equal("/dev/mapper/data"))
		Expect(vm.Disks[0].SizeBytes).To(Equal(uint64((20 << 30) + 1)))

		Expect(st.DeleteDisk(ctx, disk.ID)).To(MatchError(store.ErrDiskInUse))
		Expect(st.DeleteVM(ctx, vm.ID)).To(Succeed())
		Expect(st.DeleteDisk(ctx, disk.ID)).To(Succeed())
	})

	It("rejects a duplicate host device", func() {
		ctx := context.Background()
		st := newGinkgoStore()

		_, err := st.CreateDisk(ctx, store.Disk{
			Name: "host-data", SizeGB: 1, Device: "/dev/mapper/data", Source: store.DiskSourceHostDevice,
		})
		Expect(err).NotTo(HaveOccurred())

		_, err = st.CreateDisk(ctx, store.Disk{
			Name: "host-data-alias", SizeGB: 1, Device: "/dev/mapper/data", Source: store.DiskSourceHostDevice,
		})
		Expect(errors.Is(err, store.ErrDiskDeviceTaken)).To(BeTrue())
	})

	It("validates host device definitions", func() {
		ctx := context.Background()
		st := newGinkgoStore()

		cases := []store.Disk{
			{Name: "missing", SizeGB: 1, Source: store.DiskSourceHostDevice},
			{Name: "template", SizeGB: 1, Device: "/dev/mapper/x", Source: store.DiskSourceHostDevice, IsTemplate: true},
			{Name: "provisioning", SizeGB: 1, Device: "/dev/mapper/x", Source: store.DiskSourceHostDevice, ProvisioningScript: "/bin/true"},
			{Name: "unknown", SizeGB: 1, Source: "unknown"},
		}
		for _, disk := range cases {
			_, err := st.CreateDisk(ctx, disk)
			Expect(err).To(HaveOccurred(), "disk %+v", disk)
		}
	})

	It("rejects provisioning settings on a host device", func() {
		ctx := context.Background()
		st := newGinkgoStore()

		disk, err := st.CreateDisk(ctx, store.Disk{
			Name: "host", SizeGB: 1, Device: "/dev/mapper/x", Source: store.DiskSourceHostDevice,
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = st.SetDiskProvisioning(ctx, disk.ID, "/bin/true", "5m")
		Expect(err).To(HaveOccurred())
	})
})
