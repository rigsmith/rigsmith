# Testing a member PR in its stackspace

Keep the plain-clone build matrix: it tests released sibling packages. Add a
separate stackspace job to compile code and tests against sibling sources.
The two jobs answer different questions and should both be required checks.

The composite lives at `.github/actions/stack/` in rigsmith. The short name
`rigsmith/stack-action@v1` is the intended name but is not published yet. Copy that self-contained directory
into your member repository to use the local path below. The workflow in
[examples/github-workflows/stack.yml](../../examples/github-workflows/stack.yml)
is ready to copy after selecting a rig release containing `stack ci`.

```yaml
- uses: actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683 # v4
- uses: ./.github/actions/stack
  with:
    rig-version: vX.Y.Z # exact release containing stack ci
    manifest: stack/rig.stack.jsonc
    member: app
    test: dotnet test tests/App.Tests
```

Commit the manifest and its full `lastSync` cursors. `member` defaults to a
unique match of origin against manifest upstream/fork URLs; specify it for fork
PRs with a different origin. The selected tree is checkout HEAD, including a
GitHub PR merge commit when checkout selects one. Uncommitted and ignored files are not imported; commit source before running.
Submodules are not materialized: a committed `.gitmodules` or gitlink fails setup clearly.
Commands run with bash from the member prefix. Build defaults to `rig build`,
test to `rig test`; an empty command skips that phase. Install the language
SDKs your commands need using your existing workflow setup steps.

Other CI systems install a pinned rig and its pinned josh, then run
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

Caching, checksum-verified installation, warnings guards, dependency checks,
nightly canaries and topic testing are later work. Reverse-dependency matrices
and cursor-lag checks are deliberately outside this implementation.

No separate josh step is needed: stack init installs the manifest/default pinned
josh on demand before imports. Fresh-runner installation was reasoned through
the existing installer call path, not exercised against hosted downloads.
