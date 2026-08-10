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

## What we control

**The manifest must say portable.** ClaudeRig 1.4.0 shipped with a manifest that
wasn't `NestedInstallerType: portable`, so winget unpacked the zip and put
nothing on PATH. Automatic validation failed as an unattended-install timeout —
which reads like flakiness — and it took a human asking *"Is this a Portable
package?"* to name it. That was 23 days for a one-line manifest field.

`scripts/check-winget-manifests.sh` now runs in the release workflow and fails
the job if any generated manifest isn't portable with an alias per binary. The
PRs are still opened (GoReleaser publishes before the check can run), but the
alarm arrives in minutes instead of weeks.

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
