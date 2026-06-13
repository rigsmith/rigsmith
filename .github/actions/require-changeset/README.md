# rigsmith require-changeset action

The **changeset gate** — the in-Action equivalent of the
[`@changesets` bot](https://github.com/apps/changeset-bot), with no hosted service to run. On every
pull request it:

1. checks whether the PR **adds a changeset** (`.changeset/<id>.md`, excluding `README.md`),
2. **upserts a sticky comment** — a nag with instructions when one is missing, a ✅ when present, and
3. **fails the check** when none is found, so a *required status check* blocks the merge until a
   changeset is added (or the PR is labelled to opt out).

That covers the same job as the official bot — comment **and** block — without registering a GitHub
App or hosting a webhook server.

## Inputs

| Input        | Default          | Description                                                           |
| ------------ | ---------------- | -------------------------------------------------------------------- |
| `cwd`        | `.`              | Working directory containing the `.changeset` folder.                |
| `skipLabel`  | `skip-changeset` | A PR carrying this label waives the requirement (gate passes green). |
| `comment`    | `true`           | Upsert the sticky comment. `false` ⇒ check-only, no comment.         |
| `addCommand` | `changerig add`  | Command shown in the nag comment for creating a changeset.           |

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

## How detection works

It diffs the PR range (`base..head`) for **added or modified** files matching
`.changeset/*.md` other than `README.md`. The base commit is fetched if the checkout was shallow;
if the range can't be resolved it falls back to the merge-base against the PR's base branch.
