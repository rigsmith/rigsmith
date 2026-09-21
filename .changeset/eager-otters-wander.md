---
type: feat
scope: rig
"github.com/rigsmith/rigsmith"
---

npm packages now publish with no stored credential at all. Every wrapper package has a trusted publisher registered for the release workflow, so CI mints a short-lived credential from its own identity — nothing to expire, nothing to rotate, nothing to leak. Because the repository is public, npm also attaches a provenance attestation to each package, so anyone installing can verify which workflow built it.

Republishing is idempotent as well: recovering a release whose npm step alone failed no longer stops at the first version that is already on the registry, which is the normal state of a recovery run and used to abort it.