# Stackspace CI action

Intended final home: `rigsmith/stack-action@v1`, which is not published yet.
Until publication, copy this entire directory into your member repository and
use the local path. It has no relative dependencies outside this directory.

```yaml
- uses: actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683 # v4
- uses: ./.github/actions/stack
  with:
    rig-version: vX.Y.Z # select an exact release containing stack ci
    manifest: stack/rig.stack.jsonc
    member: app
    test: rig test
```

Keep your plain-clone build matrix alongside this job. Install required language
SDKs as usual. The checkout may be shallow. `rig-version` is required and must
be an exact release tag; installation downloads its release asset, falling
back to `go install` at that pin (which requires Go). Josh uses the manifest's
pin or rig's built-in pin and is installed by rig before importing members.

`manifest` is required.
`member` defaults to matching origin URL. `upstream-tip: 'true'` selects tips
instead of committed sibling cursors. `build` and `test` default to `rig build`
and `rig test` and run from the member prefix with bash; empty skips a command.

Outputs: `commits` is a JSON object mapping prefixes to resolved commits;
`test-result` is the test phase result, empty if it was not reached. Console
output carries test results. Use your test runner's report options and artifact
steps for structured reports. The job summary includes commits and phase results.
Exit codes: setup 10, wire 11, build 12, test 13.

Use read-only contents permission and `pull_request`. No write token is needed;
private upstreams may be unavailable to fork PRs. Never execute PR code under
privileged `pull_request_target` credentials. The checkout stays untouched;
temporary stackspaces are removed after execution. No pushing occurs.

Caching, verified checksums and other CI modes are deferred.

No separate josh step is needed: stack init installs the manifest/default pinned
josh on demand before imports. Fresh-runner installation was reasoned through
the existing installer call path, not exercised against hosted downloads.
