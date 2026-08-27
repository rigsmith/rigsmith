# desktop launcher: what can, and cannot, decide which profile a link opens

> Status: **partly closed by evidence** (proposed 2026-08-27; read out of the
> bundle and then **confirmed live** the same day, against Claude Desktop
> **1.37937.1**). Per-profile **theme** is confirmed
> and ready to build. **`claude://` handler registration** is **not viable** —
> the app re-claims the scheme on every launch, on every platform; see
> [What the experiment found](#findings). The **shim swap** remains **on hold**
> and has lost most of its value with it.
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

## What the experiment found {#findings}

First read out of the shipped bundle —
`/Applications/Claude.app/Contents/Resources/` at version **1.37937.1** — and
then, for the finding that decides the design, confirmed by taking the scheme
over and watching the app take it back. Every claim below is either a string in
that bundle or an observation, and all of it is **version-specific**: this is
what one release does, not a contract.

### The app re-claims `claude://` on every launch (fatal to §2)

```js
function NDr(){ if(W().authentication.disableDeepLinks)
    for(let e of ADr) o.app.removeAsDefaultProtocolClient(e,jDr,MDr);
  else for(let e of ADr) o.app.setAsDefaultProtocolClient(e,jDr,MDr) }
```

`NDr()` sits in the startup call sequence and runs again on `window_created`,
and — unlike the Squirrel handling nearby — it is **not** guarded by
`platform === 'win32'`. On macOS Electron implements
`setAsDefaultProtocolClient` as `LSSetDefaultHandlerForURLScheme`, on Windows as
an `HKCU\Software\Classes` write. Both are exactly the registration a launcher
would install, so **Claude Desktop takes the scheme back the next time it
starts** — which, since clauderig is what starts it, is immediately.

The one supported way to stop it is the managed-policy key the same code reads:
`disableDeepLinks`, surfaced as `disableDeepLinkRegistration`. It is a dead end,
because it does not only stop the *registration*. `claudeURLHandler` consults the
same flag and refuses everything but Login and ClaudeAI hosts — so the policy
that would let clauderig keep the scheme also stops the app honouring
`claude://resume?session=`, which is the whole of `desktop open --session`.

Corroborating evidence that scheme claims are contested and stale ones linger:
this machine has **two** bundles claiming `claude:` — `/Applications/Claude.app`
at 1.37937.1 and a still-registered `/Volumes/Claude/Claude.app` at 1.26832.0,
a mounted installer image four hundred versions behind.

### Confirmed live, not just read

The bundle reading above was tested directly on 2026-08-27, and the app behaved
exactly as the code says.

Method: build a minimal `.app` claiming `claude` in `CFBundleURLTypes` under its
own bundle id, register it, make it the default handler, then launch a Claude
Desktop **profile** through `clauderig desktop open` and re-read the handler.

```
before launch   pref=dev.rigsmith.clauderig.schemeprobe
                resolves=/Users/john/Applications/SchemeProbe.app
after launch    pref=com.anthropic.claudefordesktop
                resolves=/Applications/Claude.app
```

One launch, and the scheme was gone. Note it was a **profile** launch, not the
machine-wide app — so the re-registration is not something a launcher could
dodge by only ever starting profiles. Every path clauderig has to open Claude
Desktop hands the scheme back.

A detail worth keeping for whoever builds a handler bundle for any scheme: the
probe was ignored while it lived in a temp directory. `LSSetDefaultHandlerForURLScheme`
returned success and the `LSHandlers` preference recorded it, but
`NSWorkspace.urlForApplication(toOpen:)` still resolved to Claude — the
preference was written and not honoured. Copying the identical bundle to
`~/Applications` and re-registering made it resolve immediately. Writing the
preference is not evidence that the handoff took; resolution has to be read
separately. (`shortcut_darwin.go` already puts bundles in `~/Applications`, so
it lands on the right side of this by construction.)

### There is no single-instance lock (fatal to the Windows hypothesis)

`requestSingleInstanceLock`, `second-instance`, `makeSingleInstance` and
`hasSingleInstanceLock` are **all absent** from the bundle, `app.asar` and
`app.asar.unpacked` alike. Electron fires `second-instance` only for an app that
requested the lock, so `claude.exe --user-data-dir=<dir> <url>` cannot forward a
URL into the instance already on that directory. It would start a *second*
instance on a live profile — the specific hazard `desktop open` is built to
prevent.

So the hoped-for asymmetry does not exist. Windows is no better than macOS, and
the ambiguity refusal stays on both.

### Nothing can hold the scheme against the app

Three routes were considered for pinning the handler so the app's
re-registration could not win. All are closed.

**Sandboxing does not apply.** Claude Desktop is not sandboxed to begin with —
no `com.apple.security.app-sandbox` entitlement and no container; it ships
hardened and notarized, with `keychain-access-groups` under team `Q6L2SF6YDW`
(which is also what makes the re-signed-copy problem in the branding section
real). More to the point, sandboxing would not help if it were: the App Sandbox
isolates a process's filesystem and IPC, while the LaunchServices handler
database is **per user**. Every process in the session, sandboxed or not,
registers against the same `lsd`.

The boundaries that do give a separate handler database defeat the purpose. A
second macOS user account has its own, but it is also a separate GUI session, so
the profiles stop being side-by-side windows — which is the model. A VM is
complete isolation and far more than the problem is worth.

**A managed preference does not work.** Tested 2026-08-27 on macOS 26, on a
machine with no MDM and no profiles installed. An `LSHandlers` payload naming a
probe bundle for the `claude` scheme was written into
`/Library/Managed Preferences/<user>/` under **both** candidate domains,
`com.apple.LaunchServices` and `com.apple.launchservices.secure`, root-owned.

```
com.apple.LaunchServices:        forced=true  claude->dev.rigsmith.clauderig.schemeprobe
com.apple.launchservices.secure: forced=true  claude->dev.rigsmith.clauderig.schemeprobe
resolves=/Applications/Claude.app
```

`CFPreferencesAppValueIsForced` returned true for both — the managed layer was
working exactly as designed — and LaunchServices resolved to Claude anyway,
before and after re-registering every domain. **The managed preference is
honoured by `cfprefs` and not consulted by LaunchServices**, which resolves from
its own database rather than from CFPreferences at query time. The probe never
won the scheme at all, so the app was never even given the chance to take it
back.

The one caveat, stated rather than overstated: a managed `LSHandlers` might
apply only at login, and a real profile install would normally be followed by
one. But a mechanism that needs a re-login to take effect, and that the app
overwrites on its next launch regardless, is not a foundation.

**And even a win would have been small.** Holding the registration still gives
no per-instance address. The best case was always automating the remedy
`ambiguousRoutingError` already prints — quit the others, then send — at the
cost of a configuration profile on every machine.

### Theme is confirmed (§1 is unaffected)

```js
var B9 = Ja.get(`userThemeMode`);
(B9===`system`||B9===`light`||B9===`dark`) && (o.nativeTheme.themeSource = B9);
```

The accepted values are exactly `system`, `light` and `dark`; the file is **read
at startup** and the value applied to `nativeTheme.themeSource`; and
`setThemeMode` writes the same key back through the app's own store. Writing it
into a closed profile's `config.json` is therefore sufficient, which is what §1
assumed and can now stop assuming.

### What this leaves

`refuseIfRoutingIsAmbiguous` is not a stopgap awaiting a launcher. Given an app
that re-registers the scheme at every launch and takes no instance lock, **it is
the answer**, and the design should stop treating it as a limitation to be
engineered around.

## Design

### The primitive this rests on — moot, kept for the reasoning

Only relevant if §2 is ever revived; the recursion below is real, but it is
downstream of a registration that does not survive a launch.

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

### 2. `claude://` handler registration — **not viable**

**Superseded by the findings above**: the app re-registers itself as the scheme
handler on every launch, so any registration clauderig installs survives only
until Claude Desktop next starts. The design below is kept because it is correct
*given a handler that sticks* — if a future release stops re-claiming, or gains
a policy that separates registration from handling, this is the shape to build.
Nothing here should be started before that changes.

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

**Worth less than it looked.** The findings above remove the deep-link half of
its value: a relocated real bundle goes on claiming `claude://` at every launch,
so links keep routing to the app regardless of what sits in `/Applications`. A
Dock click is all the shim would ever catch — a much thinner return for the
`Installed()` surgery and the update-clobber risk below.

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

A second rejected alternative, because it needs no OS trickery at all and will
therefore come back around: **closing the competing windows automatically**.
Every piece exists — `competingWindows` already enumerates them, including the
profile-less app it currently tells you to close by hand, and `Quit` and
`waitGone` already do the work — so `refuseIfRoutingIsAmbiguous` could offer to
carry out its own remedy and then send. Rejected on judgement rather than
mechanism: those windows hold live conversations, and a confirmation prompt
people learn to click through is not consent. Which windows to close is the
user's decision, because only the user knows what is in them. The refusal names
them and stops there, and that is the right division.

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

1. **Theme**, as a standalone PR. It is the only piece the evidence leaves
   standing, and it is independently useful.
2. Nothing else, until Claude Desktop's re-registration behaviour changes.
   `desktop launcher` should not be built against 1.37937.1.
3. Re-read this document at the next Desktop release that touches deep links.
   The two greps that decide it — `setAsDefaultProtocolClient` and
   `requestSingleInstanceLock` in `app.asar` — take under a minute.

Theme needs a changeset, and reaches `rig ui`, the parent help block,
`site/rig/verbs.md`, and the `rigsmith-tools` skill.

## Open questions

- **Answered.** `userThemeMode` accepts exactly `system`, `light` and `dark`, and
  is read at startup — so writing it into a closed profile is enough.
- **Answered.** Claude Desktop's Windows build takes no single-instance lock, so
  a deep link cannot be delivered into a running profile through `second-instance`.
- **Answered, and it was the decisive one.** The re-registration is not merely
  read from the bundle — the scheme was taken over and Claude Desktop took it
  back on the next profile launch. §2 is closed on evidence, not on inference.
- Should `desktop add` offer a theme at creation time? Theme and the abandoned
  launcher answered the same question — *which profile am I looking at* — and
  theme is now the only one left answering it.
