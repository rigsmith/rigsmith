---
type: fix
---

`clauderig sync` no longer syncs `node_modules`. A Cowork session that runs a build leaves its dependency tree inside the session's `outputs/` dir, which sits under an allowed tree — so tens of thousands of reinstallable files rode along with the session metadata (10 MB in one session here, plus the lockfile churn that was tripping the secret scanner). Allowlist rules gained an any-depth form, `**/name`, which matches that segment wherever it appears and prunes the whole subtree; specificity is now measured by how many path characters a rule pins down rather than raw pattern length, so a short any-depth exclude correctly outranks the long include it sits inside. Both roots exclude `**/node_modules`, covering session build output and skills with npm deps alike. Note that this only governs what future syncs stage: a `node_modules` tree already committed to your sync repo stays there until you remove it.
