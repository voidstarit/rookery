-- Kernel command line appended when booting a LightVM. New and existing
-- definitions default to a console on the virtio-console and /init as PID 1.
ALTER TABLE lightvms ADD COLUMN append TEXT NOT NULL DEFAULT 'console=hvc0 rdinit=/init';
