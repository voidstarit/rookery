CREATE TABLE IF NOT EXISTS client_certificates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    serial TEXT NOT NULL UNIQUE,
    not_before INTEGER NOT NULL,
    not_after INTEGER NOT NULL,
    revoked INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);
