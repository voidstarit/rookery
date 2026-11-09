-- Raw per-disk bandwidth limits are retired in favor of
-- disk_throttle_classes. This migration runs after the Go and UI cutover.
ALTER TABLE disks DROP COLUMN read_bps;
ALTER TABLE disks DROP COLUMN write_bps;
