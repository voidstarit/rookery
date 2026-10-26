CREATE TABLE startup_script (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  enabled INTEGER NOT NULL DEFAULT 0,
  script TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'disabled',
  exit_code INTEGER NOT NULL DEFAULT -1,
  output TEXT NOT NULL DEFAULT '',
  ran_at INTEGER,
  updated_at INTEGER NOT NULL,
  vpp_enabled INTEGER NOT NULL DEFAULT 1,
  vpp_script TEXT NOT NULL DEFAULT '',
  vpp_status TEXT NOT NULL DEFAULT 'pending',
  vpp_exit_code INTEGER NOT NULL DEFAULT -1,
  vpp_output TEXT NOT NULL DEFAULT '',
  vpp_ran_at INTEGER,
  vpp_updated_at INTEGER NOT NULL DEFAULT 0
);

INSERT INTO startup_script (
  id, enabled, script, status, exit_code, output, ran_at, updated_at,
  vpp_enabled, vpp_script, vpp_status, vpp_exit_code, vpp_output,
  vpp_ran_at, vpp_updated_at
)
VALUES (
  1,
  0,
  '',
  'disabled',
  -1,
  '',
  NULL,
  CAST(strftime('%s', 'now') AS INTEGER) * 1000,
  1,
  '# Assign NAT44 workers
vppctl set nat workers 0
',
  'pending',
  -1,
  '',
  NULL,
  CAST(strftime('%s', 'now') AS INTEGER) * 1000
);
