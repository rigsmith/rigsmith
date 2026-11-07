---
type: feat
scope: rig
"github.com/rigsmith/rigsmith"
---

Add stack edge ci with credential-free build reports and an independent NuGet HTTP publisher that proves ids, versions, fork ancestry, package hashes and monotonicity; tip publishing requires explicit opt-in. Third-party dependency and assembly-name expectations remain build-job claims. Include a two-job workflow and security regression coverage.

Require the same trusted run number in both CI jobs to prevent inflated-version reports. Permit non-empty packed subsets of the publisher’s text-only allowed ids, and use independently discovered NuGet sibling ids for cross-member checks. Protect the publish workflow with a default-branch-only edge-publish environment and environment-scoped secret.

Stream report-job build and test diagnostics to stderr and preserve subprocess failure errors without putting output in report files.

Keep short feed-read timeouts separate from package uploads. Bound pushes with a 15-minute context deadline and two-minute response-header timeout, and explain how to check for a package that may have been stored after a timeout.

Render the GitHub run-number expression literally in the stack documentation so Vue does not evaluate it during the documentation build.

Keep CI-only commands out of the interactive edge menu; prompt for member and PR inputs when choosing pack or adopt.
