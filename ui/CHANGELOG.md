# github.com/rigsmith/rigsmith/ui

## 0.2.0
### 🚀 Enhancements

- **clauderig-ui:** The claudeRig UI now ships on its own tag, so a window release no longer waits for a toolchain release.
  
  It has always been a separate module on its own version — 0.x while the command line tools are at 1.x — and `shiprig tag` has always rendered `ui/vX.Y.Z` for it. Nothing consumed that tag: the window rode the CLIs' release, which meant every fix to it waited for one, and the Windows download lived in a release named after a version the app does not have.
  
  Pushing `ui/vX.Y.Z` now fires its own workflow, built from the pieces that already existed. GoReleaser builds and Authenticode-signs the Windows binaries through the same hook the CLIs use, a macOS runner builds, signs and notarizes the `.app` through the same script as before, and the two are published together as one GitHub release, a Homebrew cask and a winget submission. `brew install --cask rigsmith/tap/clauderig-ui` is unchanged; the zip it downloads now sits in a release carrying the window's own version rather than the CLIs'.
  
  A tag that disagrees with `ui/go.mod` is refused before anything is built. The two are written at different moments, and a mismatch would ship a window reporting one number under a tag promising another, with the cask and the winget manifest each believing a different one.
- **clauderig-ui:** The window now tells you when the machine-wide Claude Desktop is launched, because that is the window a session meant for a profile can end up in.
  
  A watch scans the running Claude Desktop processes every ten seconds — five while the notice is up — and raises a small window the moment one appears with no `--user-data-dir`: the ordinary install, the one the Dock and Spotlight start. While it is open a `claude://` deep link is routed by scheme rather than to a chosen window, so **Open in Desktop** can land there instead of the profile that was picked, and `clauderig desktop send --session` refuses rather than guess. Until now the first sign of any of that was a refusal, or a conversation filed under the wrong account.
  
  It is the app's own window rather than an OS notification, and deliberately: a real notification needs a signed bundle and the user's permission, so it would be silent in a dev build and silent again for anyone who ever declined the prompt. A window is the one surface a tray app can always put on screen.
  
  Three decisions about when NOT to speak, each of them a way this could have become noise. A Desktop window already open when the tray starts is not a launch, so starting the app never greets you with a warning about something you have had open all morning. A failed process scan holds the previous answer instead of reading as "closed", which would otherwise make the next successful scan look like a launch. And a machine with no clauderig Desktop profiles is never warned at all — with nothing to route a session to, the main app is simply Claude Desktop.
  
  Raising only on the transition is also what makes dismissing it work: it will not come back until that app is closed and opened again. **Don't warn again** on the notice turns it off for good, and the tray's **Warn when Claude Desktop opens** is the way back on — an off switch whose on switch is a file somebody has to find is not a setting.

### 🩹 Fixes

- **clauderig-ui:** The sessions window no longer hangs on Windows when a CLI store's project folder has to be named from its transcripts.
  
  Recovering the real directory behind a project slug walks up the working directory a transcript recorded, and it stopped when it saw `/`. Windows spells its root `\` or `C:\`, `filepath.Dir` returns those unchanged, and the walk ran forever — the window wedged on the first folder whose name had to be recovered. It now stops where `Dir` stops changing the path, which is the root on every platform.
  
  A group id from the window that begins with `/` is also refused on Windows now. The guard called `filepath.IsAbs`, which is the host's rule: `/etc/passwd` is not absolute on Windows because it names no volume, so an id written to be refused walked straight past it.
- **clauderig-ui:** Two Claude Desktop accounts whose ids begin the same way no longer merge into one folder in the Places view.
  
  The group was keyed by the NAME shown for an account, and with no email on file that name was the account id cut at its first `-`. Two accounts sharing that prefix therefore shared a key: one group, holding both accounts' sessions, carrying one account's id. Clicking it showed only that one account's sessions, because the drawer filters on the id — so the row advertised a count it could not produce, which is the exact failure the split exists to prevent. Groups are now keyed by the account id, and an account with no email on file is named by its id in full: long, but never two accounts under one heading.
  
  A Desktop sidecar that will not parse also keeps its filename as its label. It used to be relabelled `(untitled) <first segment of the filename>`, which presented a slice of a filename as though it were a session id — for `local_broken.json`, a session called "broken". The relabelling is right for a record that parsed and simply had no title; for one we could not read at all, the filename is the only fact there is, and it is also what somebody needs in order to go and look at the file.
- **clauderig-ui:** The claudeRig UI's Windows executable now carries an icon, a version and a description.
  
  `build/winres/` had an entry for each CLI and none for the window, so `scripts/winres.sh` embedded nothing into it: a generic icon in Explorer, an empty properties dialog, and no FileDescription for winget's tooling to read — komac classifies a binary from exactly that field. It shipped that way for its whole life, and nothing in the repo said so.
  
  Its version comes from `ui/go.mod` rather than from `git describe`, which would have answered with whichever tag is newest in the history — usually the CLIs' — and put a different number in the properties dialog than the app reports about itself.

## 0.1.1
### 🩹 Fixes

- **clauderig-ui:** The Homebrew cask no longer warns on install.
  
  It declared its minimum macOS with the string form Homebrew has deprecated, so
  every `brew install --cask clauderig-ui` printed a deprecation notice and asked
  the user to report it. Same requirement, written the way Homebrew now wants.

## 0.1.0
### 🚀 Enhancements

- **clauderig-ui:** Click a row in Sync Activity to see the files that run touched, with added and removed line counts.
  
  Each sync is exactly one commit in the staging repo, so the answer already existed losslessly — it just had no way of being asked. Reading git rather than recording paths in the journal keeps a bounded feed from turning into an inventory of a data set git already holds.
  
  A record and its commit are matched on time: the journal line is written *before* the commit precisely so it travels inside it, which means the sha does not exist yet when the record is made and there is nothing to record. The commit always lands a moment after its own record, so "the earliest commit at or after this timestamp from this machine" is exact. A run that committed nothing says so rather than erroring — that is the ordinary state of a sync with nothing to write, and of the newest record, which is usually still waiting for its own commit.
  
  Long lists are capped at 200 paths and say how many they dropped; a first sync on a new machine would otherwise hand the window several thousand rows.
- **clauderig-ui:** When Open in Desktop is refused because another Desktop window is open, the window now offers to send it anyway.
  
  The refusal is right: a deep link is routed by *scheme*, not to a particular window, so with more than one Desktop window open the OS decides which receives the session — and picking wrong files somebody's conversation under the wrong account. The CLI has always had `--anyway` for the case where you know which window is in front and just want it sent. The window had no way to say so, which left you reading an error explaining a flag you could not reach.
  
  A **Send anyway** button now appears under that particular error, in both the session detail and the full window's drawer, and only after the guarded attempt has actually been refused.
  
  The error itself is scrolled into view when it appears. These messages run to several lines and land at the very bottom of a pane you have usually scrolled, so unscrolled the text is clipped mid-sentence and anything beneath it — including the button — is simply not on screen.
  
  It is deliberately not labelled "open it in the window that's already open". That is the usual outcome and almost certainly what you want, but the mechanism cannot promise it — the OS chooses, and a button that claims otherwise would be making a guarantee out of a likelihood. The tooltip says exactly that. It also does not offer itself twice: once the override has been used, a second identical attempt has nothing new to try.
- **clauderig-ui:** The window has a Health pane: every `clauderig doctor` check, and a button on the ones it can repair.
  
  The same `doctor.Run` the CLI calls, against the same environment — a second implementation would eventually disagree with the command line about whether a machine is healthy, and that is not a disagreement anyone can settle from the outside.
  
  Only the checks that are saying something are shown, with a toggle for the passing ones. A list where the one line worth acting on sits under a dozen reading "ok" is a list nobody reads, which is exactly what happened to session filing when it was added as a check the CLI would print.
  
  Fixes run by check id rather than by name, so rewording a label cannot change what the window is allowed to ask for. A check that can repair itself but has no id gets no button — an offer that could only fail is worse than no offer. Applying one returns a freshly run report, so the pane shows the state after the repair rather than the one that prompted it.
  
  It checks **this machine**, not a repository. `clauderig doctor` is run from inside the repo you mean, so its working directory answers the question; the window has no such directory — it inherits whatever the launcher used, which is a shell's cwd from a terminal and the filesystem root from Finder. Reporting worktree discipline for a repository nobody chose, that changes depending on how the app was started, would be worse than not reporting it, so the pane says where that check lives instead. The global sync hooks stay: they are `~/.claude`, not any repo.
  
  Deliberately not on the status poll. These checks shell out to git and gh, look for binaries on PATH and size the Desktop store; that is far too much to repeat every five seconds for an answer that only changes when something changes. It runs when the window opens, after a fix, and on **Re-check**.
- **clauderig-ui:** Let the window act on the warnings it reports.
  
  A warning that names the command you should go and run has stopped one step
  short — the command is right here. The accounts desync now offers to diagnose
  itself, and an empty account list offers to capture the login you are on.
  
  The button says what it will do rather than "Fix": `doctor --fix` mends what it
  can, `account doctor` only says which account is really being used, and a button
  labelled Fix that merely looks at something is a small lie.
- **clauderig-ui:** Add a Places mode to the sessions window, for finding a session by where it was
  kept rather than by what was in it.
  
  The list answers "which session was that". Places answers "where was I" — it
  walks stores instead of sessions, so you can open a Desktop, a profile, or a
  project and see what is filed there. It shows the things a session list cannot:
  a Desktop sidecar for a session whose conversation lives in the CLI tree (with
  the link to that transcript), the same store live and synced side by side, and
  the config each profile carries beyond its sessions.
- **clauderig-ui:** The Accounts panel can now start Claude Code or Claude Desktop as any account.
  
  Each row gains a terminal button and, where a Desktop profile is bound to that account, a window button. The terminal one runs `clauderig account run <id>` — which scopes the credential to that one terminal rather than changing the machine-wide login — so starting a second account does not sign the first out, and the live-session guard never enters into it. The Desktop one runs `clauderig desktop open <profile>`, which focuses an existing window rather than launching a second.
  
  That distinction is the point: **launching is not switching**. The Switch button still changes the machine-wide login and is still refused while Claude Code is running, because that operation genuinely cannot be done underneath a live session. Neither launch button is guarded, because neither needs to be — the whole reason `account run` and Desktop profiles exist is that they keep accounts apart without anything being swapped.
  
  Which profile belongs to which account comes from the binding recorded when the profile was created, and whether it is already open is a process check. Both are best-effort: Desktop is a separate application with its own login, a machine with no profiles is the ordinary case, and a failure to read either costs the buttons rather than the accounts list they sit beside. An unreadable process scan is treated as "closed" — the button then says "open", and opening an already-open profile focuses it, so guessing that way is harmless.
- **clauderig-ui:** The claudeRig UI ships as a signed, notarized macOS app: `brew install --cask rigsmith/tap/clauderig-ui`.
  
  It could not ride the existing release path, and the reason is worth recording. The four CLIs cross-compile from Linux and quill signs the bare Mach-O binaries, which is what keeps a release cheap — no macOS runner at all. The UI breaks every one of those assumptions: it needs cgo, so a real macOS runner; it ships as a bundle rather than a binary; and quill signs Mach-O files, not bundles.
  
  What it does **not** need is any new signing work. It uses the same Developer ID certificate and the same App Store Connect key the notarize block already feeds to quill, through `codesign` and `xcrun notarytool` instead. A second consumer of the credentials that exist, not a second identity.
  
  So it is a separate job on `macos-latest` that runs after the release is cut, rather than moving the whole release onto macOS. Keeping the CLI builds on Linux is what makes them cheap, and a failure packaging the app must not take the four binaries down with it.
  
  The bundle is universal (arm64 + amd64 via `lipo`), so one download serves both architectures and the cask needs no arch logic. It declares `LSUIElement`, which is the Info.plist half of the `ActivationPolicyAccessory` the app already sets at runtime — declaring it in both stops a Dock icon flashing up before the app gets to say otherwise. The icon is generated at package time from `design/marks/png/app-claudeRig-512.png` rather than committed as an `.icns`, because `design/` is the single source for the brand and a binary copy checked in beside it silently stops matching.
  
  The cask is written by the same job from the zip's real checksum. GoReleaser publishes the other casks from its own artifacts and cannot publish this one — the app is built after that job has finished, so its checksum does not exist when the casks are written. There is no `depends_on` linking it to the `clauderig` cask: the app reads the sync repo directly and is usable on its own, and forcing the CLI on someone who wanted a menu bar app is the coupling a separate cask exists to avoid.
  
  Everything degrades to a skip rather than a failure when a secret is absent — unsigned, un-notarized, no cask — the same stance the existing notarize block takes, so this is safe to ship before the tap token is wired.
  
  Separately, CI now installs `libgtk-3-dev` and `libwebkit2gtk-4.1-dev` on the Linux leg. `go test ./...` could not compile `./ui` there at all, so that job was red the moment the UI landed. The app only ships for macOS, but keeping it inside `./...` means a change that breaks another platform is caught here rather than by whoever tries to port it.
- **clauderig-ui:** Show a session's full detail in the Places panel, and add a layout probe.
  
  Clicking a session in Places now fills the third panel with what the sliding
  drawer shows — the same call and the same rendering, so there is one description
  of a session rather than two that drift apart. Sessions with no conversation
  behind them (a Desktop record whose transcript is elsewhere, a deleted one) say
  what the record holds and why there is nothing more.
  
  `go run ./ui/frontend/layout` renders the window in headless Chrome with this
  machine's own data and measures it. jsdom has no layout engine, so it will
  confirm an element exists and is styled and still let a checkbox be 280 pixels
  wide — which is exactly what happened. It skips cleanly where Chrome is absent.
- **clauderig-ui:** Read a session's conversation from the panel, both sides, with a search box.
  
  The detail showed a session's opening and closing prompts with a count of what
  sat between them and no way to reach it. That gap now offers to open the
  conversation out in place — the two ends are the context for the middle.
  
  It shows both sides. Reading back a conversation with one voice removed is not a
  shorter conversation, it is a different and confusing document, so the assistant
  turns come too: prompts sit to the right, answers to the left, each coloured for
  its side. That is as much chat window as this is trying to be.
  
  A search box over the conversation filters it to the turns that mention
  something and marks where, because by that point you are inside one session
  looking for a place in it, not looking for a session.
- **clauderig-ui:** The window's search box searches the whole session, not just what a row shows.
  
  It matched a row's own fields — title, last prompt, project, branch, id, client — and a separate **deep** toggle sent the term to the transcript bodies *instead*. So a word you remembered from a title and a word you remembered from the conversation needed different searches, and you had to know which kind of word you had before you could look for it.
  
  One box now covers both. `sessions.Options` gains `Search`, which keeps a session when its row fields match **or** its transcript body does. The existing `Text` and `Content` remain the halves, and they still AND together — passing a word to both asks for sessions whose title *and* body contain it, which is not what typing a word into a search box means.
  
  The cheap half runs first and a transcript is only opened for rows it missed, so a term that matches a title costs nothing extra. There is a test for exactly that, because it is the difference between a search that reads three files and one that reads seven hundred.
  
  Rows found in the body report it the way `clauderig search` does — the hit count and the first snippet, in place of the project path, since the hit is why the row is there.
  
  **Deep** survives, meaning body-only. That is still worth having when a common word matches half your titles.
  
  Opening a session found that way leads with **where the term appears** — up to a dozen excerpts with the word marked, above the opening and closing prompts. The prompts say what a session was *for*; when you arrived from a search, the excerpts say why it came back, which is the thing you clicked to find out. `Detail` takes the term for this, so the pane titles itself with what was searched rather than the frontend restating what it asked.
  
  Excerpts are built as text nodes, not markup. The text is somebody's conversation, and putting it into the page as HTML would run whatever they happened to have written down.
  
  And searching now says it is working. Opening transcripts is not instant, and a list that sits there unchanged reads as a search that found nothing — the one answer it must not give by accident. The indicator waits 150ms before appearing, so a fast search never flashes it, and it only shows when there is a term: an unfiltered reload is quick, and announcing every background poll would be noise.
- **clauderig-ui:** A session filed in more than one place now says so in its own row, and offers the repair where you are already standing.
  
  The status pane has listed split sessions for a while, but only to someone who thought to go and ask it. The person who needs it is looking at the sessions list, trying to work out why a conversation appears to have lost a week — which is what a split looks like from the outside, because anything resolving by project directory can open the copy that stopped growing.
  
  The row carries a small mark, on the same rule as the store icons beside it: shown only when it is true, so a row with nothing on it is the ordinary case. Opening the session describes both copies — how many records each holds, and how many exist only in the older one — and offers **Keep newest, park the older**, the same repair the status pane offers, moving the older copy to `~/.clauderig/parked` rather than deleting it.
  
  When the copies have genuinely diverged it says so and offers no button. Choosing between them would lose turns, and that is a decision for the person who was there.
  
  The comparison is loaded when you open the session, not when the list is drawn: describing a split reads both transcripts end to end to match them record by record, which is not something to do because a list scrolled past.
- **clauderig-ui:** The clauderig UI has a second window: a sessions manager listing every Claude Code session this machine can see, wherever it lives. Open it from the tray menu (**Sessions…**), or start the app with `--sessions`.
  
  One row per session, merged across the places a session actually leaves a trace — this machine's live `~/.claude`, each Claude Desktop install including clauderig-managed profiles, and the synced staging repo. Each row shows how long ago you used it, the project directory and the branch it ended on, the account it belongs to, the client that ran it (`cli`, `vscode`, `desktop@profile`, an `sdk-*`), its title, and the last thing you typed in it.
  
  The title and the last prompt are separate columns on purpose. The title is the first thing you asked, which is what a session was opened to do; an hour later that is rarely what it became. The last prompt is what makes a row recognisable when you are hunting for the chat you were just in.
  
  A "where" column names which stores hold a transcript, because the lopsided rows are the ones worth seeing: `repo` alone means the session is backed up but not on this Mac, and `cli` alone means it exists only here and has never been synced. The footer says what the list could not cover — sessions with no readable date, sessions with no recorded account, and any machine whose sync is stale enough that its recent work is not listed. A session finder that quietly shows less than it should is worse than one that says so.
  
  Dates come from each transcript's own last record rather than the file's mtime, so restoring a backup or checking out the synced tree does not re-date every chat to the same instant. The few that can only be dated from a file or an old ledger row are marked `~`.
  
  Click a row and a panel opens with the first and last couple of prompts, so you can tell one session from another without opening it, plus the things you actually want to do next.
  
  **Resume** works two ways. *Open in terminal* runs the resume command for you; *Copy command* hands you `cd <project> && claude --resume <id>` for any terminal, multiplexer or other machine. *Open in Desktop* imports the session into a Claude Desktop profile you pick — only clauderig-managed profiles are offered, never the machine-wide install, because sending a session there files it under whichever account that install happens to be logged into. All three are disabled unless the transcript is in this Mac's `~/.claude`, since that is the only copy any of them can read.
  
  **Delete** asks first, and asks properly: the dialog lists the stores that actually hold the session and you tick which to remove, defaulting to this Mac only. It tells you which way the asymmetry falls — keeping the synced copy means a restore can bring the session back, removing it means the deletion reaches your other machines on the next sync. A session a running Claude Code process is writing to is refused outright rather than deleted underneath it. The ledger's record that the session existed is kept; only the conversation goes.
  
  **Search** starts with the box at the top, which matches anything a row shows — title, last prompt, project, branch, id, client. When that finds nothing, the empty state offers to look deeper, and **search inside** (or just pressing Enter) runs the same content scan `clauderig search` does, over the transcripts themselves. Matching rows show the hit count and the first snippet in place of the last prompt, because the hit is why the row is there. Both filter server-side, so they search the whole window rather than the rows that happen to be loaded.
  
  **Open** reopens a session where it was last used, read off the client its transcript recorded — `Open in Terminal`, `Open in VS Code`, `Open in Desktop · <profile>` — with the alternatives behind an `Other…` picker beside it. VS Code resumes the actual session rather than just opening the folder: the extension registers a `vscode://anthropic.claude-code/open?session=…` handler, so the window opens the project and then hands it the session. All of them need the transcript to be on this Mac, since that is the only copy any of them can read.
  
  The status window gains a **Sessions** button, both windows can be dragged by their header, and `--sessions` opens the manager at startup the way `--window` opens the status window.
  
  The menu bar window is the one you live in. It holds both views behind a single toggle — sync status, and your sessions — with a session's detail opening in place rather than sending you elsewhere. It hides when you click away, the way a menu bar window should, and refreshes every five seconds while it is open rather than every forty-five: if you are looking at it, you are looking for something. The full sessions window is still there for the work that needs room — filters, searching inside transcripts, deleting — one click from the pop-out icon, and opening a session from the popup carries it across so you land on what you were reading.
  
  A session the sync holds but this Mac does not now offers **Bring to this Mac**, which copies it over so it can be opened here.
  
  The status window catches up with its own documentation: the health banner's advice is now a button — Sync now, Pull, or Resolve, chosen from the same reason the banner reports, so the two cannot disagree — with the CLI's output streaming into a pane beneath it. That matters for the failure this project started from: a sync refusing on a secret tripwire used to say so only in hook stderr, where nobody saw it. Below that, the accounts you have captured, with the live one marked and a switch button that stays disabled while Claude Code is running, naming the processes holding it up rather than letting the CLI's refusal look like a bug.
  
  Underneath, the listing moved out of the `recent` command into a package both front ends read, so `clauderig recent` and the window answer with the same facts rather than two implementations that drift.
- **clauderig-ui:** The window is its own module, at its own version.
  
  It is a different product from the command line tools beside it — new, moving
  fast, and at 0.x while they are at 1.x — and a single version line would either
  hold it back or drag them forward. `ui/` is a Go module now, with `go.work`
  tying it to the packages it imports, and the release stamps it from its own
  version rather than from the tag.
- **clauderig-ui:** The window gains a **Repository** panel: what the sync repo costs, what it is made of, and the two ways to make it smaller.
  
  The window grows a **Repository** panel carrying the same numbers, with the ratio flagged amber once history costs four times the content, and a prune dialog offering a week, a month or three, and a Repack button beside it. Both report progress inside the panel rather than in the banner at the top of the pane — repacking gigabytes takes a minute, and a message that far from what you are looking at is a message nobody reads; silence for a minute reads as a button that did nothing.
  
  Finishing either one refreshes the rest of the window, which the poll would not have done on its own: the journal has not moved, so the activity feed's repaint check would keep the stale render, and the status line's last sync is read from a commit a prune has just replaced. A prune also clears the cached commit file lists, since it rebuilds every commit above the cutoff and every cached id is then a lie.
  
  The window shows the same split with a bar per row, because the shape of this data is one line at 97% and everything else rounding to zero — a column of numbers makes you work that out, a bar does not. Hovering a row names what it is and how many files it covers.
- **clauderig-ui:** Re-filing a session is a button in the window, beside Open and Delete.
  
  It asks for the directory and nothing else — where a conversation belongs is a judgement only the person who was there can make — and always dry-runs first, so the confirmation names a real number before a file is written.
  
  The window's dialog offers both a text field and a **Browse…** button onto the system folder picker. Typing is faster when you know the path and can paste it; the picker is the only way to be certain the directory exists, and a typo here does not fail — it files the session somewhere plausible and wrong. The picker opens at the session's current directory, walking up if that folder has since been deleted, so the common case of moving a session one level down opens next to the answer.
  
  The picker is attached to the window, so macOS runs it as a sheet rather than a free-floating panel. Unattached it opened *behind* the window that asked for it — the tray window is raised on reveal and nothing put the dialog above it. A sheet is always in front of its parent and moves with it, which also makes it obvious which question is being answered.
  
  The tray window's auto-hide stands down while a system dialog is up. It hides on focus loss, which is what a menu bar window should do — but a native dialog takes focus, so opening the picker dismissed the window that asked for it and the form the answer was going into. The window consults a shared flag rather than the picker reaching in to disable the behaviour, so any dialog added later is covered by construction.
- **clauderig-ui:** The window shows sessions filed in more than one place, and offers to resolve them.
  
  A banner appears above the Repository panel when `clauderig` finds any — silent otherwise, because a panel that says "all good" every time is a panel people stop reading, and this one has to be noticed the once it matters.
  
  In the window the banner expands: **Show them** lists each split session by title, with both copies side by side — the project each sits in, how many records it holds, its date range, and how many records exist *only* in the older one. Where the older copy is wholly contained in the newer, one button parks it.
  
  The panel is only rebuilt when the finding actually changes. The status poll runs every five seconds while the window is open, and this data moves only when a session is filed or fixed — repainting regardless threw the expanded list away underneath whoever had just opened it. Fixing one keeps the list open, since collapsing it the moment you resolve one of four is not a reward for resolving it.
  
  Describing a split reads *both* transcripts and compares them record by record, so it is not done on the status refresh — the banner is cheap, the list is asked for.

### 🩹 Fixes

- **clauderig-ui:** One repaint guard for the whole window, instead of the same fix written twice and missing twice.
  
  The status poll runs every five seconds while the window is open, and most panels answer questions that move far more slowly. Rebuilding regardless tears down whatever the reader is in the middle of, and it did — three times, found separately: the activity feed threw away an expanded file list, the session detail dropped what it was showing, and the split-session list vanished a few seconds after **Show them**.
  
  `changed(key, projection)` now decides, and every panel that repaints on the poll asks it: activity, session filing, accounts and the repository.
  
  The last two had never shown the fault but had the same exposure. Accounts carries the tick a launch button flashes for two seconds and the disabled Switch while Claude Code is running; the repository panel has buttons that sit disabled through a repack. A poll landing mid-operation wipes both.
  
  Callers pass a **projection** rather than the whole payload, which is the part worth getting right. Most of these objects carry something that churns — `.git` grows on every sync, timestamps advance — and comparing everything would report a change on every poll: the same bug wearing a disguise. The repository panel compares its numbers *as displayed*, so reclaiming 2 GB repaints and adding a few KB does not.
  
  `force` skips the check for when a panel's own action changed the data and the repaint is the point, and `repaint(key)` forgets a signature for when something outside the data invalidated the screen — a prune rewrites every commit id under an activity feed whose journal entries have not moved.
- **clauderig-ui:** **Open in Terminal** and **Run as this account** work on Windows and Linux, not only macOS.
  
  Both refused outright with "macOS-only for now". That was never a platform limitation — it was that I had written one implementation: a `.command` script (a macOS convention Terminal.app executes on open) launched with `open -a`, neither of which exists elsewhere. Two of the window's actions simply failed on two of three platforms.
  
  They now go through one launcher with a real implementation per platform. It takes the command as separate arguments and the directory separately, rather than one pre-built string, because quoting is per-platform: a POSIX shell wants single quotes and a batch file wants double ones plus its own escape for `%`. Handing each platform the pieces lets it build something its own shell will actually parse.
  
  - **macOS** — a `.command` opened with `open -a`, honouring `CLAUDERIG_TERMINAL` and defaulting to Terminal.
  - **Windows** — a `.cmd` batch file, opened in Windows Terminal when it is installed and cmd.exe otherwise. `cd /d`, because plain `cd` will not cross drives and a project on `D:` is exactly the case that hits. `%` is escaped, since a batch file expands `%VAR%` and a path containing a percent sign would otherwise silently lose part of itself.
  - **Linux and BSD** — probes `x-terminal-emulator` first (on Debian and Ubuntu that *is* the user's choice, expressed through alternatives), then GNOME Terminal, Konsole, xfce4-terminal, kitty, Alacritty, WezTerm and xterm. There is no `open -a` equivalent — no registered default terminal — so looking for what exists is the only honest approach. It says so plainly when none is found rather than failing obscurely.
  
  `CLAUDERIG_TERMINAL` is honoured everywhere, so someone running Ghostty on a Mac and Windows Terminal on a PC can say so once in one variable.
  
  **Copy command** is quoted for the shell of the machine reading it. It exists to be pasted into a terminal, and a POSIX-quoted line pasted into `cmd.exe` is not a command, it is a syntax error.
  
  The UI also ships for Windows now, as `clauderigUi_<version>_windows_<arch>.zip`. Wails reaches WebView2 through syscall rather than cgo there, so it cross-compiles from the same Linux runner as the CLIs and Authenticode-signs through the same script — no separate job, unlike macOS where the `.app` needs a bundle and `codesign`. It gets its own archive rather than riding in the four-CLI bundle: someone installing the command line tools has not asked for a desktop app.
  
  One message also stopped saying "this Mac" when it meant "this machine".
- **clauderig-ui:** A round of fixes to the clauderig window.
  
  **No more white flash when a window opens.** Neither window set a background colour, so Wails applied `RGBA`'s zero value — fully transparent — and there was nothing painted behind the webview's first frame. Setting the window colour alone was not enough: WKWebView is created opaque white and Wails never sets the webview's own colour (`webviewSetBackgroundColour` exists in its darwin bindings with no caller), so the white sat on top. Both windows now use a transparent backdrop with the palette's ink re-applied once the app is running, which is the first point a native window exists and the only point after the backdrop has had its say.
  
  **Sessions rows read at a glance.** The three store labels — `here-cli here-desk sync` on every row — became glyphs, and in the tray window they appear only when a session is *not* in the ordinary state, so a row with an icon on it is one worth looking at. The space bought a column for the client that last ran the session, which is what decides where **Open** will take you. Accounts get a monogram taken from the account's domain rather than its address — two accounts belonging to one person are `john@` twice — coloured from its own first letter, so `b` is blue and `r` is red and the mapping needs no legend.
  
  **Deleting asks in a dialog.** The per-store confirm is several times the height of the row it replaced, so on a long session detail it opened below the fold and had to be scrolled to. It is now centred over a scrim, always whole. Escape and a click outside both cancel, and focus lands on Cancel rather than Delete.
  
  **The activity feed stops repeating itself and stops losing your place.** Consecutive identical runs from one machine fold into a single row with a count — failures and tripwire refusals never fold, since each of those is its own event. The feed also skips the repaint entirely when nothing has changed, which it was doing four times a minute, throwing away whatever you had open in it.
  
  **An open session detail refreshes when the window does.** The list already reloaded on focus, but a detail never did — and that is the pane you leave the window *from*, via Open in Desktop or VS Code, and what those change is exactly what it is showing. Moving a session between accounts now shows its new account when you come back rather than up to thirty seconds later.
  
  Also: the dropdown no longer draws the macOS focus ring over its own styling, the retention figure in `clauderig sync` reads `N too old` rather than `N aged out` because nothing is removed at that point, and the status line drops the trailing `— clauderig sync: <this machine>` that made it wrap, keeping it only when the last commit came from a different machine.
- **clauderig-ui:** Three more from running the window on Windows.
  
  **No taskbar button for the tray popover.** A popover is not a program you alt-tab to, and it had a taskbar button showing a thumbnail of a window that vanishes the moment you click away from it. macOS says this with `ActivationPolicyAccessory` and `LSUIElement`, which keep it out of the Dock; `HiddenOnTaskbar` is the Windows half of the same statement. The sessions window keeps its button — it is a real window you leave open and come back to, which on Windows means the taskbar and alt-tab, the same reason it keeps its native frame.
  
  **The white flash.** Unlike macOS, Wails already handles this properly on Windows: it paints `WM_ERASEBKGND` with the configured colour and sets WebView2's background at creation, so the window was never the problem. `WindowsWindow.Theme` defaults to `SystemDefault`, though, so on a light-themed machine the frame and WebView2's pre-paint background come up light — the one surface CSS cannot reach. Both windows declare `Theme: Dark` now, which is right on its own merits since this UI is dark whatever the system says.
  
  **`Failed to unregister class Chrome_WidgetWin_0. Error = 1412` on exit.** That is `ERROR_CLASS_HAS_WINDOWS`: Chromium unregistering its window class while windows still exist. Both windows host a WebView2, and both cancel every close and hide instead — the tray is the app, so closing a window must never quit it. The consequence was that both were still alive when the process exited. Quitting now sets a flag that lets the close hooks through, closes both windows, and only then quits.
  
  The tray icons are also rendered at 64px rather than 32. `SM_CXSMICON` is 16, 20, 24 or 32 depending on the display's scaling, and Wails scales one image to whatever that is — 64 divides evenly into 16 and 32 and resamples gracefully to the awkward sizes a 125% or 150% display asks for.
- **clauderig-ui:** Four things the first Windows run turned up.
  
  **The font stack named only Apple's faces.** `ui-monospace` and `SFMono-Regular` mean nothing on Windows or Linux, so both fell through to the generic `monospace` — whatever the engine felt like. It now names Cascadia (ships with Windows Terminal), Consolas (ships with Windows itself) and DejaVu / Liberation for Linux, so every platform gets a face that was actually chosen.
  
  **The tray icon was squashed.** The icons are 44px, which is the macOS retina menu bar slot (22pt @2x). Windows draws the notification area icon at 16, or 32 on a high-DPI display, and scales whatever it is handed — from 44 that is a non-integer ratio, and the mark came out visibly squeezed. There is now a 32px set for Windows. The README used to claim 44 "downsamples cleanly" on Windows; a screenshot from a real machine says otherwise, and it has been corrected rather than left to mislead the next person.
  
  **`ShellNotifyIcon NIM_MODIFY failed (icon not registered)`, twice per launch.** The tray icon was being set while the tray was still being built, and on Windows the notification area icon does not exist until the app runs. It is set on `ApplicationStarted` now, where the tray definitely exists. macOS never minded, but there was no reason to do it early there either — the first poll sets the real level within seconds.
  
  **A native caption bar on a tray popover.** macOS hides the title bar while keeping the traffic lights, which has no Windows equivalent, so the popup arrived looking like a dialog that had wandered out of a settings screen. It is frameless on Windows now. The header was already draggable and clicking away already dismisses it, so the bar was carrying no weight — but "dismissible by clicking away" is not discoverable, so Windows gets an explicit close button, hidden on macOS and Linux where the window frame provides one. The header's 38px top padding exists to clear the traffic lights and is dropped on the platforms that have nothing overlapping.
  
  The sessions window keeps its native frame on Windows. That one is a real window you keep open, and taking its minimise and close buttons away to match the popover would be fidelity for its own sake.
