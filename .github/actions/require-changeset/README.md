# rigsmith require-changeset action

The **release-intent gate** — the in-Action equivalent of the
[`@changesets` bot](https://github.com/apps/changeset-bot), with no hosted service to run. On every
pull request it:

1. checks whether the PR carries **release intent**,
2. **upserts a sticky comment** — a nag with instructions when it's missing, a ✅ when present, and
3. **fails the check** when none is found, so a *required status check* blocks the merge until intent
   is added (or the PR is labelled to opt out).

That covers the same job as the official bot — comment **and** block — without registering a GitHub
App or hosting a webhook server.

What counts as "release intent" depends on the `mode` input:

- **`changeset`** (default): the PR **adds a changeset** (`.changeset/<id>.md`, excluding `README.md`).
- **`commit`**: the PR **title is a conventional commit** (`feat: …`, `fix(scope): …`, `feat!: …`).
  Use this with commit-based versioning. On a squash-merge repo
  the PR title becomes the squash subject that lands on the base branch and drives the next release,
  so that is exactly what the gate validates.

## Inputs

| Input        | Default          | Description                                                                       |
| ------------ | ---------------- | -------------------------------------------------------------------------------- |
| `mode`       | `changeset`      | `changeset` (added `.changeset/*.md`) or `commit` (conventional PR title).        |
| `cwd`        | `.`              | Working directory containing the `.changeset` folder.                            |
| `skipLabel`  | `skip-changeset` | A PR carrying this label waives the requirement (gate passes green).             |
| `comment`    | `true`           | Upsert the sticky comment. `false` ⇒ check-only, no comment.                     |
| `addCommand` | `changerig add`  | Command shown in the **changeset-mode** nag comment for creating a changeset.    |

## Usage

```yaml
name: Changeset
on:
  pull_request:
    types: [opened, synchronize, reopened, labeled, unlabeled]

permissions:
  contents: read
  pull-requests: write   # post/update the sticky comment

jobs:
  require-changeset:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0   # need the base commit to diff the PR range
      - uses: rigsmith/rigsmith/.github/actions/require-changeset@main
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

Then make **require-changeset** a required status check in branch protection (Settings → Branches)
so a missing changeset actually blocks the merge.

## Opting a PR out

Add the `skip-changeset` label (or whatever you set `skipLabel` to). Docs-only, CI, and internal
refactors that need no release entry pass the gate with the label on. Listing `labeled`/`unlabeled`
in the trigger means the check re-runs the moment the label is toggled.

## Fork PRs

A normal `pull_request` workflow on a PR **from a fork** gets a read-only `GITHUB_TOKEN`, so the
sticky comment can't be posted there — the action logs a warning and the **failing check still
blocks the merge**. If you want the comment on fork PRs too, run this action from a
`pull_request_target` workflow instead (it runs in the base repo's context with a write token);
note the usual `pull_request_target` security caveats — this action only reads the PR's changed-file
list and never executes PR code, but anything else in that workflow must stay just as careful.

## Commit mode

For repositories using commit-based versioning, set `mode: commit`:

```yaml
      - uses: rigsmith/rigsmith/.github/actions/require-changeset@main
        with:
          mode: commit
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

The gate then passes when the **PR title** parses as a conventional commit and nags/blocks otherwise.
The `skip-changeset` label still waives it. `checkout` does not need `fetch-depth: 0` in this mode —
only the PR title (from the event payload) is inspected, not the git range.

## How detection works

- **changeset mode** diffs the PR range (`base..head`) for **added or modified** files matching
  `.changeset/*.md` other than `README.md`. The base commit is fetched if the checkout was shallow;
  if the range can't be resolved it falls back to the merge-base against the PR's base branch.
- **commit mode** matches the PR title against the conventional-commit header grammar
  `type(scope)?!?: description` (case-insensitive type). No git history is consulted.
