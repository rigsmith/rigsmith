---
type: feat
scope: shiprig
"github.com/rigsmith/rigsmith"
---

`shiprig pack --out-dir <dir>` builds the package file for each release a publish would push (`npm pack`, `dotnet pack`) into `<dir>/packages/` and writes `<dir>/publish-plan.json` recording each file and its sha256 integrity, as `changeset pack` does. `--from-publish-plan <file>` packs a plan made earlier. It's the build half of a split release; cargo releases are refused, since cargo can't publish a prebuilt crate.
