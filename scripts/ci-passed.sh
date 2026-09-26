#!/bin/sh
# Wait for CI to pass on one exact commit, before it's tagged or released.
#
#   scripts/ci-passed.sh <sha> [--push-only]
#
# A ci.yml run counts only when it succeeded AND ran every platform's tests: a
# draft pull request's CI skips the macOS and Windows jobs, and GitHub counts a
# skipped job as success, so "the run passed" alone would let a release through
# on Linux tests only. Every run for the commit is read, not just the latest.
#
# --push-only takes push runs alone (the release job: the commit it tags is a
# merge on main, whose CI is the push run). Without it, a pull request's run of
# the exact commit counts too, as long as it ran everything.
#
# Waits while any run is still going; fails once none passed and none is left
# running, or after CI_WAIT_TRIES polls CI_WAIT_SECONDS apart (90 × 30s). Needs
# GH_TOKEN and GH_REPO. Fails closed: an API error is a failure, not a pass.
set -eu

sha="${1:?usage: ci-passed.sh <sha> [--push-only]}"
push_only=false
[ "${2:-}" = "--push-only" ] && push_only=true
tries="${CI_WAIT_TRIES:-90}"
pause="${CI_WAIT_SECONDS:-30}"
required="test (linux)
test (macos)
test (windows)"

i=0
while [ "$i" -lt "$tries" ]; do
  i=$((i + 1))
  runs=$(gh api --paginate "repos/$GH_REPO/actions/workflows/ci.yml/runs?head_sha=$sha&per_page=100" \
    --jq '.workflow_runs[] | "\(.id) \(.event) \(.status) \(.conclusion)"')
  pending=false
  seen=""
  # Read from a file, not a pipe, so the loop's decisions reach this shell.
  printf '%s\n' "$runs" > "${TMPDIR:-/tmp}/ci-passed.$$"
  while read -r id event status conclusion; do
    [ -n "$id" ] || continue
    if $push_only && [ "$event" != push ]; then continue; fi
    seen="$seen $event:$status:$conclusion"
    if [ "$status" != completed ]; then
      pending=true
      continue
    fi
    [ "$conclusion" = success ] || continue
    jobs=$(gh api --paginate "repos/$GH_REPO/actions/runs/$id/jobs?per_page=100" \
      --jq '.jobs[] | "\(.name)=\(.conclusion)"')
    missing=$(printf '%s\n' "$required" | while read -r job; do
      printf '%s\n' "$jobs" | grep -qxF "$job=success" || printf '%s; ' "$job"
    done)
    if [ -z "$missing" ]; then
      rm -f "${TMPDIR:-/tmp}/ci-passed.$$"
      echo "CI passed on $sha ($event run $id, every platform's tests ran)."
      exit 0
    fi
    echo "$event run $id passed without every platform's tests (not run or not passed: $missing); not counting it."
  done < "${TMPDIR:-/tmp}/ci-passed.$$"
  rm -f "${TMPDIR:-/tmp}/ci-passed.$$"

  echo "$(date -u +%H:%M:%S) CI on $sha:${seen:- no run yet}"
  if ! $pending && [ -n "$seen" ]; then
    echo "::error::No CI run on $sha passed with every platform's tests, and none is still running; not releasing it."
    exit 1
  fi
  [ "$i" -lt "$tries" ] && sleep "$pause"
done
echo "::error::CI hadn't passed on $sha after $tries checks; not releasing it."
exit 1
