# ClaudeRig v2 compatibility baseline

The shared-layer work targets `codex/v2`. Implementation PRs target that
integration branch; `main` remains available for 1.x fixes. ClaudeRig and CodexRig
remain separate executables with their own configuration, state and backup
repositories. No v2 release is published by creating or updating this branch.

The first milestone establishes compatibility evidence. The next milestone
extracts synchronous sync/pull/reconcile services from command handlers. Artifact
adapters and shared mechanics follow, then store coordination and the durable
queue, and then CodexRig as a second consumer. Queued Claude hooks will initially
require explicit activation.

## Pinned baseline

`internal/clauderig/compatibility` compares the candidate working tree against
`b8711429ada6e570e5ccf0259ee3d1064a4b4e64`, the release commit immediately after
the scanning and transcript chunking changes in PR #283. It exports that commit
with `git archive` and builds both CLI binaries with the current Go toolchain.
The comparison tests application behavior, not historical compiler behavior.

The reference is deliberately fixed. Do not replace it with `main`, the PR base,
or `HEAD`: that would allow an extraction regression to redefine its own expected
behavior. Change it only in a reviewed compatibility decision with evidence for
the intentional differences. Known baseline defects should get explicit fixes
and revised assertions, rather than becoming desired behavior by accident.

## Running the checks

From a checkout with the pinned commit available locally:

```sh
CLAUDERIG_E2E=1 go test ./internal/clauderig/e2e -count=1 -v
CLAUDERIG_COMPAT=1 go test ./internal/clauderig/compatibility -count=1 -v -timeout=10m
```

CI enables synthetic end-to-end tests and the baseline comparison on Linux,
macOS and Windows, for PRs and pushes to the v2 integration branch. A shallow
local checkout must fetch the pinned commit before running the comparison.

`CLAUDERIG_E2E=1` does **not** enable the optional scan of a user's real Desktop
metadata. That test now requires `CLAUDERIG_REAL_DATA_E2E=1`, which CI does not set.

## Evidence collected

Each binary receives a separately created temporary home, source trees, state
directory, and local bare remote. Child processes have isolated Git/vendor/home
configuration, fixed Git identity and file-only transport. The harness does not
call account login, switch, keychain, Desktop launch, or installation commands.
Building the binaries can use the usual Go module cache and dependency downloads.

The command scenarios cover:

| Scenario | Explicit contract |
| --- | --- |
| Sync and restore | Large transcripts become chunks and restore to identical native bytes; live sources survive; settings merge preserves local secrets; backup precedes prune. |
| Desktop profiles and attribution | Separate profile roots retain metadata, exclude cookies, and distinguish Desktop provenance from the current CLI login. Unknown login stays unattributed. |
| Dry run and tripwire | Dry run stages and scans without committing; a detected credential prevents publication; a cleaned retry succeeds. |
| Offline push | Local capture survives transport failure and reaches the remote on retry without another source edit. |
| Abandoned merge | A conflicted settings merge is settled before sync replaces the staged snapshot. |
| Retention and flush | Old/oversized files are excluded; blank hook input flushes none; a named session flushes only that session; empty stdin flushes all. |
| Fresh-machine pull | Auto-restore is disabled unless configured, restores a fresh machine when enabled, and leaves an established machine's settings alone. |

For each checkpoint, the harness compares file presence, bytes, modes and symlink
targets, plus local/remote branch names, commit counts and committed payloads.
It compares exit codes and checks meaningful diagnostic substrings separately
for both binaries. It does not compare full terminal rendering or Git progress.
Only temporary sandbox roots and known generated metadata timestamps are
normalized. Transcript content/timestamps, attribution, journal outcomes,
unknown metadata and branch selection remain significant. Commit IDs are not
compared because time and fixture paths can legitimately change them.

The existing engine and command tests remain part of the gate. They provide
focused coverage for malformed records, symlink/prune protections, scoped
subagent flushes, merge policies, and profile permissions. The synthetic
round-trip tests also cover portable path translation in both directions and
chunked Git transport to a second home. These checks complement the command
comparison; they do not prove that either vendor's GUI can resume every session.

Before extracting a workflow not covered by these scenarios, add its observable
contract here. Keep each extraction independently reviewable and reversible.
