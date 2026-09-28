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
#   WINGET_TAG, WINGET_PACKAGES  passed through by winget-submit.sh; repeated in
#                            the resubmit command so it targets the same release
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
# retried. GitHub's issue search lags, so it isn't used for this. When GitHub
# can't answer (an outage, a rate limit, a bad token) the question is open, not
# "no": the package isn't submitted blind, it's reported, and a re-run by hand
# settles it. A duplicate PR is a moderator's time; a missing one is visible.
set -eu

out="${1:?usage: winget-submit-each.sh <manifest dir>}"
tries="${WINGET_SUBMIT_TRIES:-3}"
first_wait="${WINGET_SUBMIT_WAIT:-30}"
api="https://api.github.com"

# Bounded, so a stalled response can't hold up the other packages or the report.
gh_get() {
  curl -fsS --connect-timeout 10 --max-time 30 --retry 2 \
    ${GITHUB_TOKEN:+-H "Authorization: Bearer ${GITHUB_TOKEN}"} \
    -H "Accept: application/vnd.github+json" "$api/$1"
}

user=""

# pr_open <id> <version>: 0 when an open PR in microsoft/winget-pkgs comes from
# one of komac's branches for this package and version, 1 when none does, and
# 2 when GitHub couldn't be asked. The answers are read as JSON (jq), never by
# their line layout: a response jq can't read as the expected shape is 2, not
# an empty list, since reading "no refs" into it would submit a duplicate.
pr_open() {
  if [ -z "$user" ]; then
    raw=$(gh_get user 2>/dev/null) || return 2
    user=$(printf '%s' "$raw" | jq -r '.login // empty' 2>/dev/null) || return 2
    [ -n "$user" ] || return 2
  fi
  # Fetched, then parsed: in `gh_get | jq` a failed call reaches jq as empty
  # input, which jq reads as nothing and exits 0.
  raw=$(gh_get "repos/$user/winget-pkgs/git/matching-refs/heads/$1-$2-" 2>/dev/null) || return 2
  refs=$(printf '%s' "$raw" |
    jq -r 'if type == "array" then .[] | .ref | sub("^refs/heads/"; "") else error("not a list of refs") end' 2>/dev/null) || return 2
  for ref in $refs; do
    raw=$(gh_get "repos/microsoft/winget-pkgs/pulls?state=open&head=$user:$ref" 2>/dev/null) || return 2
    count=$(printf '%s' "$raw" |
      jq -r 'if type == "array" then length else error("not a list of pulls") end' 2>/dev/null) || return 2
    if [ "$count" -gt 0 ]; then
      return 0
    fi
  done
  return 1
}

# Discovery has to be complete: a directory find can't read would leave its
# package out of a list that otherwise looks fine, and nothing would say so.
listing=$(mktemp)
trap 'rm -f "$listing"' EXIT
if ! find "$out" -name '*.installer.yaml' >"$listing"; then
  echo "::error::Couldn't list every winget manifest under $out — nothing was submitted."
  exit 1
fi
installers=$(sort "$listing")
if [ -z "$installers" ]; then
  echo "::error::No winget manifests under $out — nothing was submitted."
  exit 1
fi

# Each failure as <label>@<version>|<reason>, one per line.
failed=""
fail() {
  failed="${failed}$1@$2|$3
"
}

for installer in $installers; do
  dir=$(dirname "$installer")
  version=$(basename "$dir")
  id=$(sed -n 's/^PackageIdentifier:[[:space:]]*\([^[:space:]]*\).*/\1/p' "$installer" | tr -d '\r' | head -n 1)
  if [ -z "$id" ]; then
    fail "$(basename "$installer")" "$version" "no PackageIdentifier in the manifest"
    continue
  fi
  # Already open (a resubmission after a partial failure, or a re-run): leave it.
  if pr_open "$id" "$version"; then
    echo "$id $version: a PR is already open — skipping it."
    continue
  elif [ $? -eq 2 ]; then
    fail "$id" "$version" "GitHub couldn't be asked whether its PR is already open, so it wasn't submitted"
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
    elif [ $? -eq 2 ]; then
      fail "$id" "$version" "komac failed, and GitHub couldn't be asked whether it opened the PR anyway, so it wasn't retried"
      break
    fi
    if [ "$attempt" -ge "$tries" ]; then
      fail "$id" "$version" "not submitted after $tries attempts"
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
# command that resubmits them in the run summary. The command carries the
# release's WINGET_TAG and WINGET_PACKAGES when they were set, so it targets the
# same release; packages whose PR is open by then are skipped.
env_prefix=""
[ -n "${WINGET_TAG:-}" ] && env_prefix="WINGET_TAG='$WINGET_TAG' "
[ -n "${WINGET_PACKAGES:-}" ] && env_prefix="${env_prefix}WINGET_PACKAGES='$(printf '%s' "$WINGET_PACKAGES" | tr '\n' ' ')' "
resubmit() {
  echo "GITHUB_TOKEN=<the WINGET_TOKEN PAT> ${env_prefix}sh scripts/winget-submit.sh $1 --submit"
}

versions=""
while IFS='|' read -r entry reason; do
  [ -n "$entry" ] || continue
  version=${entry##*@}
  echo "::error::${entry%@*} $version: $reason. Resubmit by hand: $(resubmit "$version")"
  case " $versions " in *" $version "*) ;; *) versions="$versions $version" ;; esac
done <<FAILED
$failed
FAILED

if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  {
    echo "### ⚠️ winget: not submitted"
    echo
    printf '%s' "$failed" | while IFS='|' read -r entry reason; do
      [ -n "$entry" ] && echo "- \`${entry%@*}\` ${entry##*@}: $reason"
    done
    echo
    echo "From an up-to-date checkout, resubmit; packages whose PR is already open are skipped:"
    echo
    echo '```sh'
    for version in $versions; do
      resubmit "$version"
    done
    echo '```'
  } >>"$GITHUB_STEP_SUMMARY"
fi
exit 1
