package instancespec

import "maps"

// PatchesDir is where every instance container sees the agent's itzg patch definitions:
// a read-only bind mount of a root-owned directory outside the data directory. Their
// content is fixed here; nothing in them comes from the panel.
const PatchesDir = "/mcpanel-patches"

// bukkitPatch disables Bukkit's connection throttle. Every player reaches the server
// through mc-router and so has the router's address; with Paper's default of 4000 ms a
// second player joining within 4 s is refused ("Connection throttled"). itzg downloads
// the default bukkit.yml before it patches, and the patch runs before every start, so a
// hand edit of the setting does not survive a restart.
const bukkitPatch = `{
  "file": "/data/bukkit.yml",
  "ops": [
    {"$set": {"path": "$.settings.connection-throttle", "value": -1, "value-type": "int"}}
  ]
}
`

const bukkitPatchFile = "bukkit-connection-throttle.json"

var patches = map[string]string{bukkitPatchFile: bukkitPatch}

// Patches returns the patch definition files the agent provides in PatchesDir, by file
// name (a fresh map on every call).
func Patches() map[string]string { return maps.Clone(patches) }
