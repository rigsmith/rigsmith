# Stackspace CI action

Intended final home: `rigsmith/stack-action`, which is not published yet.
Until publication, copy this entire directory into your member repository and
use the local path. It has no relative dependencies outside this directory.

```yaml
- uses: actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683 # v4
  with:
    persist-credentials: false
- uses: ./.github/actions/stack
  with:
    rig-version: vX.Y.Z # select an exact release containing stack ci
    manifest: stack/rig.stack.jsonc
    member: app
    test: rig test
```

Keep your plain-clone build matrix alongside this job. Install required language
SDKs as usual. The checkout may be shallow. `rig-version` is required and must
be an exact release tag. Installation verifies the archive against the release's
`checksums.txt` before extraction; all download and verification failures are
fatal, including missing assets. Both downloads retry transient failures up to
three times with a 30-second connection timeout and a 300-second total limit
per attempt. Curl retries transfer timeouts with `--retry`; checksum mismatches remain fatal. No source fallback survives. Josh uses rig's
built-in version and source-pinned per-platform SHA256; custom pins are refused
in CI. Restored josh binaries are hashed again before execution.

`manifest` is required.
`member` defaults to matching origin URL. `upstream-tip: 'true'` selects tips
instead of committed sibling cursors. `build` and `test` default to `rig build`
and `rig test` and run from the member prefix with bash; empty skips a command.

`--topic <name>` (action input `topic`) tests every open proposal PR head in
that manifest topic together. Missing, closed, untrusted or known conflicting
PRs fail setup. Head repositories must match declared fork/upstream repositories.
Use read-only `pull_request` jobs without secrets: code from every participating
PR runs. Name the job `stack topic`; the check covers only its triggering PR,
so run it in each member and rerun after sibling heads move. Topic mode disables
shared caches in the action and cannot combine with upstream-tip. See the
[stack guide](https://rigsmith.dev/rig/stack#testing-a-complete-topic) for trust,
cursor and provenance semantics. No merge train is installed.
Topic mode captures the API token and removes token variables before starting
children; it refuses persisted checkout HTTP extraheaders. Use
`persist-credentials: false`. A PR-owned manifest is contributor-controlled
policy; use a reviewed immutable policy checkout for an independent allowlist.
Copy the [topic workflow](../../../examples/github-workflows/stack-topic.yml)
for a job named `stack topic` with that separate manifest source.

`guard-project` replaces the build command with `rig stack ci-guard` for that
member-relative .NET project. New warnings in member code fail except for
explicit stack-only declarations and existing warning tolerances. In a stack,
sibling warnings, including SDK warnings such as `NETSDK1138`, remain visible,
counted and non-fatal unless `strict: 'true'` is set. NU/MSB infrastructure
warnings without exact swap attribution fail closed; `MSB3277` remains fatal.

The guard covers **only the build step**. The subsequent test command runs
independently, so any build it starts uses its own warning policy and may fail
on sibling warnings. To reuse the guarded test build, configure:

```yaml
guard-project: tests/Acme.Tests.csproj
test: dotnet test tests/Acme.Tests.csproj --no-build
```

Guard the same test project, configuration and target frameworks that will run;
the guarded build must produce its test outputs first. `--no-build` cannot run
outputs that were never built. No guard policy is added to the test phase.


Outputs: `commits` is a JSON object mapping prefixes to resolved commits;
`test-result` is the test phase result, empty if it was not reached. Console
output carries test results. Use your test runner's report options and artifact
steps for structured reports. The job summary includes commits and phase results.
Exit codes: setup 10, wire 11, build 12, test 13.

Use read-only contents permission and `pull_request`. No write token is needed;
private upstreams may be unavailable to fork PRs. Never execute PR code under
privileged `pull_request_target` credentials. The checkout stays untouched;
temporary stackspaces are removed after execution. No pushing occurs.

The `rig-sha256` input accepts the expected archive SHA256 pinned in your
workflow. Set it to the platform-specific release archive hash obtained through
an independently trusted channel. The release checksums alone detect corruption
and a tampered mirror; they do not protect against a compromised release that
replaces both archive and checksums. A workflow-pinned checksum adds that boundary
as long as the workflow and action implementation remain trusted.

`allowed-hosts` defaults to `github.com`, a narrow useful default for GitHub
Actions. It is a comma-separated exact host list (include ports when needed).
Both `fork` and `upstream` are checked by the verb before imports. Explicitly list
enterprise hosts; do not derive this input from PR data. This is a manifest
policy, not a network sandbox for arbitrary build commands.

Caches include verified josh binaries, josh's fetched/filtered host object store,
and filtered member bundles keyed by source URL, cursor, prefix filter and engine
version. An outer manifest-keyed cache has a version/platform restore prefix:
when one cursor moves, restored bundles still serve unchanged members. `init`
consumes bundles directly, skipping the proxy fetch and filtering on a hit.
Cache-write failures warn on stderr and do not fail an already successful import;
the temporary cache ref is cleaned up on every write path.
Only successful bundle consumption counts in the summary's member hit rate;
binary/package restore matches do not establish saved work and are not counted.

NuGet, npm, Go module and Cargo download caches use the manifest (every sibling's
immutable cursor transitively identifies its lockfiles and pins) plus checkout
lockfiles/pins. Restore prefixes allow partial download reuse. These are download
caches, never build-output caches; tools still validate their own packages.
Tip mode is inherently mutable and its package cache is only a download seed.

Each resolved commit and `:prefix=` filter is included in the job summary and
`stack-provenance-<job>-<matrix-index>-<attempt>` JSON artifact. LFS is deliberately unsupported: any
member `.gitattributes` containing `filter=lfs` fails clearly, even if the rule
is currently unused. Fetch LFS content in a separately designed workflow first.

The fused repository has no remotes, `push.default=nothing` and a rejecting
pre-push hook. The verb refuses configured destinations. These prevent accidental
publication; arbitrary untrusted build scripts can bypass local Git safeguards,
so withholding credentials and write permissions is still essential.

No separate josh step is needed: stack init installs the manifest/default pinned
josh on demand before imports. Fresh-runner installation was reasoned through
the existing installer call path, not exercised against hosted downloads.

Use ordinary `pull_request`, `permissions: {contents: read}`, checkout
`persist-credentials: false`, and no secrets. Fork jobs cannot access private
upstreams without credentials and should fail clearly. Never run this action on
untrusted PR code under `pull_request_target`: that event runs with base-repository
privileges. If privileged PR metadata handling is ever needed, use a separate
metadata-only job/workflow that never checks out or executes PR code, validates
all input, and never consumes executable artifacts from the untrusted build.

Pin all external actions by full reviewed commit SHA, including the eventual
`rigsmith/stack-action@<full-reviewed-commit-sha>` after publication. Tags are
mutable; the intended separate action repository is not published yet. For now
copy this directory from a reviewed rigsmith commit and use its local path.

### History cache trust

A restored filtered bundle is trusted history. Git checks object integrity,
but neither the bundle filename nor its Git object hashes prove that its
filtered commit corresponds to the upstream cursor in the manifest. No local
cursor-to-filtered-history verification is performed. The substituted-bundle
unit fixture deliberately demonstrates that different valid history under the
same cache key is accepted. An import record carried inside that same bundle
would not provide an independent trust anchor.

The trust boundary is GitHub's cache scoping: PR cache writes are scoped to that
PR ref and cannot poison another ref's cache; poisoning a default-branch cache
requires the ability to write caches in that branch's execution context. A
compromised default-branch workflow or trusted cache writer can therefore
substitute sibling sources. Cache keys and cursor provenance are not signatures.
See [GitHub cache access restrictions](https://docs.github.com/en/actions/using-workflows/caching-dependencies-to-speed-up-workflows#restrictions-for-accessing-a-cache).

`cache-history: 'false'` disables restored member/host history and engine caches
in the action and turns bundle consumption/writes off in the verb. Use it for
release-grade runs that should not trust cached filtered history; the equivalent
CLI flag is `--cache-history=false`. Existing host object caches on self-hosted
runners remain josh's responsibility; use a clean runner for that trust boundary.
`cache-packages` independently controls package-download cache restore/save and
defaults to `'true'`, like `cache-history`. Set both inputs to `'false'` for a
job that must not use this action's shared caches. Package caches contain only
downloads, not build outputs. Fork PRs override both defaults as described below.

The summary and JSON provenance record each member's `source` (`cache` or
`fetched`) and the actual `filteredCommit` imported. The manifest `commit` is a
requested upstream cursor, not an independently verified claim about cached
content. The selected PR member always says `fetched` because its tree comes
from checkout HEAD after import. Its `filteredCommit` describes the preceding
import, not the replacement checkout commit. For cached siblings, retain the
actual bundle/filtered history as well as the provenance to reproduce a build;
the cursor alone is insufficient if the cache was substituted.

Provenance artifact names include the job, matrix `strategy.job-index`, and
`github.run_attempt`, avoiding collisions between matrix legs and reruns.
GitHub documents strategy context access from job steps in its
[contexts reference](https://docs.github.com/en/actions/reference/workflows-and-actions/contexts#strategy-context).
The runner's [composite handler](https://github.com/actions/runner/blob/main/src/Runner.Worker/Handlers/CompositeActionHandler.cs)
creates embedded children; [child-context creation](https://github.com/actions/runner/blob/main/src/Runner.Worker/ExecutionContext.cs)
copies parent expression values. This source inspection supports strategy context
availability inside the composite. Actual matrix uploads remain unverified on
GitHub-hosted runners; local tests do not establish that integration.

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

### Fork PR cache policy

When a pull request's `github.event.pull_request.head.repo.full_name` differs
from `github.repository`, this action skips both combined `actions/cache` steps:
it neither restores nor saves history/engine caches or package caches. It also
leaves `RIG_STACK_CACHE` empty, unsets it before invoking rig, and passes
`--cache-history=false`, so the verb cannot write new filtered bundles. The
summary says both caches were skipped because this is a fork PR, rather than
presenting a hit rate. Push, schedule and other events without a pull-request
payload retain input-controlled caching. `cache-history` and `cache-packages`
each accept exactly `'true'` or `'false'` and default to `'true'` for eligible jobs.

Same-repository PRs still read base-branch caches, by GitHub's design. Cache
write scoping protects refs from some poisoning scenarios; it does not provide
confidentiality. These guards control this action's cache operations, not the
GitHub cache service's access policy: other steps or untrusted code that explicitly
request accessible caches are outside this guard. Do not treat a cache as secret
storage. Keep private workflows/material in a separate trust boundary when
untrusted code could request those caches. Use clean runners: disabling these
steps does not erase pre-existing files on self-hosted runners.

The action conditions and shell behavior are covered by local YAML-parsing and
fork/non-fork script tests. Actual GitHub fork-PR restore/save behavior remains
unverified locally; validate it on hosted runners before relying on that setup.
