---
type: feat
scope: shiprig
"github.com/rigsmith/rigsmith"
---

`shiprig publish --from-pack-dir <dir>` publishes exactly the files `shiprig pack` built there, in dependency order and under the plan's npm dist-tag, building nothing, as `changeset publish --from-pack-dir` does. Before anything is pushed, every file must still match the sha256 `pack` recorded and its package must be at the plan's version. npm and NuGet publish the prebuilt file; cargo, which publishes from source, is refused.
