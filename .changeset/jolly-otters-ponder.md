---
type: feat
scope: rig
"github.com/rigsmith/rigsmith"
---

Add stack edge ci with credential-free build reports and an independent NuGet HTTP publisher that proves ids, versions, fork ancestry, package hashes and monotonicity; tip publishing requires explicit opt-in. Third-party dependency and assembly-name expectations remain build-job claims. Include a two-job workflow and security regression coverage; edge init is not built yet.

Require the same trusted run number in both CI jobs to prevent inflated-version reports. Permit non-empty packed subsets of the publisher’s text-only allowed ids, and use independently discovered NuGet sibling ids for cross-member checks. Protect the publish workflow with a default-branch-only edge-publish environment and environment-scoped secret.
