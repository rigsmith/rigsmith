# verify: prove the artifacts agree before trusting a result

> Status: **implemented** (proposed 2026-08-17, shipped 2026-08-18). Adds
> `rig verify` — build, test and run in sequence — and, more importantly, a
> staleness check that catches the case the sequence alone cannot: artifacts from
> different verbs disagreeing with each other while every verb reports success.
>
> Code: [`internal/rig/stale`](../internal/rig/stale) (the checks — no commands,
> no writes, just mtimes) and
> [`internal/rig/cli/verify.go`](../internal/rig/cli/verify.go) (the verb).
> User docs: [verbs](../site/rig/verbs.md#verify) ·
> [configuration](../site/rig/configuration.md#artifacts). What the
> implementation settled, including the open question at the end of this
> document, is recorded under [As built](#as-built).

## The job

`build`, `test` and `start` each produce or consume artifacts, and **nothing
checks that the ones in play were produced together**. Each verb answers its own
question honestly and the answers are still collectively wrong.

This is not hypothetical. It cost an evening on a Chromium fork on 2026-08-16,
and the shape is worth reading because it generalises:

1. `rig tests` was configured to build `brave_browser_tests` and
   `brave_unit_tests`. It built only the unit tests — the underlying script takes
   **one comma-separated** `--target`, and the config passed the flag twice, so
   the first target was silently dropped.
2. The command then ran the browser-test binary anyway. It was two hours old.
3. Meanwhile a resource change had renumbered string IDs, regenerating the
   `.pak` files but not relinking that binary.
4. Every browser test crashed — in a feature nobody had touched — because a
   stale binary was reading resources that had moved underneath it.

Then the same shape again from the other side: `rig start` launched the app
bundle, which in a component build loads a **fresh** dylib from the output
directory while reading a **stale** `.pak` from *inside* the bundle, because
`tests` never builds the app target. The crash was an "invalid extension
manifest" — nowhere near the change that caused it.

Three verbs, three green results, one broken product. **Every stack trace
pointed at innocent code; the diagnosis came from comparing file timestamps.**

## What `verify` is for

Two jobs, and the second is the valuable one.

**Sequencing.** Run `build`, then `test`, then `run`, stopping at the first
failure. Cheap to implement and useful on its own — it makes "I checked" mean one
thing instead of three.

**Agreement.** Sequencing alone does not solve the problem. It hides it, by
rebuilding everything every time. That is fine for a Go service and unusable on a
project where a build is minutes to hours — exactly the projects where stale
artifacts survive longest, because nobody rebuilds casually.

So the real feature is: **detect that the artifacts disagree, without rebuilding
to find out.** Compare what each verb produces against what the next consumes,
and say so plainly when a consumer is older than its producer.

## Design

### `rig verify`

```
rig verify              # build → test → run, stop on first failure
rig verify --stale-only # report disagreement, run nothing
```

Exits non-zero on any failure, so it can gate CI or a pre-push hook.

### Staleness, where rig can infer it

For ecosystems rig already understands, the artifacts are knowable:

| Ecosystem | Produced | Consumed by run/test |
|---|---|---|
| Go | binary in `./bin` or `go build` output | same binary |
| Node | `dist/`, `build/` | same, plus `node_modules` vs lockfile |
| .NET | `bin/<config>/<tfm>/` | same |
| Rust | `target/<profile>/` | same |

The common, valuable case is nearly free: **is anything under the source tree
newer than the newest build output?** That catches "you edited and didn't
rebuild" for most projects with no configuration at all, and it is the check
people most often skip.

### Staleness, where rig cannot infer it

Chromium forks, generated resources, multi-artifact builds — rig has no way to
know these. They need declaration, and the declaration should be optional and
small:

```jsonc
"artifacts": {
  // Anything here that is older than its newest input is stale.
  "browser":    { "path": "../out/Component_arm64/Sheepish.app",
                  "inputs": ["**/*.cc", "**/*.grd", "**/*.gni"] },
  "unit-tests": { "path": "../out/Component_arm64/brave_unit_tests",
                  "inputs": ["**/*.cc", "**/*.h"] }
}
```

`rig verify --stale-only` then answers the question that actually mattered on
2026-08-16 — *are the things I am about to trust built from the code I have?* —
in a second, against a build that takes two hours.

## Scope decisions

- **Verbs are not re-implemented.** `verify` calls the existing `build`, `test`
  and `run` resolution paths. If `verify` and the individual verbs could ever
  disagree about what they run, `verify` is worse than useless — the same
  argument that says `rig explain` must reuse the resolver rather than reproduce
  it.
- **Absent config is not an error.** With no `artifacts` block, `verify` does
  sequencing plus the generic source-newer-than-output check, and says which
  checks it could not perform. Silence about a check that did not run is how a
  green result becomes misleading.
- **Staleness is a failure, not a warning**, under `--stale-only`. A warning in a
  long log is what got missed the first time.
- **No implicit rebuild on stale.** Report it and let the caller decide;
  `verify` without `--stale-only` already rebuilds by construction.
- **`run` needs a termination story.** Launching a browser or a server does not
  exit. Options: a `--no-run` flag, a timeout treating "still alive after N
  seconds" as success, or a per-project `verify.run` override. Timeout-as-success
  is probably right — it answers "does it start", which is the question — but
  this is the part of the design that most needs a second opinion.

  *Resolved: all three, with timeout-as-success as the default — see
  [As built](#as-built).*

## As built {#as-built}

The design above is what shipped. Five things it left open got settled by
writing it, and each is a place where the honest answer was not the obvious one.

**The run step's termination story: all three options, timeout-as-success as the
default.** `run` passes by *staying alive* — still running when `--run-timeout`
(default 10s) expires is the answer to "does it start"; an exit with a non-zero
status before then is a failure, and a clean exit before then is a pass too (a
CLI that does its job and stops has started fine). `--no-run` and
`verify.run: false` both drop the step, and each prints *why* it was skipped
rather than quietly running a shorter sequence. One caveat worth knowing: for
ecosystems whose run wrapper compiles first (`go run .`), the timeout has to
outlast that compile or "still alive" means "still building". The sequence
mitigates this by construction — `build` runs first, so the run step reuses a
warm cache.

**A directory artifact is judged by its OLDEST file, not its newest.** This is
the `.pak` case from the story above, and it is the whole reason the declared
form exists: a bundle whose newest file is minutes old can still hold a resource
the build never refreshed, so it looks fresh and loads stale data. Newest-file
semantics would have called that bundle fine. The report names the offending file
and counts the rest, because "some test crashed in code nobody touched" only
becomes a diagnosis once a specific file is named.

**The generic check uses newest-output semantics instead.** It has no
per-artifact dependency knowledge, so it asks the weaker question that cannot
produce false positives — is anything under the source tree newer than the newest
build output? "Source" is per-ecosystem and deliberately narrow (files a build
actually consumes), so editing a README never reads as "you didn't rebuild". A
check that cries wolf gets ignored, and an ignored check is the thing that cost
the evening.

**Staleness fails in both modes, not just `--stale-only`.** The design only
committed to failing under `--stale-only`. Making the full sequence's final
agreement check a warning would have reproduced the original failure exactly —
three green results, one broken product — so it exits non-zero too. The
`--stale-only` case remains the one that matters most in practice: it answers
"are the things I am about to trust built from the code I have?" in a second,
against a build that takes two hours.

**"Nothing could be checked" is its own outcome.** Absent config is not an error,
but a summary reading "artifacts agree (0 checks)" would be exactly the
zero-exit-while-wrong this verb exists to prevent. When every check was skipped,
the summary says so instead of printing a green line. Individual skips are always
listed with their reason.

One scope decision held up under implementation and is worth restating, because
it was the cheapest thing to get right and the most expensive to get wrong:
`verify` builds the *same* `build`/`test`/`run` commands the root tree registers
and runs those. It does not re-resolve targets. A test pins this
(`TestVerifyStepCmd_MatchesTheRootTreesVerbs`), on the theory that a `verify`
which could disagree with the verbs about what it ran would be worse than no
`verify` at all.

## Why this belongs in rig rather than in each project

Every project can hand-roll this, and none do, because it is invisible until it
costs a day. Rig already knows the ecosystem conventions and already owns the
verbs; it is the only place where "these artifacts were built together" can be
asked without every project inventing its own answer.

The generalisable lesson from the failure that prompted it: **a check that exits
zero while being wrong is worse than no check.** `verify` exists to make
"I checked" a claim about the product rather than about three commands.
