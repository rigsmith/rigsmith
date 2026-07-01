# winget manifests

Staged [Windows Package Manager](https://learn.microsoft.com/windows/package-manager/)
manifests for the rigsmith CLIs, ready to submit to
[microsoft/winget-pkgs](https://github.com/microsoft/winget-pkgs). These are kept
here for review and record; nothing here is consumed by the build.

## Layout

`manifests/` mirrors the winget-pkgs path so the files drop in unchanged:

```
manifests/r/RigSmith/<Tool>/<version>/
  RigSmith.<Tool>.yaml              # version manifest
  RigSmith.<Tool>.installer.yaml    # zip + portable nested installer (x64 + arm64)
  RigSmith.<Tool>.locale.en-US.yaml # publisher / description / license
```

Per-tool packages (hand-staged here): `RigSmith.Rig`, `RigSmith.ShipRig`,
`RigSmith.ChangeRig`, `RigSmith.ClaudeRig`. Each installer is the per-tool
`*_<version>_windows_<arch>.zip` from the GitHub release, installed as a `portable`
nested installer exposing the `<tool>` command. `InstallerSha256` values come from
the release's `checksums.txt`.

## The bundle: `RigSmith.Rigsmith` (auto-generated)

`RigSmith.Rigsmith` installs **all four** tools with one command
(`winget install rigsmith`). It is **not** staged here — GoReleaser's `winget`
publisher generates it on release from the combined `rigsmith_<version>_windows_<arch>.zip`
archive (four `portable` nested installers, real `InstallerSha256`), so there are no
SHAs to fill by hand. See `.goreleaser.yaml` (`winget:`).

Until a fork of `microsoft/winget-pkgs` + a `WINGET_TOKEN` exist, the release
generates the manifest into `dist/winget/…` (uploaded as a release artifact) but
does **not** open the submission PR — submit it manually, or set `WINGET_TOKEN`
(write access to the fork) to have GoReleaser open the PR automatically.

## Submitting a release

winget-pkgs prefers **one package per PR**. From a fork:

1. Copy `manifests/r/RigSmith/<Tool>/<version>/` into a winget-pkgs branch.
2. Open a PR to `microsoft/winget-pkgs`; the validation pipeline downloads each
   installer, checks the SHA256, and scans the binary.
3. A moderator reviews new publishers — be ready to confirm ownership of
   rigsmith.dev / the project.

On Windows you can instead let `wingetcreate` author + submit:
`wingetcreate update RigSmith.<Tool> --version <v> --urls <zip-x64> <zip-arm64> --submit`.

## Updating for a new version

For the **per-tool** packages here: bump `PackageVersion` in all three files, point
`InstallerUrl` at the new tag, and refresh `InstallerSha256` + `ReleaseDate`. (The
**bundle** `RigSmith.Rigsmith` needs none of this — GoReleaser regenerates it on
every tag with the correct SHAs, like the Homebrew cask and Scoop manifest.)
