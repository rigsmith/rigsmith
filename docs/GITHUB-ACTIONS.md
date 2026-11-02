# GitHub Actions

rigsmith ships **two composite GitHub Actions**, both in this repo under
[`.github/actions/`](../.github/actions). Together they automate the full changesets workflow on
GitHub, the same two-piece model the Node `@changesets` project uses (an Action for releasing, a
bot for enforcement) — except here the "bot" is a second Action, so there is **nothing to host**.

| Action | Triggers on | What it does |
|---|---|---|
| [`release`](../.github/actions/release) | `push` to your release branch | Opens/updates the **Version Packages** PR when changesets are pending; **publishes** when none are. |
| [`require-changeset`](../.github/actions/require-changeset) | `pull_request` | **Comments** on and **blocks** any PR that adds no changeset (the `@changesets` bot, sans server). |

Ready-to-copy workflows live in
[`examples/github-workflows/`](../examples/github-workflows).

> **For releasing, prefer [rigsmith/shiprig-action](https://github.com/rigsmith/shiprig-action).**
> It's changesets/action driving shiprig, and it goes further than the `release` composite action
> here: the version PR is titled with what it releases (`chore: release 1.2.0`), publishing happens
> only when that PR merges, settings can live in a committed `shiprig-action.jsonc`, and it runs
> well as a GitHub App (so the PR's CI starts on its own and pushed tags start tag-driven release
> workflows). rigsmith releases itself with it. The `release` action below stays for repos already
> using it; `require-changeset` has no counterpart there and is still the gate to use.

## The shared model

Changesets splits releasing into two moments. The key idea: **contributors don't edit versions or
changelogs** — they drop a changeset markdown file into `.changeset/` describing their change (which
packages, what bump level, a summary). The automation turns those files into versions, changelogs,
and published packages, with a human approval gate in the middle (a PR).

```
push to main
   │
   ├─ pending changesets exist?  ──► YES ──► shiprig version:
   │                                          consume .changeset/*.md,
   │                                          bump versions, write CHANGELOGs,
   │                                          commit to changeset-release/main,
   │                                          open/update "Version Packages" PR
   │
   └─────────────────────────────► NO  ──► shiprig publish:
                                            push packages to registries, create git tags
```

The loop in practice:

1. Feature PRs merge to `main`, each carrying a changeset. → The `release` action keeps a
   **Version Packages** PR open, continuously updated, previewing the next release.
2. When you're ready, you **merge that PR**. That deletes the changeset files and commits the bumped
   versions/changelogs.
3. The next push to `main` finds **no** changesets → `release` runs **publish** → packages go out,
   tags are created.

So the "release button" is just merging the bot's PR. The `require-changeset` action is what keeps
step 1 honest: it makes sure every feature PR actually carries a changeset.

## 1. The `release` action

New setups should use [rigsmith/shiprig-action](https://github.com/rigsmith/shiprig-action)
instead (see the note above); this action is kept for repos already on it.

A faithful successor to the net-changesets composite action, driving `shiprig` instead of the .NET
CLI. It is **polyglot for free** — `shiprig` runs the same engine across .NET, Node, Go, and Rust, so
a single `version`/`publish` drives every ecosystem in the repo.

Two improvements over the net-changesets version:

- **It installs `shiprig` for you** (`install: true`, default) — download the goreleaser asset, or
  `go install` from the module proxy as a fallback. net-changesets required you to put the CLI on
  `PATH` first.
- Same configurable `version` / `publish` / `status` inputs and the same outputs
  (`hasChangesets`, `published`, `publishedPackages`, `pullRequestNumber`), so anyone who has used
  `@changesets/action` recognizes it 1:1.

See the [action README](../.github/actions/release) for the full input/output tables.

## 2. The `require-changeset` action (the gate)

This is the piece net-changesets **deferred and never built** — the per-PR "add a changeset" nag and
block. On the Node side that is a separate hosted GitHub App (`changeset-bot`). rigsmith does it as a
plain Action instead:

- it **upserts a sticky comment** on the PR — a nag with `changerig add` instructions when a
  changeset is missing, a ✅ when present (identified by a hidden marker so it edits in place rather
  than spamming);
- it **fails the check** when none is found. Mark `require-changeset` as a **required status check**
  in branch protection and a missing changeset blocks the merge;
- a `skip-changeset` label waives the requirement for docs/CI/refactor-only PRs.

### Action gate vs. a hosted App — why no server

A hosted GitHub App and this Action do the same two visible things — comment and block. The only
real difference is **fork PRs**: a `pull_request` workflow on a PR from a fork gets a read-only
token, so the Action can't post the comment there (the failing check still blocks). A hosted App
carries its own installation token and can always comment. If you need fork-PR comments, run the
action from a `pull_request_target` workflow (with the usual caveats — see the action README).
For same-repo PR flows, the Action gate is the complete bot with zero infrastructure.

## Dogfooding on this repo

rigsmith keeps a `.changeset/` folder and releases through
[rigsmith/shiprig-action](https://github.com/rigsmith/shiprig-action), running as the shipRig GitHub
App, in one workflow ([`.github/workflows/release.yml`](../.github/workflows/release.yml)):
changesets keep a `chore: release x.y.z` PR open, and merging it waits for CI on the merge
commit, then tags each package that release bumps and hasn't tagged yet. For `vX.Y.Z` (the CLIs)
the same run builds and publishes, with the changelog entry as the GitHub release's notes;
`ui/vX.Y.Z` (the UI) starts [`release-ui.yml`](../.github/workflows/release-ui.yml). A CLI-only
release makes no `ui/` tag, so only the CLI build runs. The composite
`release` action here isn't used by this repo; it's built for other repos that want it. Copy the
[example workflows](../examples/github-workflows) to adopt it and `require-changeset`.

## Stackspace member CI

`rig stack ci --manifest stack/rig.stack.jsonc --member app --test "rig test"`
builds the committed checkout against sibling sources in a temporary stackspace.
Keep this job alongside the plain-clone matrix. The self-contained composite is
`.github/actions/stack/`; `rigsmith/stack-action` is the intended name but is not published yet.
See [the guide](https://github.com/rigsmith/rigsmith/blob/main/docs/guides/testing-a-member-pr-in-its-stackspace.md) for a copyable workflow and other CI usage.

`--topic <name>` (action input `topic`) tests every open proposal PR head in
that manifest topic together. Missing, closed, untrusted or known conflicting
PRs fail setup. Head repositories must match declared fork/upstream repositories.
Use read-only `pull_request` jobs without secrets: code from every participating
PR runs. Name the job `stack topic`; the check covers only its triggering PR,
so run it in each member and rerun after sibling heads move. Topic mode disables
shared caches in the action and cannot combine with upstream-tip. See the
[stack guide](https://rigsmith.dev/rig/stack#testing-a-complete-topic) for trust,
cursor and provenance semantics. No merge train is installed.

For stack builds use ordinary `pull_request`, no secrets, `contents: read`, and
checkout `persist-credentials: false`. Do not use `pull_request_target` to execute
PR code: its base-repository privileges expose tokens and secrets. If privileged
metadata handling is necessary, isolate it in a metadata-only workflow that
never checks out PR code or executes untrusted artifacts. This restriction is
stronger than the separate changeset-comment workflow described above.

Pin all external actions by full reviewed commit SHA. `rigsmith/stack-action`
is not published yet; copy the local directory from a reviewed commit meanwhile.
The action verifies rig's release `checksums.txt`; pin `rig-sha256` in the workflow
for protection if the release itself is compromised. Release-provided checksums
alone detect corruption or a tampered mirror, not replacement of the release.
`allowed-hosts` defaults to `github.com`; explicitly list trusted enterprise hosts.
Member bundle reuse skips upstream fetch/filter work; only consumed hits count.
Commit/filter provenance is uploaded as JSON. See the action README for cache
keys, package caches, engine pinning, and the deliberate LFS refusal.

Set action `cache-history: 'false'` (CLI `--cache-history=false`) for builds that
should not trust restored filtered history. Cached bundles are not verified
against upstream cursors; provenance records `source` and actual `filteredCommit`
so a cached import is explicit. See the action README for the cache scope trust
boundary and reproducibility limits.

Fork PRs (head repository differs from the workflow repository) skip both shared
cache restore/save steps and disable member-bundle creation. Their summary
reports that caches were skipped. For eligible jobs, `cache-history` and
`cache-packages` independently default to `'true'`; set both to `'false'` to turn
off this action's shared caches. Push/schedule events retain caching, while
same-repository PRs can still read base-branch caches by GitHub's design. These
action guards do not change GitHub's cache access policy or erase existing
self-hosted runner files. Real hosted fork behavior is unverified locally; see
the action README for the confidentiality and trust limits.

### Stack topic dependencies

`rig stack deps --manifest rig.stack.jsonc` checks the committed `proposals`
records through the GitHub API, using `GITHUB_TOKEN` and `GITHUB_API_URL`.
Without a token, local runs reuse stack's gh-first, git-helper-second credentials.
Manifests with no dependency edges make no API or credential lookups.
Propose upstream members first: a new proposal records the already-proposed
members of that topic in `proposals[prefix][topic].dependsOn` (an array of
member prefixes). Re-proposing preserves these edges; edit the array to change
direction or backfill older proposals. Each dependency must merge upstream and
its `lastSync` cursor must include the API-reported merge commit. `rig stack status` displays the same chain
from the local manifest. Older records without `dependsOn` have no dependencies.

`rig stack deps --publish` posts the `stack dependencies` commit status to
current PR **head** SHAs only in `GITHUB_REPOSITORY`, which must match a
member upstream. Unset or unmatched repository scope refuses publication. The composite action enables this with its
`dependencies` input (default `true`), which accepts only literal `true` or
`false`. Dependency reporting failures warn without changing the build/test
result, including an older rig pin without `stack deps` or a summary-write
error. Workflows may optionally grant
`statuses: write`; read-only fork tokens produce an annotation and job summary
instead of failing for a denied write. API errors and private repositories
without access appear as **could not check**, never as passing. Do not use
`pull_request_target` to grant these writes.

For automatic refresh, copy `examples/github-workflows/stack-dependencies.yml`
into each dependent member repository's `.github/workflows/` directory and set
its release pin and manifest path. The hourly schedule uses the default branch's
committed manifest and API PR head SHAs; it needs no PR event payload. Keep cursor
bumps in that manifest current. A sweep then refreshes pending checks after upstream
merges and cursor updates without waiting for another PR event. Nothing is installed
by default, and the workflow has not been run on real GitHub. The token needs read
access to private dependencies and optional `statuses: write` in this repository;
it never publishes statuses in sibling repositories. Rerunning an old checkout
still evaluates old cursors. Local publication also requires explicit
`GITHUB_REPOSITORY=acme/app` scope.
A caller must treat pending statuses as a merge gate if desired; the reporting
command itself succeeds when dependencies are pending or unavailable.

`rig stack ci-guard app/App.csproj` guards one .NET build and records observed
holdback in `GITHUB_STEP_SUMMARY`. Plain clones opt in with
`<StackOnlyWarning Include="ACME0001" />`. Stack fusion exemptions require an
observed NU1603/NU1608/NU1605 event matched to a rewired package identity.
New member-code warnings fail except for explicit stack-only declarations and
existing member warning tolerances. In a stack, sibling warnings, including SDK
warnings such as `NETSDK1138`, remain visible, counted and non-fatal unless
`strict: 'true'` is set. NU/MSB warnings without exact swap attribution fail
closed; `MSB3277` remains fatal.

Action `guard-project` (CLI `stack ci --guard-project`) replaces only the build
step. The test command runs independently; any build it starts uses its own
warning policy. Guard the test project, then use
`test: dotnet test tests/Acme.Tests.csproj --no-build` to reuse its outputs for
the same configuration and target frameworks. A test command building another
graph is outside the guard's policy.

Stack-mode fusion holdback is not yet proven against a real stack: .NET 10
accepted the tested swapped project regardless of its package constraint and
emitted no NU16xx in that fixture. Runtime log-message warning patterns are not
implemented. See [Stackspaces](../site/rig/stack.md#ci-warning-guard) for the
verified scope and remaining limits.
