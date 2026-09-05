---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

`SquashedRoot` moves from `commands` to `status`, where the knowledge belongs.

It answers a question about a repository's shape — is this root commit one our own squash wrote, or is it where history actually began — and it was living in the package that defines the CLI's verbs. Every caller was importing the entire cobra command tree to ask something about a string.

No behaviour changes. It matters because it was the only thing tying a non-CLI reader to the command tree.
