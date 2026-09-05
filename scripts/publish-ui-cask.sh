#!/bin/sh
# Write the clauderig-ui Homebrew cask into rigsmith/homebrew-tap.
#
# GoReleaser publishes the other casks from its own artifacts. This one cannot
# come from there: the .app is built on a macOS runner AFTER that job has
# finished, so its checksum does not exist when GoReleaser writes its casks.
#
#   $1  version, e.g. 1.2.3 (no leading v)
#   $2  directory holding the built zip
#
# Env:
#   HOMEBREW_TAP_TOKEN  push access to rigsmith/homebrew-tap
set -eu

version="${1:?version required}"
dir="${2:?artifact directory required}"
# Absent token is a skip, not a failure — the same stance the notarize block
# takes, so this is safe to ship before the tap secret is wired. A step `if`
# cannot check it: a step's own env block is not in scope for its own condition,
# which is exactly the sort of thing that silently never runs.
if [ -z "${HOMEBREW_TAP_TOKEN:-}" ]; then
  echo "HOMEBREW_TAP_TOKEN unset — not publishing the cask"
  exit 0
fi

app="claudeRigUi"
zip="${app}_${version}_darwin_universal.zip"
[ -f "$dir/$zip" ] || { echo "no $dir/$zip to publish" >&2; exit 1; }

sha=$(shasum -a 256 "$dir/$zip" | awk '{print $1}')
url="https://github.com/rigsmith/rigsmith/releases/download/v${version}/${zip}"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
git clone --depth 1 \
  "https://x-access-token:${HOMEBREW_TAP_TOKEN}@github.com/rigsmith/homebrew-tap.git" \
  "$work/tap"

mkdir -p "$work/tap/Casks"
# No `depends_on` on the clauderig cask: the app is usable on its own — it reads
# the sync repo directly — and forcing the CLI on someone who wanted the menu
# bar app is the coupling the separate cask exists to avoid.
cat > "$work/tap/Casks/clauderig-ui.rb" <<RB
cask "clauderig-ui" do
  version "$version"
  sha256 "$sha"

  url "$url"
  name "claudeRigUi"
  desc "Menu bar app for claudeRig: sync status and your Claude Code sessions"
  homepage "https://rigsmith.dev/clauderig/"

  depends_on macos: ">= :monterey"

  app "$app.app"

  zap trash: [
    "~/Library/Application Support/$app",
    "~/Library/Caches/dev.rigsmith.clauderig-ui",
    "~/Library/Preferences/dev.rigsmith.clauderig-ui.plist",
    "~/Library/Saved Application State/dev.rigsmith.clauderig-ui.savedState",
  ]
end
RB

cd "$work/tap"
git config user.name "rigsmith-releaser"
git config user.email "releases@rigsmith.dev"
git add Casks/clauderig-ui.rb
# Nothing to commit when a re-run produces the same cask; that is success, not
# a failure worth turning the release red over.
if git diff --cached --quiet; then
  echo "cask already current at $version"
  exit 0
fi
git commit -m "clauderig-ui $version"
git push
echo "published clauderig-ui $version"
