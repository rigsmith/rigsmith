---
type: feat
---

rig: `worktree new/list/open/rm` accept `--repo <dir>` to act on another repo without cd'ing there — the same flag the hidden route-plumbing verbs already had. A Claude session pinned to one repo (the clauderig guard denies moving its cwd) can now create a sibling worktree for a different repo with `rig wt new <branch> --repo <path>`; the review-window opener also reads its config from the checkout being opened, not the cwd.
