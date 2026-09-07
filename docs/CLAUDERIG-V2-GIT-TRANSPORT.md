# V2 retained Git transport

`commitartifact.NewGitTransport` supplies the first concrete transport for the
retained publisher. It implements the shared `Transport` contract and Claude's
`ArtifactTransport` destination interface. It can publish through HTTPS, SSH with explicit identity/host-trust files, or an
absolute local path, with ordinary fast-forward pushes and fresh confirmation
performed by `Publish`. This remains internal code: commands, hooks and installed
workers do not call it. Existing synchronous Git and credential handling are
unchanged, so this step has no end-user changeset.

## Destination and authentication

Construction copies one exact remote string, branch, optional HTTP Basic
credential and optional CA bundle path. The destination interface returns those
same bound values. Relative local paths, remote aliases, remote helpers, HTTP
URL userinfo, queries, fragments, control characters and unsafe branch/ref syntax
are refused. SSH destinations require an explicit user and SSH options as below. Plain HTTP is limited to literal loopback IP addresses for local
integrations; production HTTP destinations must use HTTPS. Custom CA bundles are
explicit and TLS verification cannot be disabled.

Credentials are provided by the caller, separately from the captured Claude
account identity. This layer does not invoke credential helpers, select a vendor
account, prompt, or load a worker's credential store. A later composition layer
must resolve and refresh repository authentication explicitly. Passwords are
copied into a URL-scoped Authorization header in the child's environment. They
are not written to argv, Git configuration, archives, queue state or error text.
Environment delivery is not a claim of protection from the same OS user or a
privileged process inspecting child environments.

Each command disables inherited Git overrides, global/system configuration,
credential helpers, askpass, proxies, redirects, submodule recursion, hooks,
signing and automatic maintenance. The child's home/config directories point at
the owned private repository so curl cannot silently use the worker's netrc.
Only the selected transport protocol is allowed. TLS defaults remain enabled,
with an optional caller-supplied CA file. Git stderr is discarded and stdout is
bounded at 1 MiB; errors expose a stable transport error and exit code rather than
raw diagnostics, URLs or authorization values.

This uses Git's documented [URL-scoped HTTP settings and redirect controls](https://git-scm.com/docs/git-config).
The implementation intentionally rejects redirects instead of following one with
a bound credential or changing the destination. Credential-helper selection and
agent/keychain discovery still need explicit noninteractive contracts before
queued hooks can support those installations.

## SSH identity and host trust

`SSHOptions` supplies three absolute paths: a trusted OpenSSH executable, one
identity file and one known-hosts file. Construction copies their selection into
the transport. Callers own these files and must keep them stable through an
operation; path binding is not a snapshot of their contents. Missing, unreadable,
wrong or encrypted identities fail authentication without an alternate identity
or a prompt. The identity is supplied through `IdentityFile` configuration rather
than `-i`, so even a missing path suppresses default identity-file selection.
No private-key bytes are read by the transport, copied into queue
state/artifacts, or embedded in command arguments, environment or errors. The
selected file paths are visible to the child process.

Supported destinations include `ssh://git@example.com:2222/srv/repo.git` and
`git@example.com:acme/repo.git`. The original string remains the destination
binding and the argument to Git: scp-style relative paths retain their remote
home-directory semantics; SSH URL paths remain absolute. URLs may specify a port
from 1 through 65535. Scp-style destinations use the normal SSH port. Users,
hostnames and repository paths use a conservative ASCII grammar. Passwords,
percent escapes, query/fragment text, shell syntax, tilde paths and dot traversal
are refused. IPv6 literals must be bracketed in the destination. Alias expansion
through SSH configuration is not supported.

The command uses [OpenSSH's `-F none`](https://man.openbsd.org/ssh) to ignore user
and system SSH configuration. [Authentication and host-key options](https://man.openbsd.org/ssh_config)
select public-key authentication, one identity, and strict checking against the
supplied known-hosts file. Unknown/changed hosts fail before running remote Git.
Global host files, DNS host-key trust, host-key updates, certificate sidecar
selection, identity agents, password/keyboard prompts, forwarding, proxy/jump
commands, local commands, and connection sharing are disabled. The transport does
not accept host keys on first use or add keys to an agent.

Inherited SSH agent/askpass/provider environment is removed, along with the
existing Git override isolation. Git receives `GIT_SSH_VARIANT=ssh` and a generated
[`GIT_SSH_COMMAND`](https://git-scm.com/docs/git#Documentation/git.txt-GIT_SSH_COMMAND).
Every argument is shell quoted, including executable and identity paths containing
spaces or apostrophes. The known-hosts path is also quoted for OpenSSH's list
parser. Paths with control characters, quotes that would alter that parser,
percent tokens or environment/leading-tilde expansion are refused. Literal
tildes inside absolute paths remain valid, including Windows short directory
names such as `C:/Users/RUNNER~1`. This relies on Git's
shell convention, including Git for Windows; Plink/TortoisePlink are not accepted
as alternate SSH implementations. Unsupported OpenSSH options fail closed.

The same owned process runner, output bound, ordinary push and fresh confirmation
rules apply to SSH. Cancelling a connection terminates the local SSH process tree;
it does not guarantee that a remote server has stopped processing a request.
Uncertain pushes still require the publisher's fresh ancestry observation.

This first SSH path deliberately requires explicit files. Agent-backed encrypted
keys, OS keychains, Git credential helpers, host aliases, ProxyJump and custom SSH
configuration remain composition work. Repository authentication stays separate
from Claude/Codex session attribution. Existing synchronous SSH behavior is
unchanged, and no queued command or hook is activated.

## Fetch and push

Methods require the fresh private bare repository created by `Publish`, owned by
the caller and containing no caller-added Git configuration. This is a trusted
internal contract, not a sandbox for arbitrary repositories. A canonical checkout
is refused, as is overlap between a local remote and the supplied workspace.
Calls sharing a workspace must be serialized by its owner.

Fetch clears only the supplied `refs/rig/publication-…` ref and advertises the
exact configured branch with `ls-remote --exit-code --refs`. Only Git's documented
[exit status 2 for no matching refs](https://git-scm.com/docs/git-ls-remote), with
successful process cleanup and no context cancellation, means an absent branch.
An unreachable repository, authentication failure, malformed response, cleanup
failure or failed fetch is an error. A missing local remote is also an error.

The subsequent fetch imports only that branch into the supplied private ref,
without tags, FETCH_HEAD, recursive submodules or maintenance. It returns the
actual fetched commit because the branch may advance after advertisement.
`Publish` then independently checks the ref/SHA, object integrity and complete
history. Other private refs and configuration stay unchanged.

Push requires an exact commit object and sends it to the bound branch using a
normal, non-forced refspec. It does not infer a push remote, invoke a pre-push hook,
push tags or update canonical staging. A non-fast-forward attempt is rejected.
Only the publisher's subsequent fresh fetch and tree validation can establish
success for a queued batch; the push command alone is not confirmation.

The caller supplies the operation context and deadline. This transport does not
persist retry state, acknowledge generations or implement worker timeout policy.

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

Synthetic tests use real local bare remotes and a TLS Git smart-HTTP server with
an explicit fixture CA and credential. SHA-1 and SHA-256 cases publish newer local
and remote histories, validate confirmation and replay, and exercise ordinary push
rejection. Additional tests cover absent branches versus unreachable/auth failures,
protected refs, configuration/FETCH_HEAD preservation, ambient Git/netrc isolation,
credential copying, redirect refusal, untrusted TLS, cancellation and invalid
endpoints. A native Claude service test uses the concrete local transport.

Process tests start real helper trees, including a descendant retaining output
pipes after its parent exits. A write attempted only after Run returns detects
leaked helpers without depending on the race detector's delayed exit behavior.
They cover both normal completion and cancellation, direct exit status, startup
failure and pre-cancellation. Native Linux/macOS/Windows CI remains the platform
gate, alongside the unchanged six-scenario Claude compatibility baseline.

Additional retained-command tests put a synthetic Git executable on PATH and
start real descendant processes. They check cleanup after ordinary exit,
cancellation, output overflow and malformed stream rejection, including helpers
that hold stdout open. A post-return marker detects further helper execution;
large bidirectional transfers verify concurrent pipe draining and exact bytes.
The fixture ignores SIGPIPE so output-rejection checks also cover helpers that
keep running after a broken pipe. Follow-up HEAD probe tests preserve overflow
and cancellation failures from both symbolic-ref and show-ref.
Missing-executable and semantic-exit cases exercise startup closure and rejection
of exit statuses joined with cleanup errors. These fixtures run on all three CI
platforms and never access a real user's repository or vendor data.

SSH tests use real Git/OpenSSH against an unprivileged loopback SSH server with
fresh synthetic keys. They exercise SHA-1/SHA-256 publication and replay, newer
local/remote histories, absent-branch ref isolation, option copying, paths with
spaces/apostrophes, poisoned inherited settings, wrong/missing/encrypted keys,
unknown/changed/missing host trust, wrong users and cancellation during a blocked
handshake. An OpenSSH `-G` check verifies that missing selected files do not
enable default identities or agents. Trust/key files and private Git
configuration remain unchanged. The Go
SSH dependency is used only by tests; production continues to use OpenSSH. CI must
run these fixtures natively on Linux, macOS and Windows.
