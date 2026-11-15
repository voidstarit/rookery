-- Time-limited bearer tokens for direct WebSocket access to a LightVM
-- console. Only the SHA-256 hash of a token is stored. Tokens are scoped to
-- one console and are removed when the LightVM or console is removed.
CREATE TABLE lightvm_console_access (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    lightvm_id TEXT    NOT NULL REFERENCES lightvms(id) ON DELETE CASCADE,
    console_id INTEGER NOT NULL REFERENCES lightvm_consoles(id) ON DELETE CASCADE,
    token_hash TEXT    NOT NULL UNIQUE,
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE INDEX idx_lightvm_console_access_lightvm
    ON lightvm_console_access (lightvm_id, expires_at);
CREATE INDEX idx_lightvm_console_access_expires
    ON lightvm_console_access (expires_at);
