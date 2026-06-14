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

rigsmith itself currently releases its binaries via GoReleaser (see
[`.goreleaser.yaml`](../.goreleaser.yaml)), not via `shiprig publish`, and does not keep a
`.changeset/` folder — so these actions are **not** wired into this repo's own CI. They are built
here to be consumed by polyglot repos that use changesets. Copy the
[example workflows](../examples/github-workflows) into such a repo to adopt them.
