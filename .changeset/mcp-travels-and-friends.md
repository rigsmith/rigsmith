---
type: feat
scope: clauderig
"github.com/rigsmith/rigsmith"
---

`mcp list` and `mcp get` now say whether each MCP server will exist on your other machine, and the answer is more often "no" than people expect.

Every place Claude Code stores an MCP server is outside the tree clauderig syncs. User- and local-scope servers live in `~/.claude.json`, which sits *beside* `~/.claude` rather than inside it — so a backup that has run nightly for a year carries none of them, and nothing has ever said so. Project-scope servers do travel, but through your own repository rather than through the backup; what does not travel with them is the approval, which is recorded in the gitignored `.claude/settings.local.json`, so a fresh clone is asked again.

A `TRAVELS` column carries the verdict, with the details below it: which env or header values are committed to your repo in plain text, and which absolute paths are spelled for this machine only and will fail to start elsewhere. `mcp list --json` emits the same as records for a script to gate on.

`recent --json` is new alongside it, matching `search --json`.

Two smaller fixes: a file the publication audit condemns is now taken back out of the staging tree instead of being left there, one permissive run from being committed; and the dashboard's shortcut legend is derived from the actions actually on screen, so it can no longer name a key that does nothing.
