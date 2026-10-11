-- +goose Up
-- Sub-project 3a: ops and the whitelist belong to the Minecraft server itself (ops.json,
-- whitelist.json) and are changed live over RCON; the panel no longer stores or syncs them.
-- Instances that had either list get a new revision so that their container is recreated
-- without the OPS/WHITELIST variables (running ones show "restart required").
-- The files on disk keep the lists; only names of a disabled whitelist (which TP2 emptied
-- in whitelist.json) are lost - they were not in effect.
UPDATE instances
SET properties = json_remove(properties, '$.ops', '$.whitelist'),
    revision   = revision + 1,
    updated_at = CAST(strftime('%s', 'now') AS INTEGER) * 1000
WHERE json_type(properties, '$.ops') IS NOT NULL
   OR json_type(properties, '$.whitelist') IS NOT NULL;

-- +goose Down
-- The removed lists cannot be restored here; ops.json and whitelist.json still hold them.
SELECT 1;
