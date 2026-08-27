# desktop launcher: decide which profile a deep link opens, before the OS does

> Status: **proposed** (2026-08-27). Three pieces, deliberately separable:
> per-profile **theme** (ready to build), **`claude://` handler registration**
> (ready once one experiment lands), and the **shim swap** — which is **on
> hold**, pending evidence described under [Shim swap](#shim-swap).
>
> Builds on [Desktop profiles](CLAUDERIG-DESKTOP-PROFILES.md), whose deep-link
> section states the constraint this document tries to loosen, and whose
> branding section records what is permanently out of reach.
> Code it touches: [`internal/clauderig/desktop`](../internal/clauderig/desktop)
> and [`internal/clauderig/commands/desktop*.go`](../internal/clauderig/commands).

## The job

A `claude://` deep link is routed by **scheme**, not to a window. With more than
one Claude Desktop instance running, the OS picks which one receives it — and
that pick can cross an account boundary. This is observed, not theorised:
asking for `brightshore` with `relatecpa` also open filed the session under
relatecpa's `accountUuid`.

clauderig's current answer is to refuse. `refuseIfRoutingIsAmbiguous` counts
competing windows and stops the send, with `--anyway` as the override. That is
the right default and it is not a solution: the user is told to close windows.

Worse, the profile-less install — the ordinary Claude Desktop, started with no
`--user-data-dir` — is both the most likely competitor and the hardest to
exclude. It cannot be blocked from running:

- **macOS** has no supported per-app launch veto short of MDM policy or an
  Endpoint Security system extension, which needs an Apple-granted entitlement.
  Removing execute permission from the bundle is not available either, because
  every profile launches through that same bundle.
- **Windows** does have one — an Image File Execution Options `Debugger`
  redirect on `claude.exe`, plus AppLocker on Pro and Enterprise — but both are
  admin-only, machine-wide, and match by executable **name**.
- The default install cannot simply be removed: `desktop.Seed` seeds every new
  profile from its configuration, and `Installed()` is how each launch finds the
  app at all.

So the goal is not to block it. It is to **intercept the decision** — to own
what happens between "a link exists" and "a window receives it".

## Design

### The primitive this rests on

Registering a handler for `claude://` creates a loop hazard that must be solved
first. `darwinApp.OpenURL` is `/usr/bin/open <url>` and the Windows one is
`cmd /c start "" <url>`; both resolve **by scheme**. Once clauderig owns that
scheme, `desktop open --session` hands a link to the OS, which hands it straight
back to clauderig — on the most-used path in the package.

The delivery primitive must therefore bypass scheme resolution:

- **macOS:** `open -a <bundle> <url>` targets the named application. It still
  cannot choose *which instance* of that bundle receives the link, so the
  existing ambiguity refusal stays exactly as it is.
- **Windows:** likely better. Electron's single-instance lock is keyed on the
  `userData` directory, so `claude.exe --user-data-dir=<dir> <url>` against a
  running profile fails to take that profile's lock, forwards its argv to it,
  and fires `second-instance` **in the right window**.

If the Windows behaviour holds, that is genuine per-profile deep-link delivery,
and `--anyway` and the ambiguity refusal become unnecessary there. It is a
20-minute experiment — two profiles open, send a `claude://resume` link to one,
see where it lands — and it gates how much of the guard survives on Windows.
**Run it before writing the handler.**

### 1. Theme per profile

The only per-profile visual distinction available at all (see the branding
section of the profiles document for why icon and window title are closed).

- `desktop add --theme dark|light|system`, applied **after** `desktop.Seed`, so
  it overrides the value seeding copied from the default install.
- `desktop theme <name> [mode]` to change it later; the bare form prints the
  current mode. `desktop ls` gains a theme column.

**It must refuse while that profile is running.** Electron rewrites `config.json`
and holds it open; a write underneath a running app is silently lost. Same
discipline `desktop rm` already applies — and, as there, a failed process scan is
not "closed" and must not be treated as such.

`userThemeMode` is already in `DesktopConfigKeepKeys()`, so a theme travels with
its profile through sync. That is the wanted behaviour — same profile, same
colour, on every machine — but it is a consequence worth stating rather than
discovering.

**Prerequisite:** the accepted spellings must be **observed**, not guessed. A
live install reads `"system"`; the dark and light values are to be read back
after toggling the app once.

### 2. `claude://` handler registration

Surface: `clauderig desktop launcher install | status | remove`.

**The launcher is clauderig.** No second binary, and no second codebase. What
differs per platform is only what the OS is permitted to register.

*macOS* can bind a URL scheme only to an `.app` bundle, never to a bare binary —
but `shortcut_darwin.go` already writes exactly the bundle needed: a `/bin/sh`
executable invoking clauderig by absolute path, `LSUIElement` so it never takes
a Dock tile, an `osascript` alert helper that surfaces clauderig's own error text
as a dialog, and a marker file for finding it again. The launcher bundle is that
writer plus a `CFBundleURLTypes` block. The scheme is claimed with
`LSSetDefaultHandlerForURLScheme`.

The bundle **must not** claim `com.anthropic.claudefordesktop`. `Installed()`
falls back to an `mdfind` query on that bundle identifier, and a shim answering
to it would have clauderig launching itself in a circle.

*Windows* needs no stub: `HKCU\Software\Classes\claude\shell\open\command` can
name `clauderig.exe` directly. Per-user, no elevation, trivially reversible —
the cheapest half of this document. One wrinkle: clauderig is a
console-subsystem binary, so a GUI-triggered launch flashes a console window
before any of our code runs (`FreeConsole` is too late; the console is allocated
at process creation). Ship with the flash first. If it grates, add a
`clauderigw.exe` GoReleaser entry — the *same* `./cmd/clauderig` main built with
`-H=windowsgui`, the `python.exe`/`pythonw.exe` split — which is one config block
and no new code, though it does reach the archives, winget, scoop, brew and the
bundle installer.

**Routing.** A URL arrives with no profile attached, so the launcher decides: an
explicit flag, else the single running instance, else `lastOpened`, else the
picker already built for `desktop open -i`. Then it delivers with the primitive
above.

**The handler entry point is a hidden command.** It is an OS-facing entry, not a
human one, and does not belong in help or completion. Management stays at
`clauderig desktop launcher`, under `desktop`, because routing to a Desktop
profile is the only thing it does.

**`status` and `remove` carry the risk.** `status` must detect and report that
something else has taken the scheme back — an app update, a reinstall — and
`remove` must restore Claude Desktop as the handler. A half-uninstalled scheme
handler is a machine where every deep link silently dies.

**State** lives at `~/.clauderig/desktop/launcher.json`, deliberately *not* as a
`.rig.json` key, which would pull in the JSON schema, `knownKeys`, the scaffold
template and `configuration.md` for something no user hand-edits.

**Path rot now costs more.** The absolute-path dependency the shortcut writer
documents applies to deep links too: move or uninstall clauderig and `claude://`
stops working, not merely an icon. `launcher status` must check the recorded
path is still executable, and `doctor` should fold that check in.

### 3. Shim swap {#shim-swap}

**On hold.** Not scheduled, not started; recorded here so the reasoning is not
re-derived, and so the evidence that would unblock it is written down.

The idea: relocate the real bundle to `~/Library/Application Support/clauderig/`
and put a launcher bundle at `/Applications/Claude.app`. It is the only approach
that catches a **Dock click**, which no scheme handler can see.

Held because:

- `Installed()` would have to prefer a recorded relocated path and actively skip
  the shim — and that function is on every launch path in the package.
- **Auto-update behaviour is unknown.** Squirrel.Mac updates in place at the path
  it runs from, which argues relocation is safe; but an updater that ever
  reinstalls to `/Applications` silently clobbers the shim, and the machine
  returns to profile-less launches **with no signal**. This needs observation
  across at least one real update cycle, plus a `launcher doctor` that detects
  the clobber.
- **Windows is worse, not better.** Squirrel recreates its Start Menu shortcuts
  on update, so repointing them decays. Real interception there means the IFEO
  redirect: admin, machine-wide, and matching every `claude.exe` on the box.
  Document it as available; do not ship it.

If it is ever built: behind `launcher install --takeover`, never by default, with
a loud `remove`. The failure mode is the kind that appears a week later on
someone else's machine.

The rejected alternative is worth naming, because it looks reasonable: a
**watchdog** — a launchd agent or scheduled task polling `RunningDefault()`,
quitting a profile-less instance and offering the picker. It needs no privileges
and is fully reversible, but it races a user who is mid-click and shows a window
that then vanishes. Least reliable of the three, and the most likely to feel
haunted.

## Scope decisions

**Blocking is not attempted.** The section above lists why every mechanism is
either unavailable, admin-only, or breaks the profiles themselves. Interception
gets the same outcome and is reversible.

**The ambiguity refusal stays on macOS.** The handler decides where a link is
*sent*; it does not create a per-instance address. Nothing here makes it safe to
send into two open windows, so `refuseIfRoutingIsAmbiguous` is unchanged.

**No second binary.** A separate launcher would need its own release lane,
Authenticode and notarization hooks, and manifest entries across four package
managers — and would introduce version skew, where a launcher from one release
routes links for a clauderig from another. Startup cost does not argue otherwise:
cobra init is milliseconds against an Electron launch.

**Reentrancy is a test, not a convention.** Because handler and sender are one
binary, "the URL-handler path never calls `OpenURL`" is a unit test.

**GUI-safe errors are a requirement, not a nicety.** The OS invokes the handler
from a click, with no terminal attached. macOS reuses the existing `osascript`
alert; Windows needs an equivalent, whichever way the console question is
settled.

## Sequencing

1. Run the Windows single-instance forwarding experiment. It decides whether the
   handler merely routes deliberately, or also retires the ambiguity refusal on
   Windows.
2. Theme, as a standalone PR. Smallest, safest, independently useful.
3. Handler registration.
4. Revisit the shim swap only with update-cycle evidence in hand.

Each of 2 and 3 needs a changeset, and both reach `rig ui`, the parent help
block, `site/rig/verbs.md`, and the `rigsmith-tools` skill.

## Open questions

- Do the `"dark"` and `"light"` spellings of `userThemeMode` match the app's own
  settings exactly, and does Desktop re-read the file at launch or only write it?
- Does Claude Desktop's Windows build handle a deep link delivered through
  `second-instance`, or only through its own protocol registration?
- Should `launcher install` offer to set distinct themes at the same time? The
  two features answer the same underlying question — *which profile am I looking
  at* — from opposite ends.
