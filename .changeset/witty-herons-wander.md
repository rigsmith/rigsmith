---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

`shiprig publish` no longer reports a release that didn't happen. It publishes the versions already on disk and never versions, so it now refuses while changeset files are waiting to bump them, and says to run `shiprig version` and commit first, or `shiprig release` to do it all (`--allow-pending` publishes the versions on disk anyway; a dry run only warns). Before, a publish run with a changeset still pending re-pushed the last release, the feed quietly skipped the duplicate, and the run printed "published … pushed" for every package. A NuGet version the feed already holds is now reported as skipped, "already on" the feed, instead of as pushed.