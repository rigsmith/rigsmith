---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

three `release.jsonc` `vars` snags, all fixed. `fail(msg)` is in scope for a computed var (and an `if` gate), so `{ "script": "ctx.env.BUILD ? … : fail('set BUILD')" }` refuses with its own message instead of an unresolved-reference compile error — and the dry run says so up front. `${env.NAME}` inside a literal var's value now expands from the release environment the way it does in a step, rather than passing through to the shell unexpanded. And a captured var no longer needs `sh -c 'if …'` to differ by machine: `{ "os": { "macos": "…", "windows": "…", "linux": "…" } }` picks the command for the OS the release runs on (with `command` as the fallback for one not listed), and `{ "secret": "op://…" | "env:NAME" | "cmd:…" }` resolves a credential through the same resolver the publish `auth` config uses, masked either way.
