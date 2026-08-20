---
# Daily review of what changed in Claude Code (matins.news) for anything that breaks the
# assumptions clauderig makes about Claude Code's own configuration.
on:
  schedule:
    # 10:00 UTC = 06:00 in New York on EDT, 05:00 on EST — Actions cron is UTC-only and does
    # not follow DST, so it drifts an hour in winter rather than tracking the clock. Either
    # way it lands after matins publishes, which it does at 08:00 UTC every day.
    - cron: "0 10 * * *"
  workflow_dispatch:
    inputs:
      since:
        description: "Oldest brief date to review (YYYY-MM-DD). Blank = resume from the newest existing [matins] issue."
        required: false
      until:
        description: "Newest brief date to review (YYYY-MM-DD). Blank = today."
        required: false
      max-briefs:
        description: "Briefs to review per run. Backfill more than this with .github/scripts/matins-backfill.sh, which dispatches one run per chunk."
        required: false
        default: "14"

permissions:
  contents: read
  issues: read

# rigsmith/rigsmith is org-owned, so `copilot-requests: write` is theoretically available —
# but it also needs Copilot seats and the "Allow use of Copilot CLI billed to the
# organization" policy, and the org currently reports zero seats with CLI unconfigured. Until
# that changes this authenticates with the COPILOT_GITHUB_TOKEN secret: a fine-grained PAT
# owned by a user account with Account permissions -> Copilot Requests: Read, billed to that
# user's Copilot plan.
engine: copilot

network: defaults

tools:
  github:
    toolsets: [issues]

# `steps:`, not `pre-steps:` — pre-steps are emitted BEFORE the repository checkout, so the
# fetch script would not exist yet and the checkout would land on top of its output.
steps:
  - name: Work out the window and fetch the briefs
    env:
      GH_TOKEN: ${{ github.token }}
      SINCE_INPUT: ${{ inputs.since }}
      UNTIL_INPUT: ${{ inputs.until }}
      MAX_BRIEFS_INPUT: ${{ inputs.max-briefs }}
    run: |
      set -euo pipefail
      SINCE="${SINCE_INPUT:-}"
      if [ -z "$SINCE" ]; then
        # Resume from the newest brief date already written up as an issue. Days that
        # produced no findings produce no issue, so they can be re-read on a later run —
        # a 0-change day costs nothing and this needs no state file or write permission.
        LAST=$(gh issue list --label matins-news --state all --limit 200 --json title \
                 --jq '.[].title' | grep -oE '[0-9]{4}-[0-9]{2}-[0-9]{2}' | sort | tail -1 || true)
        if [ -n "$LAST" ]; then
          SINCE=$(date -u -d "$LAST +1 day" +%F)
        else
          SINCE=$(date -u -d '2 days ago' +%F)
        fi
      fi
      echo "reviewing briefs from $SINCE to ${UNTIL_INPUT:-today}"
      python3 .github/scripts/matins-fetch.py \
        --since "$SINCE" \
        --until "${UNTIL_INPUT:-}" \
        --max-briefs "${MAX_BRIEFS_INPUT:-14}" \
        --out matins-briefs.txt

safe-outputs:
  create-issue:
    title-prefix: "[matins] "
    labels: [matins-news, claude-code]
    max: 5

timeout-minutes: 20
---

# Matins watch — Claude Code changes that break clauderig's assumptions

`matins-briefs.txt` in the repository root holds one or more daily briefs from
[matins.news](https://matins.news/), which distils each Claude Code release. Each brief is
delimited by `===== BRIEF <date> — <headline> =====`. It was fetched for you; do not try to
reach the network yourself. A `# BACKFILL-CONTINUES-FROM:` line in the header means the
window was chunked and another run covers the rest — not your concern.

## Why this matters here

**clauderig reads, rewrites and syncs Claude Code's own configuration** across machines. It
does not drive the CLI — it edits what the CLI reads. So the releases that hurt are the ones
that move a file, change which tier wins, or change the shape of something clauderig writes.
Those break quietly: the sync keeps succeeding and silently produces config Claude Code no
longer honours.

The assumptions, and where each one lives:

- **Settings tiers and precedence** — `internal/clauderig/settings/settings.go` hard-codes
  three scopes (`~/.claude/settings.json`, `<repo>/.claude/settings.json`,
  `<repo>/.claude/settings.local.json`) and their precedence order. A new tier, a moved path,
  a changed merge rule or a setting that stops being honoured at a given scope all land here.
- **Hooks** — `internal/clauderig/hooks/hooks.go`. Event names, matcher syntax, payload shape.
- **MCP servers** — `internal/clauderig/mcp/mcp.go`. Config location and schema.
- **CLAUDE.md** — `internal/clauderig/claudemd/claudemd.go`. Include semantics, discovery order.
- **Permission rules** — `internal/clauderig/allowlist/allowlist.go` and `defaults.go`. Rule
  syntax, tool names, what a rule is allowed to match.
- **Accounts and credentials** — `internal/clauderig/account/` (`oauthaccount.go`,
  `sessionstore.go`, `livestore_darwin.go`, `claudelock.go`). Where tokens live, keychain
  layout, how a live session is detected.
- **The guard** — `internal/clauderig/guard/guard.go`.
- **What travels between machines** — `internal/clauderig/manifest/`, `dirmap/`, `devices/`,
  `session/`, and the command surface in `internal/clauderig/commands/`.

Read the files before claiming anything is affected. A finding that names a real symbol or
line is worth five that describe the brief back to me.

Second, smaller bucket: a Claude Code capability clauderig should **expose or adopt** — a new
settings key worth syncing, a new config surface worth managing. Say concretely where it
would land.

Ignore anything that lives purely inside Claude Code's interactive TUI, its model or pricing
news, or its subprocess/stream protocol — clauderig never drives the CLI.

## What to do

1. Read `matins-briefs.txt`. If it says `NO-NEW-BRIEFS`, stop — create nothing.
2. Before writing anything up, list existing issues labelled `matins-news` and skip findings
   already covered by an open one. Repeating a finding is worse than missing a day.
3. Read the Go files above for anything you intend to report, and check whether clauderig is
   actually exposed. Most changes are not. **No findings is the normal, correct outcome for
   most days** — create nothing and stop rather than padding.
4. Create at most one issue per distinct finding, highest-consequence first.

## Issue format

Title: the brief date, then the change, e.g. `2026-08-18 — settings.local.json no longer
honours sandbox.ripgrep`. (The `[matins] ` prefix is added for you; the date is how the next
run knows where it left off, so it must be there and must be the date of the brief.)

Body:

- **What changed** — quote the brief line, and link to `https://matins.news/daily/<date>/`.
- **What it breaks** — the concrete failure, naming the file and symbol you checked. If it is
  the adopt bucket instead, say so and say where it would land.
- **Suggested action** — what you would change, or what to verify first if you are unsure.
- **Confidence** — high / medium / low, and what would settle it. Say plainly when you could
  not confirm clauderig's side from the code.
