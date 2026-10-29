-- desired_state records the power state a VM should have after the API or the
-- host restarts. Starting a VM sets it to running; stopping one sets it to
-- stopped. VMs that were running before this migration keep their running state
-- so they come back after a reboot.
ALTER TABLE vms ADD COLUMN desired_state TEXT NOT NULL DEFAULT 'stopped';

UPDATE vms SET desired_state = 'running' WHERE status = 'running';
