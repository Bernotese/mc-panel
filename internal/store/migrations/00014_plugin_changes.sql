-- +goose Up
-- Plugins (spec §6.2): the time of the last plugin change the panel made per server. Compared
-- with the container's start time on every inventory, so the restart notice survives a panel
-- restart and clears with the first start after the change; never reset.
CREATE TABLE plugin_changes (
    server_id  INTEGER PRIMARY KEY REFERENCES servers (id) ON DELETE CASCADE,
    changed_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE plugin_changes;
