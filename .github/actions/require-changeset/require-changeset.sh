#!/usr/bin/env bash
#
# The changeset gate: the in-Action equivalent of the @changesets bot. On a pull
# request it checks whether the PR adds a changeset file and:
#   - upserts a sticky comment (nag when missing, ✅ when present), and
#   - fails the check when none is found, so a required status check blocks the
#     merge until a changeset is added (or the skip label is applied).
#
# Runs on the `pull_request` event. For PRs from forks the GITHUB_TOKEN is
# read-only, so the sticky comment is skipped there (the failing check still
# blocks); use `pull_request_target` if you need fork comments too.
set -euo pipefail

cwd="${INPUT_CWD:-.}"
skip_label="${INPUT_SKIP_LABEL:-skip-changeset}"
want_comment="${INPUT_COMMENT:-true}"
add_cmd="${INPUT_ADD_COMMAND:-changerig add}"
marker="<!-- rigsmith-changeset-gate -->"

cd "${cwd}"

event="${GITHUB_EVENT_PATH:?GITHUB_EVENT_PATH not set — this action runs on pull_request events}"
pr_number="$(jq -r '.pull_request.number // empty' "${event}")"
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

# Diff the PR's range for *added* changeset files. base_sha may be absent from a
# shallow clone, so fetch it; fall back to the merge-base if the range is unknown.
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
      || echo "::warning::Could not update the changeset comment (token may be read-only on fork PRs)."
  else
    gh api -X POST "repos/${GITHUB_REPOSITORY}/issues/${pr_number}/comments" -f body="${body}" >/dev/null 2>&1 \
      || echo "::warning::Could not post the changeset comment (token may be read-only on fork PRs)."
  fi
}

if [[ "${count}" -gt 0 ]]; then
  echo "Found ${count} changeset(s) in this PR:"
  printf '%s\n' "${new_changesets}"
  upsert_comment "✅ **Changeset detected.** This PR adds a changeset, so the next release will pick it up. Thanks!"
  exit 0
fi

if [[ "${skipped}" == "true" ]]; then
  echo "No changeset, but the '${skip_label}' label is present — gate passes."
  upsert_comment "⏭️ **Changeset skipped.** This PR carries the \`${skip_label}\` label, so the changeset requirement is waived."
  exit 0
fi

# ---- no changeset: nag + fail ----------------------------------------------
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

echo "::error::No changeset found in this PR. Add one with '${add_cmd}' or apply the '${skip_label}' label."
upsert_comment "${nag}"
exit 1
