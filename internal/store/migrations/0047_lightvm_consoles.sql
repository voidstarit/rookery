-- LightVM virtio-console ports. Every LightVM has at least one console; the
-- first is exposed as org.example.console for compatibility, additional ports
-- use org.example.console.<id>.
CREATE TABLE lightvm_consoles (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    lightvm_id TEXT    NOT NULL REFERENCES lightvms(id) ON DELETE CASCADE,
    name       TEXT    NOT NULL COLLATE NOCASE,
    port_name  TEXT    NOT NULL,
    position   INTEGER NOT NULL,
    UNIQUE (lightvm_id, name),
    UNIQUE (lightvm_id, port_name)
);

CREATE INDEX idx_lightvm_consoles_lightvm ON lightvm_consoles (lightvm_id, position, id);

INSERT INTO lightvm_consoles (lightvm_id, name, port_name, position)
SELECT id, 'console', 'org.example.console', 0 FROM lightvms;
