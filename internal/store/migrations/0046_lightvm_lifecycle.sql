-- LightVM lifecycle state, mirroring VM status/desired_state so a started
-- definition can be reconciled after an API or host restart.
ALTER TABLE lightvms ADD COLUMN status TEXT NOT NULL DEFAULT 'stopped';
ALTER TABLE lightvms ADD COLUMN desired_state TEXT NOT NULL DEFAULT 'stopped';
