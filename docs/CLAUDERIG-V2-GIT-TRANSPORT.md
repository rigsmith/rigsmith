# V2 retained publication execution

The retained publisher keeps its shared `Transport` interface, exact destination
binding, ordinary fast-forward publication and fresh remote confirmation.
[#326](https://github.com/rigsmith/rigsmith/pull/326) removes the custom HTTPS/SSH
transports introduced in #323/#325 and drops the proposed generic credential
helper. Queue reliability does not require replacing users' existing Git
credentials or adding rig-specific key, host-trust or keychain configuration.

## Network integration direction

Use the existing Git authentication setup, with `gh` as the primary GitHub path.
Claude's existing `gh` repository creation/private-repository verification and
Git push behavior remain the reference. Retained-publisher integration must
exercise that path while preserving exact destination/commit selection, retry
confirmation and owned subprocess cleanup. No replacement credential provider
or automatic SSH-agent/keychain discovery is a prerequisite for the queue.
Existing GitLab and SSH users retain the synchronous workflow during development.

This cleanup does not wire a network transport into the retained publisher or
activate queued commands/hooks. The next implementation should connect existing
Git/`gh` authentication to the publication boundary with focused integration
fixtures, then progress through worker recovery and rollout gates in the
[roadmap](CLAUDERIG-SHARED-LAYERS-ROADMAP.md).

## Local publication adapter

`commitartifact.NewGitTransport` now accepts only an absolute local repository
path and branch. Its options contain no credentials or network configuration.
HTTP, HTTPS, SSH, file URLs and remote aliases are refused. This adapter keeps
synthetic publication and Claude service fixtures exercising real Git. The
private Git command environment permits only file transport and disables inherited
Git overrides, hooks and automatic maintenance.

Methods require the fresh private bare repository created by `Publish`, owned by
the caller and containing no caller-added Git configuration. A canonical checkout
is refused, as is overlap between the local destination and publication workspace.
Calls sharing a workspace must be serialized by their owner.

Fetch clears only the supplied `refs/rig/publication-…` ref and advertises the
exact configured branch. Only a clean `ls-remote --exit-code --refs` exit status
of 2 means the branch is absent. Missing repositories, cancellation and cleanup
failures remain errors. Fetch imports the branch without tags, FETCH_HEAD or
maintenance and returns its actual commit SHA for independent publisher checks.
Push sends exactly the candidate commit to the bound branch without forcing,
pushing tags or updating canonical staging. The publisher confirms success by
fetching again and checking ancestry and the observed tree.

## Command ownership and cleanup

The `internal/agentrig/process` runner is used by transport commands and all
other Git commands in `commitartifact`: seed retention, bundle creation/opening,
local HEAD inspection, private merges, object checks, blob materialization and
raw-tree attribute validation. It owns cancellation, helper termination and
direct-child reaping before those calls return normally. Existing synchronous
runners remain unchanged.

On Linux/macOS, commands start in their own process group. The runner waits for
root exit without reaping it, keeping its PID reserved while it signals the
whole group. It then checks that no group member is still executing before reaping
the root and completing output copies. Linux uses bounded, streamed `/proc` stat
reads; macOS uses a process-group sysctl query. Zombies have stopped executing and
may remain for their new parent to reap. Further group signals are disabled before
the root PID can be reused. The runner also handles macOS's EPERM response for a
group containing only zombies, after checking membership.

On Windows, the command starts suspended, joins a private kill-on-close job, and
only then resumes. Assignment/resumption failure terminates the suspended child.
Cancellation and normal completion terminate the job and wait for its active
process count to reach zero before releasing handles. This follows the documented
[job-object membership and lifetime rules](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects).
A job handle stays owned through cleanup, including helpers holding output pipes.

These guarantees assume trusted helpers remain in the inherited Unix group or
Windows job. They are not containment against deliberately escaping helpers.
Cleanup/inspection failures remain failures, never absent-branch or publication
success. Linux requires accessible `/proc` process metadata; unsupported platforms
fail before starting a command.

Abrupt parent death is still a rollout gate. Unix process groups alone do not
terminate when the worker dies. Windows kill-on-close jobs help after association,
but the suspended-create/assign window needs a stronger startup contract for that
guarantee. Worker supervision, store-lease ownership across parent death, ownership of any
future external merge tools, native conflict recovery and draining
must be completed before enabling queued hooks. The next integration work must
not confuse cancellation cleanup with crash recovery.

### Retained command streams and errors

One-shot retained commands, including transport, cancel their owned process tree if the output writer
rejects data or exceeds its bound. They wait for cleanup before returning the
write/capacity error and discard partial control output. Git's exit code is usable
as a semantic result only after clean ownership completion: a joined cleanup or
cancellation failure cannot mean an absent local branch, a negative ancestry
answer, or an ordinary merge conflict. Follow-up HEAD probes preserve their own
startup, cancellation, capacity and cleanup errors rather than replacing them
with the earlier HEAD verification error.

Streamed `cat-file --batch` and `check-attr` responses use an explicitly owned OS
pipe. A concurrent runner observes process exit and cleans up descendants before
closing the parent's writer. That permits EOF even when a helper inherited
stdout, without `Cmd.Wait` prematurely closing the consumer's reader. Requests and
responses remain concurrent and bounded by their existing protocol consumers.
Startup failure closes the writer too. Rejection or early return by a consumer
closes the reader and cancels the command; the call joins cleanup before releasing
its caller's staging lease. Inputs must be finite and consumers must return on
EOF or read failure. Arbitrarily blocking application readers/writers and escaping
helpers are outside this trusted internal contract.

## Validation

Local bare-repository fixtures cover SHA-1 and SHA-256 publication, newer local
and remote histories, confirmation, replay, fast-forward rejection, absent versus
missing destinations, ref/configuration preservation and network-URL rejection.
Claude service tests still exercise the concrete local adapter. The standalone
TLS/SSH servers, synthetic SSH keys, generic helper fixtures and their x/crypto
dependency are removed with the network implementations.

Process and retained-command tests continue to exercise normal exit, cancellation,
output overflow, rejected writers/streams, bidirectional transfers, helper trees
and semantic exit handling on Linux, macOS and Windows. Post-return markers detect
helpers that keep executing after ownership should have ended. The fixed six-case
Claude compatibility baseline and synthetic end-to-end suite remain required.
