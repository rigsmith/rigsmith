#!/bin/sh
#
# pre-push guard (lefthook's no-clobber-main): refuse an update to the remote's
# main that would drop commits already on it — the mistake that erases merged
# PRs. Git passes the remote name as $1 and one line per ref on stdin:
#
#   <local ref> <local sha> <remote ref> <remote sha>
#
# Only a line whose remote ref is refs/heads/main is checked, so tags and other
# branches always pass, whatever is checked out. That line passes when the
# remote's main (fetched fresh, since a merge you don't have yet is exactly
# what gets erased) is an ancestor of what's being pushed: a fast-forward.
# A push of a main that's behind or diverged, or a delete of main, is refused.
# Override with `git push --no-verify`.

remote="${1:-origin}"
refused=0

while read -r local_ref local_sha remote_ref remote_sha; do
  [ "${remote_ref}" = "refs/heads/main" ] || continue

  case "${local_sha}" in
    *[!0]*) ;;
    *)
      echo "refusing push: it deletes main on ${remote}." >&2
      refused=1
      continue
      ;;
  esac

  # What the remote's main is now. If the fetch fails (offline), fall back to
  # what git last knew; if the remote has no main yet, there's nothing to lose.
  upstream="${remote_sha}"
  if git fetch -q "${remote}" main 2>/dev/null; then
    upstream="$(git rev-parse -q --verify FETCH_HEAD)" || upstream="${remote_sha}"
  fi
  case "${upstream}" in
    *[!0]*) ;;
    *) continue ;;
  esac

  git merge-base --is-ancestor "${upstream}" "${local_sha}" 2>/dev/null && continue

  refused=1
  if git merge-base --is-ancestor "${local_sha}" "${upstream}" 2>/dev/null; then
    echo "refusing push: the main being pushed is behind ${remote}/main —" >&2
    echo "  forcing it would erase commits already merged there." >&2
    echo "  sync:     git fetch ${remote} && git merge --ff-only ${remote}/main" >&2
  else
    echo "refusing push: the main being pushed has diverged from ${remote}/main —" >&2
    echo "  forcing it would erase commits already merged there." >&2
    echo "  sync, keeping your commits:  git fetch ${remote} && git rebase ${remote}/main" >&2
  fi
  echo "  override: git push --no-verify" >&2
done

exit "${refused}"
