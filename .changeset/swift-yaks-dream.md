---
type: docs
scope: shiprig
"github.com/rigsmith/rigsmith"
---

The per-ecosystem publish config is documented where it actually lives, under the key it actually uses: `.changeset/config.json`, keyed by ecosystem id — `node` for npm packages, not `npm`. Both docs said otherwise, and a block under an unrecognized key is never read and never complains, so anyone who followed them configured nothing at all and got no warning.