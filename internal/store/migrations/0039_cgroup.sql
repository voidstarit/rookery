-- Optional per-VM cgroup v2 resource limits, encoded as JSON. An empty object
-- (the default) means the VM runs in the shared cgroup and inherits host-wide
-- limits from Settings.
ALTER TABLE vms ADD COLUMN cgroup TEXT NOT NULL DEFAULT '{}';

-- Singleton host-wide cgroup settings.
CREATE TABLE IF NOT EXISTS cgroup_settings (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  config TEXT NOT NULL DEFAULT '{}',
  updated_at INTEGER NOT NULL
);

INSERT OR IGNORE INTO cgroup_settings (id, config, updated_at)
VALUES (1, '{}', 0);
