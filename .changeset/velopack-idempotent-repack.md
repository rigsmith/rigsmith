---
"github.com/rigsmith/rigsmith": patch
---

Velopack: re-packing the same version is now idempotent. Before `vpk pack`, the adapter drops that exact version+channel's existing nupkg(s) from the output directory, so vpk no longer rejects the build with "a release equal or greater already exists." Only the version being packed is cleared — prior versions stay (their nupkgs feed delta generation and vpk rebuilds the channel manifest from what remains). This makes `shiprig release --from build` resume cleanly after a partial failure, and lets a local build re-run the same version without manually clearing `dist/releases`.
