-- Per-attachment serial exposed by the guest's virtio-blk device. Guests can
-- use it to identify a disk's role independent of device ordering.
ALTER TABLE workload_disks ADD COLUMN serial TEXT NOT NULL DEFAULT '';
