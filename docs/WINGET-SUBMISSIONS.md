# winget submissions: what actually costs time

Every release opens five PRs against [microsoft/winget-pkgs][repo] — the four
per-tool packages plus the `RigSmith.Rigsmith` bundle — via GoReleaser's winget
publisher. They are the slowest part of a release, but not uniformly: the 1.4.0
batch ranged from **same-day to 23 days**, all five submitted within minutes of
each other.

This is what the difference was, measured from those PRs.

## The 1.4.0 batch

| PR | package | days | what held it |
| --- | --- | --- | --- |
| [403082][] | Rig | **0** | — |
| [403085][] | ShipRig | **0** | — |
| [403083][] | ChangeRig | 16 | Policy-Test-1.2 (6 waiver rounds), one security-check failure on the arm64 zip |
| [403088][] | Rigsmith | 21 | Policy-Test-1.2, and a new package (moderator review is mandatory) |
| [403084][] | ClaudeRig | 23 | **our bug** — the manifest wasn't portable |

Two merged the same hour they were opened. winget is not inherently slow for
us; specific things are.

## The lane: komac, not GoReleaser

Submissions go through `scripts/winget-submit.sh <version> --submit`, which drives
[komac]. GoReleaser's winget publisher is disabled (`skip_upload: "true"` on each
entry) after what it cost on 1.5.1.

The difference is which manifest each tool starts from. **komac updates the
published one** — it fetches what winget-pkgs already has for the package and
rewrites only the version, URLs, hashes and release notes. **GoReleaser writes a
fresh manifest from its own config**, so any property it has no field for simply
disappears. Submitting 1.5.1 that way drew `Manifest-Metadata-Consistency` on all
five packages:

| property | GoReleaser field? |
| --- | --- |
| `PublisherSupportUrl`, `Copyright`, `Tags`, `ReleaseNotes`, `ReleaseNotesUrl` | yes — but we hadn't set them |
| `Commands` | **none exists** |
| `Moniker` | derived from `name`; regressed `changerig` → `ChangeRig` |
| `NestedInstallerType` / `Files` | written per-installer instead of at the root |

`Commands` is the one with no answer at all: it's what `winget search` and
`winget install --command` read.

### The fork komac submits from

komac has no fork-owner option: it submits from `<token user>/winget-pkgs`,
where the user is whoever `WINGET_TOKEN` authenticates as. That token is
John's PAT, so every PR opens from `JohnCampionJr/winget-pkgs`, and that fork
must exist — komac does not create it, it fails with "failed to get fork". An
org-owned fork cannot take its place (an org is never the "current user"), so
the old `rigsmith/winget-pkgs` fork, which only GoReleaser's disabled publisher
used, has been deleted. The release workflow keeps the personal fork synced
with upstream as hygiene; komac branches from upstream's master commit
regardless, so a stale fork never widens a PR.

### The three steps, and why they're separate

`winget-submit.sh` generates → corrects → verifies → submits, and the order is
the point:

1. **Generate** with `komac update --dry-run --output`, which writes the
   manifests without submitting.
2. **Correct** komac's `exe` misdetection. komac *analyses* each installer rather
   than trusting the published manifest, and it reads `clauderig.exe` as an
   installer — emitting `NestedInstallerType: exe` for that one package while
   getting the other four right. This is the same misdetection that shipped in
   ClaudeRig 1.4.0 and came back 23 days later as a moderator asking "Is this a
   Portable package?". Every rigsmith package is a single static binary in a zip,
   so `exe` is never right, and the correction is logged rather than silent.
3. **Verify** with `check-winget-manifests.sh` — which, because komac submits a
   *directory* as a separate step, now runs **before** anything reaches
   winget-pkgs. Every earlier version of this check could only run after the PRs
   were already open.
4. **Submit** with `komac submit --all`.

Two things the check has been wrong about, both fixed by testing against real
bytes rather than assumed ones: the keys may sit at the root (komac) or inside
each `Installers:` entry (GoReleaser), and winget-pkgs manifests are **CRLF**,
which defeats every `$`-anchored pattern. Fixtures from both producers live in
`scripts/testdata/`.

[komac]: https://github.com/russellbanks/Komac

## The window is a sixth package, on its own tag

`clauderig-ui` ships on `ui/vX.Y.Z` at its own version (see `ui/README.md`), so
its submission cannot derive either the version or the release URL the way the
CLIs' does. `winget-submit.sh` takes both from the environment instead:

```sh
WINGET_TAG=ui/v0.2.0 \
WINGET_PACKAGES=RigSmith.ClaudeRigUi:clauderigUi \
  sh scripts/winget-submit.sh 0.2.0 --submit
```

Defaults are unchanged — `v<version>` and the four CLIs plus the bundle — so the
CLI release calls it exactly as before.

**The first submission is a `komac new`, done by hand.** Everything here is
built on komac *updating* a published manifest, which is the whole reason this
lane exists; a package winget has never seen has nothing to update. Until
`RigSmith.ClaudeRigUi` exists upstream, the step in `release-ui.yml` will fail —
which is why it is `continue-on-error`, like the CLI one, and why a release is
never held up by it.

### Doing that first submission

Written out because it happens once per package, which means roughly never, and
the last time anyone did it the details were reconstructed from a moderator's
review comments.

**The release has to exist first.** komac downloads each installer to hash and
analyse it, so push `ui/vX.Y.Z`, let the lane publish, and only then submit.

```sh
export GITHUB_TOKEN=<the WINGET_TOKEN PAT>   # public_repo scope
V=0.2.0
BASE=https://github.com/rigsmith/rigsmith/releases/download/ui/v$V

komac new RigSmith.ClaudeRigUi \
  --version "$V" \
  --urls "$BASE/clauderigUi_${V}_windows_amd64.zip" \
         "$BASE/clauderigUi_${V}_windows_arm64.zip" \
  --package-name "claudeRig UI" \
  --publisher RigSmith \
  --moniker clauderig-ui \
  --license MIT \
  --package-url https://rigsmith.dev \
  --publisher-url https://rigsmith.dev \
  --publisher-support-url https://github.com/rigsmith/rigsmith/issues \
  --copyright "Copyright (c) 2026 John Campion Jr" \
  --release-notes-url "https://github.com/rigsmith/rigsmith/releases/tag/ui/v$V" \
  --output dist/winget
```

Interactively — no `--dry-run`, no `-s`. `--dry-run` suppresses the prompts, and
the prompts are where komac asks about the nested installer inside the zip,
which is the one thing this package has been wrong about twice. `--output`
writes the manifests without submitting them, so the check below still runs
before anything reaches winget-pkgs.

`--moniker` is set explicitly for the same reason it is on the CLIs: derived
from the package name it comes out as `ClaudeRigUi`, and the moniker is what
people type.

**Then fix what komac has no flag for.** `new` takes every metadata field above
and none of `Commands`, `NestedInstallerType` or `PortableCommandAlias` — those
come from its analysis of the zip and from the prompts. In
`dist/winget/*.installer.yaml`, confirm:

- `NestedInstallerType: portable`, never `exe`;
- every nested file has a `PortableCommandAlias` — `clauderigUi`;
- `Commands` is present, or `winget search` and `winget install --command` have
  nothing to match.

Then run the gate that exists because a moderator caught this 23 days late:

```sh
sh scripts/check-winget-manifests.sh dist/winget
komac submit dist/winget --all --yes
```

The window's `.exe` now carries a real FileDescription, so komac should read it
as a portable rather than guessing from an empty PE — but verify it rather than
trust it, since that guess is exactly what went wrong before.

After that one submission, nothing about the window is manual again: every later
`ui/v` tag runs the automated step above, and komac carries forward everything
set here.

**The identifier is `RigSmith.ClaudeRigUi`** — `Ui`, matching `RigSmith.ClaudeRig`
rather than shouting the acronym. Decided rather than defaulted, because a
published winget package cannot be renamed: it is a new package plus a removal
request for the old one. `release-ui.yml` passes this exact string.

The window's `.exe` does carry version resources — `build/winres/clauderigUi.json`,
embedded by `scripts/winres.sh ui` — so komac reads a real FileDescription and
OriginalFilename for it rather than guessing from an empty PE. It shipped
without any for its whole life, because `build/winres/` had an entry per CLI and
nothing said the window needed one; `TestEveryWindowsBinaryHasVersionResources`
now says it.

Its description is written to the same rule as the CLIs': no `installer`,
`setup`, `7zs.sfx` or `7zsd.sfx` anywhere in it, which
`TestDescriptionsDoNotLookLikeInstallers` pins.

## What we control


**The manifest must say portable.** ClaudeRig 1.4.0 shipped with a manifest that
wasn't `NestedInstallerType: portable`, so winget unpacked the zip and put
nothing on PATH. Automatic validation failed as an unattended-install timeout —
which reads like flakiness — and it took a human asking *"Is this a Portable
package?"* to name it. That was 23 days for a one-line manifest field.

`scripts/check-winget-manifests.sh` runs inside `winget-submit.sh` before
anything is submitted, and fails the run if a manifest isn't portable, is missing
a command alias for any nested binary, or has lost `Commands`. Under the komac
lane it is a real gate rather than the after-the-fact alarm it was while
GoReleaser opened the PRs itself.

**Answer the moderator's questions first.** `scripts/winget-note.sh <version>`
posts a short note on each open submission — portable, what lands on PATH, who
publishes it, that the binaries are signed. Run it after a release:

```sh
sh scripts/winget-note.sh 1.5.0          # preview
sh scripts/winget-note.sh 1.5.0 --post   # comment
```

It skips PRs it has already noted, so re-running after a retry cycle is safe.

## What we don't control

**Policy-Test-1.2 (`ManualReview`)** is a content check that lands on a package
and waits for a moderator to waive it. It hit ChangeRig and the bundle, and not
Rig, ShipRig or ClaudeRig. The one trigger we ever saw explained was on the
bundle's `Description`:

> Field: `Description` — "…clauderig (sync Claude Code config)…" **triggered Targeted Brand**

It is tempting to conclude the word "Claude" is the problem. The data says
otherwise, and it is worth writing down so nobody re-derives it:

- **`RigSmith.ClaudeRig` has never tripped it** — not in 1.0.0, not in 1.4.0 —
  despite "Claude" in its PackageName *and* its ShortDescription. The package
  name is not what draws the check.
- **ChangeRig tripped it with no brand word anywhere** in its manifest, and its
  validation JSON was never posted to the PR, so we cannot say what did it.
- **Rig mentions ".NET"** — also a trademark — and merged in 0 days.

So: don't rename anything, and don't assume a clean description buys a fast
review. Post the note and wait.

**Security-check failures** (`Installer failed security check`, `0x80004005`)
appeared once, on ChangeRig's arm64 zip, and passed on retry. It is not an
unsigned-binary problem — the shipped binaries *are* Authenticode-signed via
Azure Trusted Signing, arm64 included (verified by reading the PE certificate
table straight out of the released zip). Treat it as reputation/download
flakiness and re-request validation.

**New packages** (`New-Package`, vs `New-Manifest` for a new version of an
existing one) always get moderator review. The 1.0.0 batch took 15–16 days each
for this reason alone. Only a first submission pays it.

[repo]: https://github.com/microsoft/winget-pkgs
[403082]: https://github.com/microsoft/winget-pkgs/pull/403082
[403083]: https://github.com/microsoft/winget-pkgs/pull/403083
[403084]: https://github.com/microsoft/winget-pkgs/pull/403084
[403085]: https://github.com/microsoft/winget-pkgs/pull/403085
[403088]: https://github.com/microsoft/winget-pkgs/pull/403088
