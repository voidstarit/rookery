-- LightVM templates and managed groups. A template version is immutable; a
-- group pins one version for new members. Members have their own lifecycle and
-- own their LightVM, data disks, and vhost-user interfaces through nullable
-- group_member_id columns.
CREATE TABLE lightvm_templates (
    id          INTEGER PRIMARY KEY,
    name        TEXT    NOT NULL COLLATE NOCASE UNIQUE,
    description TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

CREATE TABLE lightvm_template_versions (
    id          INTEGER PRIMARY KEY,
    template_id INTEGER NOT NULL REFERENCES lightvm_templates(id) ON DELETE CASCADE,
    version     INTEGER NOT NULL CHECK (version >= 1),
    spec        TEXT    NOT NULL,
    created_at  INTEGER NOT NULL,
    UNIQUE (template_id, version)
);

CREATE INDEX idx_lightvm_template_versions_template
    ON lightvm_template_versions(template_id, version);

CREATE TABLE lightvm_groups (
    id                  INTEGER PRIMARY KEY,
    name                TEXT    NOT NULL COLLATE NOCASE UNIQUE,
    description         TEXT    NOT NULL DEFAULT '',
    template_version_id INTEGER NOT NULL REFERENCES lightvm_template_versions(id) ON DELETE RESTRICT,
    desired_count       INTEGER NOT NULL CHECK (desired_count >= 0),
    paused              INTEGER NOT NULL DEFAULT 0 CHECK (paused IN (0,1)),
    force_delete        INTEGER NOT NULL DEFAULT 0 CHECK (force_delete IN (0,1)),
    state               TEXT    NOT NULL DEFAULT 'active' CHECK (state IN ('active','deleting')),
    last_reconcile_at   INTEGER NOT NULL DEFAULT 0,
    last_error          TEXT    NOT NULL DEFAULT '',
    retry_attempts      INTEGER NOT NULL DEFAULT 0 CHECK (retry_attempts >= 0),
    next_retry_at       INTEGER NOT NULL DEFAULT 0,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);

CREATE INDEX idx_lightvm_groups_template_version
    ON lightvm_groups(template_version_id);

CREATE TABLE lightvm_group_members (
    id                  INTEGER PRIMARY KEY,
    group_id            INTEGER NOT NULL REFERENCES lightvm_groups(id) ON DELETE CASCADE,
    member_name         TEXT    NOT NULL COLLATE NOCASE UNIQUE,
    template_version_id INTEGER NOT NULL,
    template_version    INTEGER NOT NULL CHECK (template_version >= 1),
    state               TEXT    NOT NULL CHECK (state IN ('provisioning','active','removing')),
    desired_state       TEXT    NOT NULL DEFAULT 'running' CHECK (desired_state IN ('running','stopped')),
    retry_attempts      INTEGER NOT NULL DEFAULT 0 CHECK (retry_attempts >= 0),
    restart_attempts    INTEGER NOT NULL DEFAULT 0 CHECK (restart_attempts >= 0),
    cleanup_attempts    INTEGER NOT NULL DEFAULT 0 CHECK (cleanup_attempts >= 0),
    next_retry_at       INTEGER NOT NULL DEFAULT 0,
    last_error          TEXT    NOT NULL DEFAULT '',
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);

CREATE INDEX idx_lightvm_group_members_group_state
    ON lightvm_group_members(group_id, state);
CREATE INDEX idx_lightvm_group_members_version
    ON lightvm_group_members(template_version_id);

ALTER TABLE lightvms
    ADD COLUMN group_member_id INTEGER REFERENCES lightvm_group_members(id) ON DELETE RESTRICT;
ALTER TABLE disks
    ADD COLUMN group_member_id INTEGER REFERENCES lightvm_group_members(id) ON DELETE SET NULL;
ALTER TABLE virtual_interfaces
    ADD COLUMN group_member_id INTEGER REFERENCES lightvm_group_members(id) ON DELETE SET NULL;

CREATE UNIQUE INDEX idx_lightvms_group_member
    ON lightvms(group_member_id) WHERE group_member_id IS NOT NULL;
CREATE INDEX idx_disks_group_member ON disks(group_member_id);
CREATE INDEX idx_virtual_interfaces_group_member ON virtual_interfaces(group_member_id);
