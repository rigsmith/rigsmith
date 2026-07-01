---
"github.com/rigsmith/rigsmith": minor
---

Add `shiprig release --rehearse`: a full dry run that touches neither git history nor the network. It behaves like `--local` (skipping every network step — publish, push, release, issues) and additionally skips the git commit and tag, so version, build, and sign all run for real while nothing is committed, tagged, or pushed. Shorthand for `--local --skip commit,tag`; like `--local`, it is a real run and so is mutually exclusive with the plan-only `--dry-run` and the build-only `--dry-build`.
