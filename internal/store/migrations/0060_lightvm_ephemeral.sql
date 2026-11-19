-- Ephemeral LightVMs are one-shot workloads: when the guest kernel exits
-- (reboot, panic, or poweroff) Rookery terminates QEMU, virtiofsd, and the
-- attempt container. Disk and network attachments remain on the definition.
ALTER TABLE lightvms ADD COLUMN ephemeral INTEGER NOT NULL DEFAULT 0;
