#!/usr/bin/env bash
#
# Orchestrates a rigsmith release on GitHub Actions, mirroring @changesets/action
# (and its .NET clone in net-changesets):
#
#   - when changesets are pending, run the version command and open/update a
#     "Version Packages" PR on changeset-release/<branch>
#   - when none are pending (the version PR was merged), run the publish command
#     (if configured), which pushes to registries and creates git tags
#
# The version/publish/status commands are configurable so the consumer controls
# exactly how shiprig is invoked (`shiprig version`, `shiprig publish`, a wrapper
# script, …). NO_COLOR is set so shiprig's styled output is plain for parsing.
set -euo pipefail
export NO_COLOR=1

version_cmd="${INPUT_VERSION:-shiprig version}"
publish_cmd="${INPUT_PUBLISH:-}"
status_cmd="${INPUT_STATUS:-shiprig status}"
title="${INPUT_TITLE:-Version Packages}"
setup_git_user="${INPUT_SETUP_GIT_USER:-true}"

base_branch="${GITHUB_REF_NAME:-$(git rev-parse --abbrev-ref HEAD)}"
release_branch="${INPUT_BRANCH:-changeset-release/${base_branch}}"

has_changesets="false"
published="false"
published_packages="[]"
pr_number=""

if [[ "${setup_git_user}" == "true" ]]; then
  git config user.name "github-actions[bot]"
  git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
fi

# Pending changesets are .changeset/*.md files other than README.md (config.json
# is .json and never matches). Mirrors `changerig add`, which writes <id>.md.
changeset_count="$(find .changeset -maxdepth 1 -type f -name '*.md' ! -name 'README.md' 2>/dev/null | wc -l | tr -d '[:space:]')"

if [[ "${changeset_count}" -gt 0 ]]; then
  has_changesets="true"
  echo "Found ${changeset_count} changeset(s); preparing the '${title}' pull request."

  # Capture the plan for the PR body before versioning consumes the changesets.
  plan="$(bash -c "${status_cmd}" 2>&1 || true)"

  git checkout -B "${release_branch}"
  bash -c "${version_cmd}"

  if [[ -n "$(git status --porcelain)" ]]; then
    git add -A
    git commit -m "${title}"
    git push --force origin "${release_branch}"

    body="$(printf 'This PR was opened by the rigsmith release action. Merging it will release the versions below.\n\n```\n%s\n```\n' "${plan}")"

    pr_number="$(gh pr list --head "${release_branch}" --base "${base_branch}" --state open --json number --jq '.[0].number // empty' 2>/dev/null || true)"
    if [[ -n "${pr_number}" ]]; then
      gh pr edit "${pr_number}" --title "${title}" --body "${body}"
    else
      # `gh pr create` prints the new PR's URL (…/pull/<number>); take the trailing number.
      created_url="$(gh pr create --base "${base_branch}" --head "${release_branch}" --title "${title}" --body "${body}")"
      pr_number="$(printf '%s' "${created_url}" | grep -oE '[0-9]+$' || true)"
    fi
    echo "Version PR: #${pr_number}"
  else
    echo "version produced no changes; nothing to commit."
  fi
else
  echo "No changesets found."

  if [[ -n "${publish_cmd}" ]]; then
    echo "Publishing..."
    set +e
    publish_output="$(bash -c "${publish_cmd}" 2>&1)"
    publish_exit=$?
    set -e
    echo "${publish_output}"

    if [[ "${publish_exit}" -ne 0 ]]; then
      echo "::error::Publish failed with exit code ${publish_exit}."
      exit "${publish_exit}"
    fi

    # shiprig publish prints "published <name>@<version>  <message>" per package
    # that went to a registry; Go modules are published by their pushed tag, so
    # also harvest "tagged+pushed <module>/v<version>" lines. Strip any ANSI.
    clean="$(printf '%s\n' "${publish_output}" | sed -E 's/\x1b\[[0-9;]*m//g')"
    json=""
    while IFS= read -r line; do
      name=""; version=""
      if [[ "${line}" =~ ^published[[:space:]]+([^[:space:]@]+)@([^[:space:]]+) ]]; then
        name="${BASH_REMATCH[1]}"; version="${BASH_REMATCH[2]}"
      elif [[ "${line}" =~ ^tagged[+]pushed[[:space:]]+(.+)/v([^[:space:]]+) ]]; then
        name="${BASH_REMATCH[1]}"; version="${BASH_REMATCH[2]}"
      fi
      [[ -z "${name}" ]] && continue
      json+="{\"name\":\"${name}\",\"version\":\"${version}\"},"
    done < <(printf '%s\n' "${clean}")

    if [[ -n "${json}" ]]; then
      published="true"
      published_packages="[${json%,}]"
    fi
    echo "Published packages: ${published_packages}"
  fi
fi

{
  echo "hasChangesets=${has_changesets}"
  echo "published=${published}"
  echo "publishedPackages=${published_packages}"
  echo "pullRequestNumber=${pr_number}"
} >> "${GITHUB_OUTPUT}"
