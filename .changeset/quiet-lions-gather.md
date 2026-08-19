---
"github.com/rigsmith/rigsmith": patch
---

clauderig: extend the secret tripwire to non-JSON files. Until now only parsed JSON was scanned, so a credential that wasn't JSON — Claude Desktop's `.audit-key`, an `id_rsa`, a stray `.pem` — synced untouched. Files are now judged on their name (key material is conclusive; `.npmrc`/`.env`/`.netrc` are confirmed against their content first) plus two unambiguous content rules, and a hit refuses the sync naming the file. Deliberately narrow: transcripts are never entropy-scanned, because a tripwire hit aborts the whole sync and a false positive there would stop syncing entirely.
