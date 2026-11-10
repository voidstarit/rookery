-- Additional host-directory virtiofs shares attached to a LightVM.
CREATE TABLE lightvm_virtiofs_shares (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    lightvm_id TEXT    NOT NULL REFERENCES lightvms(id) ON DELETE CASCADE,
    host_path  TEXT    NOT NULL,
    tag        TEXT    NOT NULL,
    read_only  INTEGER NOT NULL DEFAULT 1,
    position   INTEGER NOT NULL CHECK (position >= 0),
    UNIQUE (lightvm_id, tag),
    UNIQUE (lightvm_id, position)
);

CREATE INDEX idx_lightvm_virtiofs_shares_lightvm
    ON lightvm_virtiofs_shares(lightvm_id, position);
