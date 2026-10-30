-- VMs are assigned to a named cgroup class instead of carrying per-VM limits.
-- The legacy vms.cgroup column is kept for compatibility but is no longer used.
ALTER TABLE vms ADD COLUMN cgroup_class TEXT NOT NULL DEFAULT '';
