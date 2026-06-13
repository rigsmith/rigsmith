#!/usr/bin/env bash
#
# The release-intent gate: the in-Action equivalent of the @changesets bot. On a
# pull request it checks whether the PR carries release intent and:
#   - upserts a sticky comment (nag when missing, ✅ when present), and
#   - fails the check when none is found, so a required status check blocks the
#     merge until intent is added (or the skip label is applied).
#
# Two modes (input `mode`):
#   changeset (default) — intent = the PR adds a `.changeset/*.md` file.
#   commit              — intent = the PR *title* is a conventional commit
#                         (feat/fix/…). This matches commit-based versioning on a
#                         squash-merge repo, where the PR title becomes the squash
#                         subject that lands on the base branch and drives the
#                         next release.
#
# Runs on the `pull_request` event. For PRs from forks the GITHUB_TOKEN is
# read-only, so the sticky comment is skipped there (the failing check still
# blocks); use `pull_request_target` if you need fork comments too.
set -euo pipefail

mode="${INPUT_MODE:-changeset}"
cwd="${INPUT_CWD:-.}"
skip_label="${INPUT_SKIP_LABEL:-skip-changeset}"
want_comment="${INPUT_COMMENT:-true}"
add_cmd="${INPUT_ADD_COMMAND:-changerig add}"
marker="<!-- rigsmith-changeset-gate -->"

cd "${cwd}"

event="${GITHUB_EVENT_PATH:?GITHUB_EVENT_PATH not set — this action runs on pull_request events}"
pr_number="$(jq -r '.pull_request.number // empty' "${event}")"
pr_title="$(jq -r '.pull_request.title // empty' "${event}")"
base_sha="$(jq -r '.pull_request.base.sha // empty' "${event}")"
head_sha="$(jq -r '.pull_request.head.sha // empty' "${event}")"
labels="$(jq -r '[.pull_request.labels[].name] | join("\n")' "${event}" 2>/dev/null || true)"

if [[ -z "${pr_number}" ]]; then
  echo "::error::No pull request in the event payload. Trigger this action on 'pull_request'."
  exit 1
fi

# A PR opts out by carrying the skip label.
skipped="false"
if printf '%s\n' "${labels}" | grep -qxF "${skip_label}"; then
  skipped="true"
fi

# ---- mode-specific detection -----------------------------------------------
# Each mode sets `count` (>0 = release intent present) plus the ✅ and nag
# comment bodies; the comment/exit tail below is shared.
ok_body=""
nag=""

if [[ "${mode}" == "commit" ]]; then
  # A conventional-commit header: type(scope)!: description.
  conv_re='^[a-zA-Z]+(\([^)]*\))?!?: .+'
  if printf '%s' "${pr_title}" | grep -iqE "${conv_re}"; then
    count=1
    echo "PR title is a conventional commit: ${pr_title}"
  else
    count=0
    echo "PR title is not a conventional commit: ${pr_title}"
  fi

  ok_body="✅ **Conventional PR title.** \`${pr_title}\` parses as a conventional commit, so the next release will pick it up. Thanks!"
  read -r -d '' nag <<EOF || true
⚠️ **PR title is not a conventional commit.**

This repository derives releases from conventional commits, and on a squash
merge the **PR title** becomes the commit that lands on the base branch. Rename
this PR so it starts with a conventional type, e.g.:

\`\`\`
feat(scope): add the thing
fix: correct the off-by-one
\`\`\`

Types: \`feat\`, \`fix\`, \`perf\`, \`refactor\`, \`docs\`, \`build\`, \`test\`, \`chore\` (suffix
\`!\` or add a \`BREAKING CHANGE:\` footer for a breaking change).

If this PR genuinely needs no release, add the \`${skip_label}\` label to waive
the requirement.
EOF
else
  # ---- changeset mode: diff the PR range for *added* changeset files. --------
  # base_sha may be absent from a shallow clone, so fetch it; fall back to the
  # merge-base if the range is unknown.
  git fetch --no-tags --depth=1 origin "${base_sha}" >/dev/null 2>&1 || true
  range_base="${base_sha}"
  git cat-file -e "${base_sha}^{commit}" 2>/dev/null || range_base="$(git merge-base HEAD "origin/${GITHUB_BASE_REF:-main}" 2>/dev/null || echo "")"

  changed=""
  if [[ -n "${range_base}" ]]; then
    changed="$(git diff --name-only --diff-filter=AM "${range_base}" "${head_sha:-HEAD}" 2>/dev/null || true)"
  else
    # Last resort: anything currently staged/committed under .changeset.
    changed="$(git ls-files '.changeset/*.md' 2>/dev/null || true)"
  fi

  # A real changeset is .changeset/<id>.md, excluding README.md.
  new_changesets="$(printf '%s\n' "${changed}" | grep -E '^\.changeset/.+\.md$' | grep -vE '(^|/)README\.md$' || true)"
  count="$(printf '%s' "${new_changesets}" | grep -c . || true)"
  if [[ "${count}" -gt 0 ]]; then
    echo "Found ${count} changeset(s) in this PR:"
    printf '%s\n' "${new_changesets}"
  fi

  ok_body="✅ **Changeset detected.** This PR adds a changeset, so the next release will pick it up. Thanks!"
  read -r -d '' nag <<EOF || true
⚠️ **No changeset found in this PR.**

This repository requires a changeset for every change that should appear in a
release. Add one so the next \`relrig version\` knows what to bump:

\`\`\`sh
${add_cmd}
\`\`\`

This writes a small markdown file under \`.changeset/\` describing the affected
packages and bump level. Commit it and push — this check will go green.

If this PR genuinely needs no release (docs, CI, internal refactor), add the
\`${skip_label}\` label to waive the requirement.
EOF
fi

# ---- upsert the sticky comment ---------------------------------------------
upsert_comment() {
  [[ "${want_comment}" != "true" ]] && return 0
  [[ -z "${GITHUB_TOKEN:-}" ]] && { echo "No GITHUB_TOKEN; skipping comment."; return 0; }

  local body="$1"
  body="${marker}"$'\n'"${body}"

  # Find an existing gate comment by its hidden marker.
  local existing
  existing="$(gh api "repos/${GITHUB_REPOSITORY}/issues/${pr_number}/comments" --paginate \
    --jq "map(select(.body | contains(\"${marker}\"))) | .[0].id // empty" 2>/dev/null || true)"

  if [[ -n "${existing}" ]]; then
    gh api -X PATCH "repos/${GITHUB_REPOSITORY}/issues/comments/${existing}" -f body="${body}" >/dev/null 2>&1 \
      || echo "::warning::Could not update the gate comment (token may be read-only on fork PRs)."
  else
    gh api -X POST "repos/${GITHUB_REPOSITORY}/issues/${pr_number}/comments" -f body="${body}" >/dev/null 2>&1 \
      || echo "::warning::Could not post the gate comment (token may be read-only on fork PRs)."
  fi
}

# ---- shared verdict --------------------------------------------------------
if [[ "${count}" -gt 0 ]]; then
  upsert_comment "${ok_body}"
  exit 0
fi

if [[ "${skipped}" == "true" ]]; then
  echo "No release intent, but the '${skip_label}' label is present — gate passes."
  upsert_comment "⏭️ **Release-intent check skipped.** This PR carries the \`${skip_label}\` label, so the requirement is waived."
  exit 0
fi

if [[ "${mode}" == "commit" ]]; then
  echo "::error::PR title is not a conventional commit. Rename the PR (e.g. 'feat: …') or apply the '${skip_label}' label."
else
  echo "::error::No changeset found in this PR. Add one with '${add_cmd}' or apply the '${skip_label}' label."
fi
upsert_comment "${nag}"
exit 1
