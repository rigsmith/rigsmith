---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

A round of fixes to the clauderig window.

**No more white flash when a window opens.** Neither window set a background colour, so Wails applied `RGBA`'s zero value — fully transparent — and there was nothing painted behind the webview's first frame. Setting the window colour alone was not enough: WKWebView is created opaque white and Wails never sets the webview's own colour (`webviewSetBackgroundColour` exists in its darwin bindings with no caller), so the white sat on top. Both windows now use a transparent backdrop with the palette's ink re-applied once the app is running, which is the first point a native window exists and the only point after the backdrop has had its say.

**Sessions rows read at a glance.** The three store labels — `here-cli here-desk sync` on every row — became glyphs, and in the tray window they appear only when a session is *not* in the ordinary state, so a row with an icon on it is one worth looking at. The space bought a column for the client that last ran the session, which is what decides where **Open** will take you. Accounts get a monogram taken from the account's domain rather than its address — two accounts belonging to one person are `john@` twice — coloured from its own first letter, so `b` is blue and `r` is red and the mapping needs no legend.

**Deleting asks in a dialog.** The per-store confirm is several times the height of the row it replaced, so on a long session detail it opened below the fold and had to be scrolled to. It is now centred over a scrim, always whole. Escape and a click outside both cancel, and focus lands on Cancel rather than Delete.

**The activity feed stops repeating itself and stops losing your place.** Consecutive identical runs from one machine fold into a single row with a count — failures and tripwire refusals never fold, since each of those is its own event. The feed also skips the repaint entirely when nothing has changed, which it was doing four times a minute, throwing away whatever you had open in it.

**An open session detail refreshes when the window does.** The list already reloaded on focus, but a detail never did — and that is the pane you leave the window *from*, via Open in Desktop or VS Code, and what those change is exactly what it is showing. Moving a session between accounts now shows its new account when you come back rather than up to thirty seconds later.

Also: the dropdown no longer draws the macOS focus ring over its own styling, the retention figure in `clauderig sync` reads `N too old` rather than `N aged out` because nothing is removed at that point, and the status line drops the trailing `— clauderig sync: <this machine>` that made it wrap, keeping it only when the last commit came from a different machine.
