---
type: feat
scope: rig
"github.com/rigsmith/rigsmith"
---
`stack rm <repo>` takes a repo back out of a stackspace — the counterpart of `add` that was missing. It drops the manifest entry and cursor, deletes the directory (`--keep-tree` leaves it as an ordinary part of the repo), rewrites the build overlay from the remaining members — removing rig's own overlay file when nothing crosses between them any more, so no build keeps a `ProjectReference` to a vanished path — and commits. A repo holding work that has not left the stackspace is refused unless `--force` — and so is one holding files git ignores (build output, a local `.env`), named, since no history gets those back. Work `propose` has put on your fork counts as sent: `propose` keeps the commit it pushed under `refs/rigsmith/propose/<repo>`, so `status` and `rm` stop reporting it as unsent — and `rm` asks the fork that the branch still holds the commit as pushed before it relies on that. Offered in `rig ui` under Stack, with tab completion for the repo name.
