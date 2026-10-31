-- Legacy per-VM cgroup v2 resource limits, encoded as JSON. Superseded by
-- vms.cgroup_class in 0040; the column is kept for migration compatibility.
ALTER TABLE vms ADD COLUMN cgroup TEXT NOT NULL DEFAULT '{}';

-- Singleton host-wide cgroup settings.
CREATE TABLE IF NOT EXISTS cgroup_settings (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  config TEXT NOT NULL DEFAULT '{}',
  updated_at INTEGER NOT NULL
);

INSERT OR IGNORE INTO cgroup_settings (id, config, updated_at)
VALUES (1, '{}', 0);
