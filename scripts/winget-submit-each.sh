#!/bin/sh
# Submit each package komac generated as its own winget PR, retrying a failed
# one, and saying loudly which ones never went out.
#
#   sh scripts/winget-submit-each.sh dist/winget
#
# Env:
#   GITHUB_TOKEN             komac's token (public_repo); also asks GitHub
#                            whether a PR already exists
#   WINGET_SUBMIT_TRIES      attempts per package (default 3)
#   WINGET_SUBMIT_WAIT       seconds before the first retry, doubling (default 30)
#   GITHUB_STEP_SUMMARY      when set (in Actions), failures are written there too
#
# Why not `komac submit <dir> --all`, as winget-submit.sh used to: it stops at
# the first failure. In 1.23.0 GitHub refused to create komac's branch for the
# first package ("Ref cannot be created"), so none of the five went out — and
# because the step is continue-on-error, the run was green. The same
# submission, run by hand minutes later with the same token and fork, went
# through: a transient refusal, which a retry covers.
#
# Nothing may open a second PR. komac names its branch <id>-<version>-<random>
# on the token user's fork, so before submitting a package, and again before
# each retry, this asks GitHub whether an open PR already comes from such a
# branch: a package that went out before a partial failure is skipped when the
# whole thing is re-run, and one komac opened a PR for before failing isn't
# retried. GitHub's issue search lags, so it isn't used for this.
set -eu

out="${1:?usage: winget-submit-each.sh <manifest dir>}"
tries="${WINGET_SUBMIT_TRIES:-3}"
first_wait="${WINGET_SUBMIT_WAIT:-30}"
api="https://api.github.com"

gh_get() {
  curl -fsS \
    ${GITHUB_TOKEN:+-H "Authorization: Bearer ${GITHUB_TOKEN}"} \
    -H "Accept: application/vnd.github+json" "$api/$1"
}

# pr_open <id> <version>: an open PR in microsoft/winget-pkgs comes from one of
# komac's branches for this package and version. An unanswered question is
# "no", so the caller retries — the worse outcome being a duplicate PR a
# moderator closes, rather than a submission silently never made.
pr_open() {
  user=$(gh_get user 2>/dev/null | sed -n 's/^ *"login": *"\([^"]*\)".*/\1/p' | head -n 1)
  [ -n "$user" ] || return 1
  for ref in $(gh_get "repos/$user/winget-pkgs/git/matching-refs/heads/$1-$2-" 2>/dev/null |
    sed -n 's#^ *"ref": *"refs/heads/\([^"]*\)".*#\1#p'); do
    if gh_get "repos/microsoft/winget-pkgs/pulls?state=open&head=$user:$ref" 2>/dev/null |
      grep -q '"number"'; then
      return 0
    fi
  done
  return 1
}

failed=""
for installer in $(find "$out" -name '*.installer.yaml' | sort); do
  dir=$(dirname "$installer")
  version=$(basename "$dir")
  id=$(sed -n 's/^PackageIdentifier:[[:space:]]*\([^[:space:]]*\).*/\1/p' "$installer" | tr -d '\r' | head -n 1)
  # Already open (a resubmission after a partial failure, or a re-run): leave it.
  if pr_open "$id" "$version"; then
    echo "$id $version: a PR is already open — skipping it."
    continue
  fi
  attempt=1
  wait=$first_wait
  while :; do
    echo "→ submitting $id $version (attempt $attempt of $tries)"
    if komac submit "$dir" --yes; then
      break
    fi
    if pr_open "$id" "$version"; then
      echo "$id $version: komac failed, but its PR is open — nothing to retry."
      break
    fi
    if [ "$attempt" -ge "$tries" ]; then
      failed="$failed $id"
      break
    fi
    echo "::warning::$id $version: submission failed (attempt $attempt of $tries); retrying in ${wait}s."
    sleep "$wait"
    attempt=$((attempt + 1))
    wait=$((wait * 2))
  done
done

if [ -z "$failed" ]; then
  exit 0
fi

# The step is continue-on-error, so a failure here can't fail the release that
# is already out. It has to be seen anyway: an annotation per package, and the
# command that resubmits them in the run summary.
for id in $failed; do
  echo "::error::$id was not submitted to winget after $tries attempts. Resubmit by hand (packages already open are skipped): GITHUB_TOKEN=<WINGET_TOKEN> sh scripts/winget-submit.sh $version --submit"
done
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  {
    echo "### ⚠️ winget: not submitted"
    echo
    echo "After $tries attempts each:$failed. From an up-to-date checkout, resubmit with the command below; packages whose PR is already open are skipped."
    echo
    echo '```sh'
    echo "GITHUB_TOKEN=<the WINGET_TOKEN PAT> sh scripts/winget-submit.sh ${version} --submit"
    echo '```'
  } >>"$GITHUB_STEP_SUMMARY"
fi
exit 1
