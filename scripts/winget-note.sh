#!/bin/sh
# Post a short "what this package is" note on the winget PRs a release opened,
# so a moderator has the context up front instead of asking for it days later.
#
#   sh scripts/winget-note.sh 1.5.0            # show what would be posted
#   sh scripts/winget-note.sh 1.5.0 --post     # actually post
#
# Prints by default: this comments on a third-party repo (microsoft/winget-pkgs),
# so posting is always an explicit choice. Already-noted PRs are skipped, so a
# re-run after a retry cycle is safe.
#
# Why a note at all: our submissions have drawn Policy-Test-1.2 (ManualReview)
# on packages with nothing unusual in them — ChangeRig 1.4.0 took six waiver
# rounds — and the moderator's questions are the same each time: is it portable,
# what lands on PATH, who publishes it. Answering first is the cheapest thing we
# can do about a queue we do not control. See docs/WINGET-SUBMISSIONS.md.
set -eu

version="${1:?usage: winget-note.sh <version> [--post]}"
post="${2:-}"
repo=microsoft/winget-pkgs
marker="<!-- rigsmith-submission-note -->"

me=$(gh api user --jq .login)

note=$(cat <<EOF
$marker
Submission notes, to save a round trip:

- **Portable CLI.** Each package is a zip of a single static Go binary — \`NestedInstallerType: portable\`, one \`PortableCommandAlias\` per command, no installer and no uninstall entry. Nothing is written outside the winget package/links directories.
- **Signed.** The Windows binaries are Authenticode-signed via Azure Trusted Signing (x64 and arm64 alike), timestamped RFC 3161.
- **Publisher.** RigSmith — https://rigsmith.dev, source at https://github.com/rigsmith/rigsmith, MIT. The release assets these manifests point at are built and published by that repo's tagged release workflow.
- **If the brand check flags a description:** the tools name the ecosystems and products they work with (.NET, Node, Claude Code); the mentions are descriptive, not affiliation claims. Happy to reword if you'd prefer.

Thanks for the review.
EOF
)

found=0
for pr in $(gh pr list --repo "$repo" --author "$me" --state open --limit 50 \
  --json number,title --jq ".[] | select(.title | contains(\"$version\")) | .number"); do
  found=$((found + 1))
  title=$(gh pr view "$pr" --repo "$repo" --json title --jq .title)

  if gh api "repos/$repo/issues/$pr/comments" --paginate --jq '.[].body' 2>/dev/null | grep -qF "$marker"; then
    echo "skip  #$pr — already noted ($title)"
    continue
  fi

  if [ "$post" = "--post" ]; then
    gh pr comment "$pr" --repo "$repo" --body "$note" >/dev/null
    echo "noted #$pr — $title"
  else
    echo "would note #$pr — $title"
  fi
done

if [ "$found" -eq 0 ]; then
  echo "No open $repo PRs by $me matching version $version."
  echo "GoReleaser opens them during the release; give it a minute, or check the fork pushed."
  exit 0
fi

if [ "$post" != "--post" ]; then
  echo
  echo "Nothing posted. Re-run with --post to comment. The note:"
  echo "---"
  echo "$note"
fi
