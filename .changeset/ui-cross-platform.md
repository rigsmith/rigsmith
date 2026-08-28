---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

**Open in Terminal** and **Run as this account** work on Windows and Linux, not only macOS.

Both refused outright with "macOS-only for now". That was never a platform limitation — it was that I had written one implementation: a `.command` script (a macOS convention Terminal.app executes on open) launched with `open -a`, neither of which exists elsewhere. Two of the window's actions simply failed on two of three platforms.

They now go through one launcher with a real implementation per platform. It takes the command as separate arguments and the directory separately, rather than one pre-built string, because quoting is per-platform: a POSIX shell wants single quotes and a batch file wants double ones plus its own escape for `%`. Handing each platform the pieces lets it build something its own shell will actually parse.

- **macOS** — a `.command` opened with `open -a`, honouring `CLAUDERIG_TERMINAL` and defaulting to Terminal.
- **Windows** — a `.cmd` batch file, opened in Windows Terminal when it is installed and cmd.exe otherwise. `cd /d`, because plain `cd` will not cross drives and a project on `D:` is exactly the case that hits. `%` is escaped, since a batch file expands `%VAR%` and a path containing a percent sign would otherwise silently lose part of itself.
- **Linux and BSD** — probes `x-terminal-emulator` first (on Debian and Ubuntu that *is* the user's choice, expressed through alternatives), then GNOME Terminal, Konsole, xfce4-terminal, kitty, Alacritty, WezTerm and xterm. There is no `open -a` equivalent — no registered default terminal — so looking for what exists is the only honest approach. It says so plainly when none is found rather than failing obscurely.

`CLAUDERIG_TERMINAL` is honoured everywhere, so someone running Ghostty on a Mac and Windows Terminal on a PC can say so once in one variable.

**Copy command** is quoted for the shell of the machine reading it. It exists to be pasted into a terminal, and a POSIX-quoted line pasted into `cmd.exe` is not a command, it is a syntax error.

The UI also ships for Windows now, as `clauderigUi_<version>_windows_<arch>.zip`. Wails reaches WebView2 through syscall rather than cgo there, so it cross-compiles from the same Linux runner as the CLIs and Authenticode-signs through the same script — no separate job, unlike macOS where the `.app` needs a bundle and `codesign`. It gets its own archive rather than riding in the four-CLI bundle: someone installing the command line tools has not asked for a desktop app.

One message also stopped saying "this Mac" when it meant "this machine".
