---
"github.com/rigsmith/rigsmith": minor
---

Add a Velopack ecosystem adapter (build phase). A .NET project with a sibling `velopack.json` is now a first-class release unit: shiprig's `build` step runs `dotnet publish --self-contained` + `vpk pack` for each configured channel (RID), wraps the notarized macOS `.app` in a `.dmg`, and returns the installers + update feeds as artifacts — replacing a hand-rolled `pack.sh`.

- **Overlays dotnet** (like Tauri overlays cargo): the adapter claims the `.csproj` next to a `velopack.json` and owns its build, while plain dotnet keeps packing ordinary libraries to NuGet. Version discovery and stamping delegate to the dotnet adapter, so csproj/`Directory.Build.props` handling is reused unchanged.
- **Config in `velopack.json`** next to the project: `packId`, `channels` (RIDs), `mainExe`, `icon`, and per-OS signing (`macos.signIdentity`/`notaryProfile`, `windows.trustedSigning`). Signing secrets ride in through the existing signing-env seam, not the file.
- **Host-aware**: macOS channels build only on a macOS host (signing/notarization/DMG); Windows/Linux channels cross-build anywhere. `--dry-build` (snapshot) builds everything unsigned for a fast rehearsal.
- **vpk compatibility check**: the build fails fast if the installed `vpk` CLI major differs from the `Velopack` `<PackageReference>` the project pins.

Phase 1 covers building/packaging; the GitHub upload of the Velopack feeds (`vpk upload github`) remains a release step for now.
