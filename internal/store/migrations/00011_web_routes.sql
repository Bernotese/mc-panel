-- +goose Up
-- Several web ports per server (web port paths spec §3.1): path '' is the root, any other
-- path one segment of [a-z0-9-] (agentapi.ValidWebPath). The bound of 8 routes per server is
-- the handler's. Not part of the instance spec: changing them never bumps revision.
CREATE TABLE web_routes (
    server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    path      TEXT    NOT NULL CHECK (path = '' OR (length(path) BETWEEN 1 AND 32
                        AND path NOT GLOB '*[^a-z0-9-]*'
                        AND substr(path, 1, 1) <> '-' AND substr(path, -1, 1) <> '-')),
    port      INTEGER NOT NULL CHECK (port BETWEEN 1024 AND 65535 AND port NOT IN (25565, 25575)),
    PRIMARY KEY (server_id, path),
    UNIQUE (server_id, port)
);
-- A published web port becomes the root route with that port.
INSERT INTO web_routes (server_id, path, port)
SELECT server_id, '', web_port FROM instances WHERE web_port IS NOT NULL;
ALTER TABLE instances DROP COLUMN web_port;

-- +goose Down
-- Only the root route survives: 00010 has one port per server.
ALTER TABLE instances ADD COLUMN web_port INTEGER
  CHECK (web_port IS NULL OR (web_port BETWEEN 1024 AND 65535 AND web_port NOT IN (25565, 25575)));
UPDATE instances SET web_port = (SELECT port FROM web_routes r WHERE r.server_id = instances.server_id AND r.path = '');
DROP TABLE web_routes;
