#!/bin/sh
#
# pre-push guard (lefthook's no-clobber-main): refuse an update to the remote's
# main that would drop commits already on it — the mistake that erases merged
# PRs. Git passes the remote's name and the URL being pushed to as $1 and $2,
# and one line per ref on stdin:
#
#   <local ref> <local sha> <remote ref> <remote sha>
#
# Only a line whose remote ref is refs/heads/main is checked, so tags and other
# branches always pass, whatever is checked out. That line passes when the
# remote's main — asked of the push URL itself, now, since a merge you don't
# have yet is exactly what gets erased — is an ancestor of what's being pushed:
# a fast-forward. A main that's behind, diverged, or missing commits you
# haven't fetched is refused, as is a delete of main. If the remote can't be
# asked, the push is refused too: a guard that can't see main can't clear it.
# Override with `git push --no-verify`.

remote="${1:-origin}"
url="${2:-${remote}}"
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

  # What the remote's main is now, from the URL git is pushing to. Not a
  # fetch: FETCH_HEAD is shared with any other fetch, and a failed fetch would
  # leave only git's stale idea of the remote.
  if ! listing="$(git ls-remote "${url}" refs/heads/main 2>/dev/null)"; then
    echo "refusing push: can't reach ${remote} to check its main is only moving forward." >&2
    echo "  override: git push --no-verify" >&2
    refused=1
    continue
  fi
  upstream="$(printf '%s\n' "${listing}" | cut -f1)"
  if [ -z "${upstream}" ]; then
    # Not advertised. With no main there as far as git knows either, it's a
    # first push and there's nothing to lose; but a main git knows about that
    # the remote won't list (hidden refs) can't be checked, so it isn't cleared.
    case "${remote_sha}" in
      *[!0]*)
        echo "refusing push: ${remote} doesn't list its main, so it can't be checked." >&2
        echo "  override: git push --no-verify" >&2
        refused=1
        ;;
    esac
    continue
  fi

  if ! git cat-file -e "${upstream}^{commit}" 2>/dev/null; then
    echo "refusing push: ${remote}/main has commits you haven't fetched —" >&2
    echo "  forcing main would erase them." >&2
    echo "  sync:     git fetch ${remote}, then git merge --ff-only ${remote}/main (or git rebase ${remote}/main to keep local commits)" >&2
    echo "  override: git push --no-verify" >&2
    refused=1
    continue
  fi

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
