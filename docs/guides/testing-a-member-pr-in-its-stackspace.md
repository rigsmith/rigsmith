# Testing a member PR in its stackspace

Keep the plain-clone build matrix: it tests released sibling packages. Add a
separate stackspace job to compile code and tests against sibling sources.
The two jobs answer different questions and should both be required checks.

The composite lives at `.github/actions/stack/` in rigsmith. The short name
`rigsmith/stack-action` is the intended name but is not published yet. Copy that self-contained directory
into your member repository to use the local path below. The workflow in
[examples/github-workflows/stack.yml](../../examples/github-workflows/stack.yml)
is ready to copy after selecting a rig release containing `stack ci`.

```yaml
- uses: actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683 # v4
  with:
    persist-credentials: false
- uses: ./.github/actions/stack
  with:
    rig-version: vX.Y.Z # exact release containing stack ci
    manifest: stack/rig.stack.jsonc
    member: app
    test: dotnet test tests/App.Tests
```

Use `seed: acme/rigstack-app@main` (CLI `--seed acme/rigstack-app@main`)
to read the committed manifest from its home, or `manifest` / `--manifest`
to read it from checkout HEAD. Exactly one is required. Seed references use the
same owner/repo@ref, URL, scp and local-path syntax as `stack setup`.
CI never infers this input from a member's `.rig-stack` pointer: workflows
explicitly choose their manifest source. Origin matching still selects the member.
The seed host must be in `allowed-hosts`; local paths have no host.
Private seeds use configured Git credentials and may be unavailable to fork PRs;
fetch failures name the seed and exit at setup (10). Configure credentials outside
the action without embedding tokens in workflow inputs.
The summary and provenance artifact record the sanitized seed and resolved commit.
Topic dependency reporting also reads the seed, using
`rig stack deps --seed <ref> --allowed-hosts github.com --publish`.

Commit the manifest and its full `lastSync` cursors. `member` defaults to a
unique match of origin against manifest upstream/fork URLs; specify it for fork
PRs with a different origin. The selected tree is checkout HEAD, including a
GitHub PR merge commit when checkout selects one. Uncommitted and ignored files are not imported; commit source before running.
Submodules are not materialized: a committed `.gitmodules` or gitlink fails setup clearly.
Commands run with bash from the member prefix. Build defaults to `rig build`,
test to `rig test`; an empty command skips that phase. Install the language
SDKs your commands need using your existing workflow setup steps.

Other CI systems install a pinned rig, then run
`rig stack ci --manifest stack/rig.stack.jsonc --member app --test 'dotnet test'`.
`--upstream-tip` selects upstream branch tips instead of cursors. Cursor mode
ignores proposed fork branches so its sibling sources remain reproducible.

Resolved commits and command output appear in the log. When set,
`GITHUB_STEP_SUMMARY` receives the member table and phase results. Exit statuses
are 10 for setup (including tree replacement), 11 for wire, 12 for build and 13
for test; subsequent phases stop on failure. Test output uses the test runner's
normal console format; use your runner's report options and existing artifact
upload steps if you need structured test artifacts.

The job needs no write token or secrets. Use `pull_request` and read-only
`contents` permission, never privileged PR execution. Private upstream access
may fail for fork PRs; the import error names the inaccessible member. Supply
credentials only in trusted CI contexts. No proposal or push occurs.

Warnings guards, dependency checks,
nightly canaries and topic testing are later work. Reverse-dependency matrices
and cursor-lag checks are deliberately outside this implementation.

No separate josh step is needed: stack init installs the manifest/default pinned
josh on demand before imports. Fresh-runner installation was reasoned through
the existing installer call path, not exercised against hosted downloads.

Pin external actions by full reviewed SHA after publication. Until then copy
from a reviewed rigsmith commit. The action verifies rig release checksums and
supports `rig-sha256` pinned independently in the workflow; release checksums do
not defend against replacement of the whole release. `allowed-hosts` defaults
to `github.com`. Both fork and upstream must match; opt in enterprise hosts
explicitly. Josh is version/checksum pinned and restored binaries are rechecked.
Cursor-keyed member caches skip unchanged imports; only consumed hits enter the
summary rate. Member commit/filter provenance is uploaded as JSON. LFS members
are refused clearly rather than testing pointer files.

Use no secrets, `permissions: {contents: read}` and checkout
`persist-credentials: false` for fork PRs. Never execute PR code under
`pull_request_target`; if privileged metadata handling is ever needed, isolate
it in a workflow with no PR checkout and no execution of untrusted artifacts.

Set action `cache-history: 'false'` (CLI `--cache-history=false`) for builds that
should not trust restored filtered history. Cached bundles are not verified
against upstream cursors; provenance records `source` and actual `filteredCommit`
so a cached import is explicit. See the action README for the cache scope trust
boundary and reproducibility limits.

Write structured test reports to an absolute path outside the temporary stack,
for example under `$GITHUB_WORKSPACE/test-results`. Relative report files in
the member directory are deleted with the temporary stack before later steps
run. Configure your test runner's output path accordingly and use `if: always()`
on the subsequent artifact upload step so failure reports are retained too.

Committed symlinks must stay within the member prefix. Rooted targets and
links escaping that prefix are rejected, including Windows rooted paths. If
the runner cannot create a contained symlink, setup fails (exit 10) naming the
file and target; it does not replace the link with a regular file. Windows
runners may need Developer Mode or symlink-creation privileges for members
that commit symlinks. Members without symlinks need no such privilege.

Fork PRs (head repository differs from the workflow repository) skip both shared
cache restore/save steps and disable member-bundle creation. Their summary
reports that caches were skipped. For eligible jobs, `cache-history` and
`cache-packages` independently default to `'true'`; set both to `'false'` to turn
off this action's shared caches. Push/schedule events retain caching, while
same-repository PRs can still read base-branch caches by GitHub's design. These
action guards do not change GitHub's cache access policy or erase existing
self-hosted runner files. Real hosted fork behavior is unverified locally; see
the action README for the confidentiality and trust limits.
