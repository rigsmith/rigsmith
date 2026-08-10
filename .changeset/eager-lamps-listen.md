---
type: fix
"github.com/rigsmith/rigsmith"
---

`clauderig sync`'s secret tripwire no longer aborts the sync on two shapes it was misreading as credentials. Desktop names MCP tools `mcp__<server-uuid>__<tool>`, and the UUID escape hatch only recognized a UUID in *leading* position (`local_<uuid>`), so every approved MCP tool name in a Cowork session — and the matching entries in that session's `.claude/settings.local.json` — tripped as high-entropy. The entropy backstop now strips embedded UUIDs anywhere in the string and judges what's left, which covers both shapes; a blob that stays long and opaque after the UUID comes out still trips. npm/yarn lockfile `integrity` digests (`sha512-…`) are also exempt now: they're public content hashes of published packages, and they were tripping non-deterministically — only the ones whose base64 happened to contain no `/` got past the path filter, so a synced lockfile would fail on a random fifth of its entries.
