---
"github.com/rigsmith/rigsmith": minor
---

Add `shiprig release --local` — run the whole release pipeline locally but skip every step that reaches the internet (`publish`, `push`, `release`, `issues`). The version bump, commit, build, sign, and local `tag` all run for real, so it exercises the full release and produces real artifacts while nothing leaves the machine. It's a real run (unlike `--dry-run`'s plan preview) and runs the full pipeline (unlike build-only `--dry-build`); it composes with `--only`/`--skip`/`--from`/`--to` for narrowing or resuming a local rehearsal, while the network steps stay skipped regardless. Mutually exclusive with `--dry-run` and `--dry-build`.
