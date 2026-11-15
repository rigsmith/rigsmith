---
type: feat
scope: shiprig
"github.com/rigsmith/rigsmith"
---

shiprig's velopack packaging takes a `macos.entitlements` setting: a repo-root-relative `.entitlements` plist the macOS app is signed with, passed to `vpk pack` as `--signEntitlements`. Without it `vpk` signs with its bundled default, which grants JIT, unsigned executable memory, Apple Events automation, DYLD environment variables and disabled library validation. Like `notaryProfile`, it needs `macos.signIdentity` (a config with entitlements and no identity is rejected rather than shipping without them) and is skipped on a snapshot build. A path that doesn't exist fails the build before any channel is built.
