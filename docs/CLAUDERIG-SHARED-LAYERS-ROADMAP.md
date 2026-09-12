# ClaudeRig and CodexRig v2 delivery plan

## Scope reset

V2 accumulated 60,472 added lines relative to the branch point with main before
this reduction. The audit grouped those additions as 21,044 code/other lines,
27,751 test lines, 6,139 documentation lines and a 5,538-line bundled schema.
These are diff additions, not net repository size or a reason to keep complexity.

The Codex validation work expanded into a partial copy of the vendor runtime
without delivering a user-facing restore workflow. Remove it rather than finish
it. The former 8b.6 sequence is cancelled: vendored schemas/exact-version pinning, provider/MCP
rules, managed-layer interpretation, permission inheritance and network/glob
compilation are not v2 release requirements. PR #410's endpoint expansion is
superseded. Its useful Windows test-harness repair is retained independently.

## Retain and reuse

- Separate `clauderig` and `codexrig` commands, settings, state and repositories.
- Shared secret filtering, file allowlists and transcript chunking.
- Existing capture/publication and durable queue mechanics, through vendor adapters.
- Existing Git/`gh` integration; no custom SSH/HTTPS transport.
- Codex TOML sanitization, destination-local secret/path preservation, bounded
  capture, private restore previews, stale-plan checks and guarded file replacement.
- Claude's existing synchronous default and SessionStart behavior. Queued hooks
  stay explicitly opt-in; do not reinstall or activate anything during development.

The shared queue and publication code is still substantial. The next workflow
must reuse it directly; new abstractions or duplicate orchestration need evidence
from that workflow. Do not delete recovery/concurrency tests merely to reduce a
line count, or broadly rewrite working Claude behavior as part of this cut.

Queue simplification is now in progress: `queue sync` reuses worker drain, then
ordinary sync. Remove manual coverage tickets, per-file completion evidence and
the duplicate remote-confirmation helper. Session-index conflicts reuse native
ledger reconciliation. Keep durable snapshots, retries,
recovery and legacy queue files. TOML handling stays unchanged. Larger changes to
snapshot storage require demonstrated benefit and preservation of saved work;
they are not another prerequisite stage for the config workflow.

## Remaining delivery sequence

| Outcome | Work | Acceptance |
|---|---|---|
| 1. Simplify | Delete the unfinished vendor interpreter, its schema/dependency and wrappers; make additional restore validation optional; shorten the contracts and roadmap. | Safety tests and Claude compatibility pass. No vendor rules or exact CLI version gate remain in the restore path. |
| 2. Connect the config workflow | Enforce the Codex CLI 0.154.0 minimum once at the command boundary; add independent Codex settings/repository wiring and capture, sync, preview and restore commands using existing components. Finish bounded interrupted-restore reporting/recovery before exposing writes. | A synthetic two-home workflow captures on one side, syncs with Git, previews and restores on the other, preserving local credentials/paths. Repeat and interrupted runs have clear results. |
| 3. Ship the config workflow | Native OS tests, installation/help docs and an isolated Codex smoke check where a supported interface exists. | Linux/macOS/Windows round trips, stale-plan refusal, recovery and Claude compatibility pass. Supported file layouts are documented; no claim of complete Codex startup validation. |
| 4. Extend separately | Portable instructions/rules/skills, session artifacts/resume, then optional Codex queue/hooks. | Each extension has a complete user workflow and reuses existing storage/queue mechanics. It does not block the first config workflow. |

Current public capability is `codexrig inspect`. Config capture/merge/restore APIs
are internal. Outcome 1 removed runtime validators in #411; queue completion simplification is
the follow-up. Outcome 2 is next. The original
stages 1–7 and 8a/8b.1–8b.5 established the retained infrastructure. Detailed commit
history remains in Git instead of being repeated as release prerequisites here.

## Release boundaries

Target Codex CLI **0.154.0 or newer**, the latest stable release checked on
September 11, 2026. The [minimum-version policy](CODEXRIG-V2-CONFIG-VALIDATION.md#minimum-supported-codex-version)
is a simple floor for the upcoming config workflow, not an exact-version gate,
moving latest requirement or older-version compatibility framework.

A successful restore means accepted file content was installed with the documented
safety checks. It does not mean configured providers, proxies, MCP servers or
permissions will run successfully. Codex owns those semantics. A native smoke test
can provide evidence for a documented setup; it is not a general-purpose runtime
validator, and it must not execute arbitrary user helpers during preparation.

Keep tests that protect private data, recovery and working behavior. Run checks
appropriate to the changes and the native CI matrix. Fix and reply to actionable
reviews, then let the user merge. Group implementation around outcomes above;
stop treating every vendor edge case as a new blocking stage.

## Implementation references

- [Restore safety](CODEXRIG-V2-CONFIG-VALIDATION.md), [capture](CODEXRIG-V2-CONFIG-CAPTURE.md),
  [path policy](CODEXRIG-V2-CONFIG-PATHS.md), [preparation](CODEXRIG-V2-CONFIG-RESTORE.md),
  [file application](CODEXRIG-V2-CONFIG-APPLY.md).
- [Existing queue](CLAUDERIG-V2-QUEUE.md) and [queue commands](CLAUDERIG-V2-QUEUE-COMMANDS.md).
- [Summary roadmap](../roadmap.md).
