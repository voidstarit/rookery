ALTER TABLE disks ADD COLUMN backend_metadata TEXT NOT NULL DEFAULT '{}';
ALTER TABLE disks ADD COLUMN attachment_protocol TEXT NOT NULL DEFAULT '';
ALTER TABLE disks ADD COLUMN deletion_state TEXT NOT NULL DEFAULT '';
ALTER TABLE disks ADD COLUMN deletion_error TEXT NOT NULL DEFAULT '';
ALTER TABLE disks ADD COLUMN deletion_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE disks ADD COLUMN deletion_next_attempt_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE disks ADD COLUMN deletion_claimed_at INTEGER NOT NULL DEFAULT 0;

CREATE INDEX idx_disks_deletion_due
    ON disks(deletion_state, deletion_next_attempt_at);
