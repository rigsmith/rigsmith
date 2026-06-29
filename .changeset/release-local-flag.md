---
"github.com/rigsmith/rigsmith": minor
---

Add `shiprig release --local`: run the full release pipeline for real but skip every network step (`publish`/`push`/`release`/`issues`), producing real local artifacts. Composes with `--only`/`--skip`/`--from`/`--to`; mutually exclusive with `--dry-run`/`--dry-build`.
