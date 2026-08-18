---
type: feat
"github.com/rigsmith/rigsmith"
---

`rig verify` runs build → test → run in sequence, then proves the artifacts were built together.

`build`, `test` and `run` each produce or consume artifacts, and nothing checked that the ones in play were produced *together*. Every verb answers its own question honestly and the answers are still collectively wrong. On a Chromium fork: `rig tests` built only the unit tests (the underlying script takes one comma-separated `--target`, the config passed the flag twice, so the first target was silently dropped), then ran a browser-test binary that was two hours old, while a resource change had renumbered string IDs and regenerated the `.pak` files without relinking it. Every browser test crashed in a feature nobody had touched. Then the same shape from the other side: `rig start` launched a bundle loading a fresh dylib beside a stale `.pak`, and the crash read "invalid extension manifest". Three verbs, three green results, one broken product — and every stack trace pointed at innocent code.

`rig verify` does the sequencing (stopping at the first failure, so "I checked" means one thing instead of three) and then the part that actually matters: it compares modification times to catch artifacts that disagree, *without* rebuilding to find out. Sequencing alone doesn't solve this, it hides it, by rebuilding everything every time — fine for a Go service, unusable where a build takes hours, which is exactly where stale artifacts survive longest.

With no configuration it asks the generic question: is anything under the source tree newer than the newest build output? Output locations follow the ecosystem (Go `bin`/`dist`, Node `dist`/`build`/`out`/`.next` plus `node_modules` against its lockfile, .NET per-project `bin/<config>/<tfm>`, Cargo `target/<profile>`), and "source" is narrow enough that editing a README never reads as "you didn't rebuild". Artifacts rig cannot infer — generated resources, multi-artifact builds, an `out/` tree beside the repo — are declared in `.rig.json`:

```jsonc
"artifacts": {
  "browser":    { "path": "../out/Component_arm64/Sheepish.app",
                  "inputs": ["**/*.cc", "**/*.grd", "**/*.gni"] },
  "unit-tests": { "path": "../out/Component_arm64/brave_unit_tests",
                  "inputs": ["**/*.cc", "**/*.h"] }
}
```

`rig verify --stale-only` then answers "are the things I am about to trust built from the code I have?" in a second, against a build that takes two hours:

```
  ✓ build output  up to date with main.go
  ✗ browser       out/App.app/Contents/Resources/en.pak is 2h older than src/strings.grd (and 1 more file)
  ✗ unit-tests    out/unit_tests is 2h older than src/renderer.cc
```

A directory artifact is judged by its **oldest** file, which is the whole point — a bundle whose newest file is minutes old can still hold a resource the build never refreshed. Staleness exits non-zero (a warning in a long log is what got missed the first time), checks that couldn't run are reported as skipped rather than counted as passes, and a report where nothing could be checked says so instead of printing a green line. The run step passes by staying alive: a server never exits, so "still running after `--run-timeout`" (default 10s) is the answer to "does it start" — `--no-run` or `verify.run: false` drops it. Each step is the same `build`/`test`/`run` command the root tree registers, because a `verify` that could disagree with the verbs about what it ran would be worse than no `verify` at all.
