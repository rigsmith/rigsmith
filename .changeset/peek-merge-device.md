---
type: feat
scope: clauderig
"github.com/rigsmith/rigsmith"
---

Three verbs that were only reachable from code are now on the command line: `clauderig merge`, `clauderig peek` and `clauderig device`.

`merge` reconciles a staging repo that has diverged, using clauderig's own merge policies rather than asking you to hand-resolve a conflict between two machines' transcripts. `--abort` backs one out; `--json` emits what each file was resolved by, which is the record of a decision nobody was present for.

`peek` reads another machine's sessions straight from the remote without merging anything into this one — `peek list`, `peek show <id>`, and `peek materialize <id>` to bring a single conversation over. Useful when you want one session from the desktop upstairs and not its entire tree.

`device list` and `device remove <name>` inspect and clean up the synced device registry, which otherwise accumulates a row for every machine that ever synced, including the ones that no longer exist.
