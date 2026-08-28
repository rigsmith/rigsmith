---
type: feat
scope: clauderig
"github.com/rigsmith/rigsmith"
---

The claudeRig UI ships as a signed, notarized macOS app: `brew install --cask rigsmith/tap/clauderig-ui`.

It could not ride the existing release path, and the reason is worth recording. The four CLIs cross-compile from Linux and quill signs the bare Mach-O binaries, which is what keeps a release cheap — no macOS runner at all. The UI breaks every one of those assumptions: it needs cgo, so a real macOS runner; it ships as a bundle rather than a binary; and quill signs Mach-O files, not bundles.

What it does **not** need is any new signing work. It uses the same Developer ID certificate and the same App Store Connect key the notarize block already feeds to quill, through `codesign` and `xcrun notarytool` instead. A second consumer of the credentials that exist, not a second identity.

So it is a separate job on `macos-latest` that runs after the release is cut, rather than moving the whole release onto macOS. Keeping the CLI builds on Linux is what makes them cheap, and a failure packaging the app must not take the four binaries down with it.

The bundle is universal (arm64 + amd64 via `lipo`), so one download serves both architectures and the cask needs no arch logic. It declares `LSUIElement`, which is the Info.plist half of the `ActivationPolicyAccessory` the app already sets at runtime — declaring it in both stops a Dock icon flashing up before the app gets to say otherwise. The icon is generated at package time from `design/marks/png/app-claudeRig-512.png` rather than committed as an `.icns`, because `design/` is the single source for the brand and a binary copy checked in beside it silently stops matching.

The cask is written by the same job from the zip's real checksum. GoReleaser publishes the other casks from its own artifacts and cannot publish this one — the app is built after that job has finished, so its checksum does not exist when the casks are written. There is no `depends_on` linking it to the `clauderig` cask: the app reads the sync repo directly and is usable on its own, and forcing the CLI on someone who wanted a menu bar app is the coupling a separate cask exists to avoid.

Everything degrades to a skip rather than a failure when a secret is absent — unsigned, un-notarized, no cask — the same stance the existing notarize block takes, so this is safe to ship before the tap token is wired.

Separately, CI now installs `libgtk-3-dev` and `libwebkit2gtk-4.1-dev` on the Linux leg. `go test ./...` could not compile `./ui` there at all, so that job was red the moment the UI landed. The app only ships for macOS, but keeping it inside `./...` means a change that breaks another platform is caught here rather than by whoever tries to port it.
