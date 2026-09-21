---
type: feat
scope: rig
"github.com/rigsmith/rigsmith"
---

npm packages publish with a short-lived credential minted from CI's own identity, instead of a stored token.

npm's trusted publishing binds a publisher to one workflow file, and allows one per package, so the npm recovery path moved from its own workflow into the release workflow as a second job. `Actions → Release binaries → Run workflow` now asks which: a dry run, or re-publishing the npm wrappers for a release that already exists. The old `npm republish` workflow is gone; its job does the same work from the same published archives, checksum-verified.

Publishing through CI's identity also attaches a provenance attestation to every package automatically, so anyone installing can see which workflow built it.