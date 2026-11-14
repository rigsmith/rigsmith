---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

shiprig no longer reads a failed npm visibility lookup as a new or private package. When a package is published with access other than public, shiprig asks the registry whether it already holds the package as public before attesting provenance. A lookup that failed (the registry unreachable, a refused token) used to count as "not public": under `provenance: auto` a public package then published without its attestation, and under `always` the publish failed saying the package was new or private. The lookup is now tried once more, then `auto` skips the attestation naming the failure ("registry visibility lookup failed: …") and `always` fails with it. A package the registry does not have (E404) is still treated as new or private.