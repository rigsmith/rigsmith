# claudeRig

Sync your Claude Code setup across machines — config, skills, and session
history — to your own **private** git repo, and restore it on any computer with
paths corrected across OSes and secrets never leaked. Pick up where you left off
on a different machine.

The fourth rig: a single statically-linked Go binary, zero runtime deps,
installable by `curl | sh` / Homebrew / winget / Scoop on macOS, Linux, and
Windows.

```sh
clauderig init                 # wizard: create/choose a PRIVATE repo, machine name, hooks
clauderig queue                # v2: explicit saved requests, foreground worker, manual sync, status/retry/drain
clauderig queue prepare --hook --output hook-request.json < hook.json  # save intent for later enqueue
clauderig queue hook < hook.json  # v2: save and admit a new hook event
clauderig queue recover-hooks   # retry its inbox using the original account
clauderig queue sync --flush   # v2: manual sync, confirm covered queue requests
clauderig sync                 # snapshot → redact secrets → rewrite paths → commit → push
clauderig restore              # pull → rewrite slugs for this OS → merge (keeps local secrets)
clauderig restore --dir /tmp/x # restore the CLI payload into a folder (inspect, don't touch ~/.claude)
clauderig status               # remote reachability, last sync, per-root counts, hooks
clauderig reroot <id> ~/Git/p  # re-file a session under the directory it really belongs to
clauderig repo                 # what the sync repo costs: size, files, commits, history ratio
clauderig repo gc              # repack: reclaims space, keeps every commit — try this first
clauderig repo prune --before 2026-08-01  # fold everything before a date into one commit
clauderig recent                  # sessions you actually worked on, newest first
clauderig search "auth refactor"  # find a session by title/content, with a resume command
clauderig pull                 # fetch latest into the staging repo (SessionStart hook target)
clauderig account list         # show stored Claude Code logins (alias: ls / status)
clauderig account run me@x.com # launch Claude Code as another account, isolated session
clauderig mcp add ctx7 npx -y @upstash/context7-mcp   # manage MCP servers (list/add/remove/enable)
clauderig desktop open work    # a Claude Desktop window per account, each its own profile
clauderig desktop prune --vm   # reclaim the Cowork VM image + caches; keeps login and history
rig worktree new feat/x        # sibling worktree + review window; never moves this session
clauderig doctor               # health-check env + sync + worktree discipline + ignored settings (--fix repairs what it can; ignored settings are advisory)
clauderig hooks install        # SessionStart→pull, Stop→sync, SessionEnd→sync --flush
clauderig ui                   # interactive dashboard
```

## What it does

- **Cross-OS path correction.** A session captured at `/Users/john/Git/x` resumes
  at `C:\Users\John\Git\x`. Project directory slugs and path values inside config
  are re-derived for the target machine (`core/pathmap`), including paths
  embedded in Desktop’s stored permission reasons.
- **Secret redaction and publication checks.** Secret-bearing config fields are
  stripped, and complete staged-text scanning refuses recognized credentials
  in transcripts and other staged files, regardless of size. Set
  `redactTranscripts` to true to scrub supported signatures from the staged copy of a
  conversation — the transcript, the tool results beside it, and memory notes.
  Live files are never edited. These checks do not sanitize
  existing Git history or identify every possible secret.
- **Transcript chunking.** On for new configurations; existing configs without
  the key use auto and follow the repository. Large backups use reusable 4 MiB chunks
  while restore writes native JSONL. Existing backups migrate on the next sync.
  Upgrade every participating client before enabling it. See
  [enabling chunking](./commands#transcript-chunking).
- **Private repo, no exceptions.** The remote must be a GitHub or GitLab repo
  verified private with `gh`/`glab` or the matching provider token. Git uses
  your configured credential helpers.
- **Allowlist, default-deny.** Only curated files sync; the ~12 GB Desktop cache
  tree is pruned, never descended.
- **Bounded repo, unbounded memory.** 90-day retention on transcripts + a
  size-based history squash — but every session sync ever staged keeps a
  permanent row in the ledger, so an aged-out chat is still findable by title,
  project and date.
  Project memory is exempt — it's durable state, not a dated record, so it never
  ages out of the sync.
- **Find your sessions.** `clauderig recent` lists them newest first — dated by
  what each transcript *says*, not by a file mtime that a restore or a repo
  checkout rewrites — and `clauderig search` locates one by title or content
  across live and synced history. Both hand you a `claude --resume` command. See
  [Commands](./commands#finding-a-session).
- **Worktree discipline.** A guard hook plus `rig worktree` make branches +
  PRs the default for Claude Code and keep a session from scrambling its chat
  history by moving the working directory. See [Commands](./commands#worktree-discipline).
- **Multiple accounts.** `clauderig account` captures, lists, and switches
  between Claude Code logins, and can `run` a session as another account in an
  isolated environment without touching your machine-wide login.
- **MCP server management.** `clauderig mcp` adds, removes, lists, and toggles
  MCP servers across user / project / local scopes — args-driven or via an
  interactive screen (mirrors `claude mcp`).

## Install

```sh
curl -fsSL https://rigsmith.sh/clauderig | sh    # once the release exists
# or build from source (single Go module):
go build -o clauderig ./cmd/clauderig
```

Requires `git` with credentials for the remote. Privacy verification uses
`gh` for GitHub or `glab` for GitLab. Without the matching CLI, set
`GITHUB_TOKEN`/`GH_TOKEN` or `GITLAB_TOKEN`/`GL_TOKEN`, respectively.
For GitLab/token-only setup, use `clauderig config set remote <url>` or
`clauderig init --yes --remote <url>`; the interactive wizard and doctor still
have older `gh`-availability gates. Queue commands verify privacy independently.

- [All commands →](./commands)
