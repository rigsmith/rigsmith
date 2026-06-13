// Command dev-install creates "<tool>-dev" and "<tool>-wt" launchers that run
// the rigsmith tools from your working tree, independent of whatever stable
// rig/relrig/clauderig you have installed globally.
//
// It is cross-platform: on macOS/Linux it writes POSIX `sh` wrappers, on Windows
// it writes `.cmd` wrappers. It discovers the tools from go.work, so adding a new
// module to the workspace makes new `<tool>-dev`/`<tool>-wt` launchers appear
// here automatically.
//
//	go run ./scripts/dev-install            # install (or refresh) the wrappers
//	RIGSMITH_DEV_BIN=/some/dir go run ./scripts/dev-install
//
// Each `-dev` wrapper *builds* <module> from the repo (Go's build cache keeps
// that fast) to a temp binary and then execs it from YOUR current directory — so
// the tool resolves the workspace where you actually are, not the repo. (`go run
// -C <repo>` would reset the working directory to the repo, making every command
// operate on rigsmith no matter where you ran it.) Edits take effect on the next
// run with no reinstall.
//
// The build source defaults to this repo but is overridable per invocation via
// RIGSMITH_DEV_SRC, so you can test a feature living in a git worktree from any
// other repo. Because every tool shares one workspace, the override is repo-level
// and a single env var spans all tools:
//
//	RIGSMITH_DEV_SRC=/path/to/worktree rig-dev …   # build that tree, run from cwd
//
// The `-wt` wrappers wrap that: `<tool>-wt <branch|path> [args]` resolves a git
// worktree (by branch name or path) and runs that tree's <tool> from your cwd —
// e.g. `rig-wt feat/foo doctor`. Per-source builds go to separate temp dirs so
// the main tree and a worktree never clobber each other's binary.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "dev-install:", err)
		os.Exit(1)
	}
}

func run() error {
	repo, err := findRepoRoot()
	if err != nil {
		return err
	}
	modules, err := workspaceModules(repo)
	if err != nil {
		return err
	}

	bin := installDir()
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", bin, err)
	}

	var made []string
	for _, mod := range modules {
		name := commandName(filepath.Join(repo, mod))
		if name == "" {
			continue // library module (no main package) — nothing to launch
		}
		devPath, wtPath, err := writeWrapper(bin, name, repo, mod)
		if err != nil {
			return err
		}
		made = append(made,
			fmt.Sprintf("  %-18s build %s from cwd (override tree via RIGSMITH_DEV_SRC)", filepath.Base(devPath), name),
			fmt.Sprintf("  %-18s run a worktree's %s: %s <branch|path> [args]", filepath.Base(wtPath), name, name),
		)
	}

	if len(made) == 0 {
		return fmt.Errorf("no runnable tools found in %s/go.work", repo)
	}
	fmt.Printf("Installed %d dev launcher(s) in %s:\n", len(made), bin)
	fmt.Println(strings.Join(made, "\n"))
	printPathHint(bin)
	return nil
}

// findRepoRoot walks up from the working directory to the dir containing go.work.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.work found above the current directory; run this from inside the rigsmith repo")
		}
		dir = parent
	}
}

var useEntry = regexp.MustCompile(`(?m)^\s*(?:use\s+)?(\./[^\s()]+)`)

// workspaceModules returns the module directories listed in go.work's use block,
// as repo-relative slash paths (e.g. "cli", "release").
func workspaceModules(repo string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(repo, "go.work"))
	if err != nil {
		return nil, err
	}
	var mods []string
	for _, m := range useEntry.FindAllStringSubmatch(string(data), -1) {
		mod := strings.TrimPrefix(filepath.ToSlash(m[1]), "./")
		if mod == "scripts/dev-install" {
			continue // don't make a launcher for the installer itself
		}
		mods = append(mods, mod)
	}
	return mods, nil
}

var commandDoc = regexp.MustCompile(`(?m)^// Command (\S+)`)

// commandName reads "// Command <name>" from the module's main.go. Returns ""
// when the module has no main.go (a library).
func commandName(moduleDir string) string {
	data, err := os.ReadFile(filepath.Join(moduleDir, "main.go"))
	if err != nil {
		return ""
	}
	if m := commandDoc.FindStringSubmatch(string(data)); m != nil {
		return m[1]
	}
	return ""
}

func installDir() string {
	if dir := os.Getenv("RIGSMITH_DEV_BIN"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".local", "bin")
}

// Launcher templates. Placeholders ({{NAME}}, {{REPO}}, {{MOD}}, {{DEV}},
// {{PICKER}}) are substituted by writeLauncher; on Windows it also rewrites \n
// to \r\n. Using raw literals keeps the shell/batch readable (no %%-escaping)
// and the `%` in the .cmd files literal.
const (
	devWrapperSh = `#!/bin/sh
# rigsmith dev launcher for {{NAME}} — builds live source from the working tree
# and runs it from YOUR current directory. Generated by scripts/dev-install.
# Build source defaults to this repo; set RIGSMITH_DEV_SRC to build another tree
# (e.g. a git worktree) — see {{NAME}}-wt. Per-source builds get separate dirs.
# (go run -C would reset cwd to the repo, resolving the wrong workspace.)
set -e
src="${RIGSMITH_DEV_SRC:-{{REPO}}}"
slug="$(printf %s "$src" | tr -c 'A-Za-z0-9' _)"
dir="${TMPDIR:-/tmp}/rigsmith-dev/$slug"
mkdir -p "$dir"
go build -C "$src" -o "$dir/{{NAME}}" "./{{MOD}}"
exec "$dir/{{NAME}}" "$@"
`

	wtWrapperSh = `#!/bin/sh
# rigsmith worktree launcher for {{NAME}} — runs a git worktree's source from
# YOUR current directory. Generated by scripts/dev-install.
# Usage: {{NAME}}-wt [<branch|worktree-path>] [args...]
#   With no branch/path (or a lone "--"), pick a worktree interactively.
set -e
case "${1:-}" in
	-h|--help)
		echo "usage: {{NAME}}-wt [<branch|worktree-path>] [args...]" >&2
		echo "  no branch/path (or '--'): pick a worktree interactively" >&2
		exit 0
		;;
	""|--)
		[ "${1:-}" = "--" ] && shift
		src="$("{{PICKER}}" worktree pick --repo "{{REPO}}")" || exit $?
		;;
	*)
		target="$1"
		shift
		src="$(git -C "{{REPO}}" worktree list --porcelain | awk -v b="refs/heads/$target" '/^worktree /{p=substr($0,10)} $0=="branch "b{print p; exit}')"
		if [ -z "$src" ] && [ -d "$target" ]; then src="$target"; fi
		if [ -z "$src" ]; then
			echo "{{NAME}}-wt: no worktree for branch or path '$target'" >&2
			echo "known worktrees:" >&2
			git -C "{{REPO}}" worktree list >&2
			exit 1
		fi
		;;
esac
exec env RIGSMITH_DEV_SRC="$src" "{{DEV}}" "$@"
`

	devWrapperCmd = `@echo off
rem rigsmith dev launcher for {{NAME}} — builds live source from the working tree
rem and runs it from YOUR current directory. Generated by scripts/dev-install.
rem Build source defaults to this repo; set RIGSMITH_DEV_SRC to build another
rem tree (e.g. a git worktree) — see {{NAME}}-wt.
setlocal
set "SRC=%RIGSMITH_DEV_SRC%"
if not defined SRC set "SRC={{REPO}}"
set "SLUG=%SRC%"
set "SLUG=%SLUG::=_%"
set "SLUG=%SLUG:\=_%"
set "SLUG=%SLUG:/=_%"
set "SLUG=%SLUG: =_%"
set "RIGDEVDIR=%TEMP%\rigsmith-dev\%SLUG%"
if not exist "%RIGDEVDIR%" mkdir "%RIGDEVDIR%"
go build -C "%SRC%" -o "%RIGDEVDIR%\{{NAME}}.exe" "./{{MOD}}" || exit /b %ERRORLEVEL%
"%RIGDEVDIR%\{{NAME}}.exe" %*
`

	wtWrapperCmd = `@echo off
rem rigsmith worktree launcher for {{NAME}} — runs a git worktree's source from
rem YOUR current directory. Generated by scripts/dev-install.
rem Usage: {{NAME}}-wt [^<branch^|worktree-path^>] [args...]
rem   With no branch/path (or a lone "--"), pick a worktree interactively.
setlocal enabledelayedexpansion
set "SRC="
if "%~1"=="" goto pick
if "%~1"=="--" (shift & goto pick)
set "TARGET=%~1"
for /f "tokens=1,*" %%a in ('git -C "{{REPO}}" worktree list --porcelain') do (
	if "%%a"=="worktree" set "WT=%%b"
	if "%%a"=="branch" if "%%b"=="refs/heads/!TARGET!" set "SRC=!WT!"
)
if not defined SRC if exist "%TARGET%\" set "SRC=%TARGET%"
if not defined SRC (echo {{NAME}}-wt: no worktree for branch or path "%TARGET%">&2& exit /b 1)
set "REST=%*"
set "REST=!REST:*%TARGET%=!"
goto run
:pick
for /f "delims=" %%p in ('""{{PICKER}}" worktree pick --repo "{{REPO}}""') do set "SRC=%%p"
if not defined SRC exit /b 1
set "REST=%*"
if defined REST set "REST=!REST:*-- =!"
if "!REST!"=="--" set "REST="
:run
set "RIGSMITH_DEV_SRC=%SRC%"
call "{{DEV}}" !REST!
`
)

// writeWrapper writes the `-dev` and `-wt` launchers for one tool and returns
// their paths.
func writeWrapper(bin, name, repo, mod string) (devPath, wtPath string, err error) {
	var devTmpl, wtTmpl string
	if runtime.GOOS == "windows" {
		devPath = filepath.Join(bin, name+"-dev.cmd")
		wtPath = filepath.Join(bin, name+"-wt.cmd")
		devTmpl, wtTmpl = devWrapperCmd, wtWrapperCmd
	} else {
		devPath = filepath.Join(bin, name+"-dev")
		wtPath = filepath.Join(bin, name+"-wt")
		devTmpl, wtTmpl = devWrapperSh, wtWrapperSh
	}
	pickerName := "clauderig-dev"
	if runtime.GOOS == "windows" {
		pickerName = "clauderig-dev.cmd"
	}
	repl := map[string]string{
		"{{NAME}}":   name,
		"{{REPO}}":   repo,
		"{{MOD}}":    mod,
		"{{DEV}}":    devPath,                        // -wt execs the sibling -dev launcher
		"{{PICKER}}": filepath.Join(bin, pickerName), // -wt's no-arg picker (clauderig owns worktrees)
	}
	if err = writeLauncher(devPath, devTmpl, repl); err != nil {
		return "", "", err
	}
	if err = writeLauncher(wtPath, wtTmpl, repl); err != nil {
		return "", "", err
	}
	return devPath, wtPath, nil
}

// writeLauncher renders a template (placeholder substitution, plus CRLF on
// Windows) and writes it as an executable file.
func writeLauncher(path, tmpl string, repl map[string]string) error {
	body := tmpl
	for k, v := range repl {
		body = strings.ReplaceAll(body, k, v)
	}
	if runtime.GOOS == "windows" {
		body = strings.ReplaceAll(body, "\n", "\r\n")
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

func printPathHint(bin string) {
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p == bin {
			return // already on PATH
		}
	}
	fmt.Printf("\nNote: %s is not on your PATH. Add it:\n", bin)
	if runtime.GOOS == "windows" {
		fmt.Printf("  setx PATH \"%%PATH%%;%s\"\n", bin)
	} else {
		fmt.Printf("  export PATH=\"%s:$PATH\"   # add to ~/.zshrc or ~/.bashrc\n", bin)
	}
}
