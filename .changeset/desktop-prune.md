---
type: feat
scope: clauderig
"github.com/rigsmith/rigsmith"
---
`desktop prune` reclaims a profile's disk space without deleting it. By default it drops Electron's regenerable caches; `--vm` also drops the unpacked Cowork VM root filesystem — a sparse image that only ever grows — so Desktop re-extracts a pristine one on next launch; `--all` drops the whole bundle and accepts a re-download. Logins and chat history are never touched. `--dry-run` prints the per-profile breakdown, an open profile is refused, and the data-losing tiers ask first. `desktop list` now shows each profile's size, and `doctor` points at `prune` once the VM image is holding more than a few gigabytes.
