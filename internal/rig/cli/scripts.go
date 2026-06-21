package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/envstack"
	"github.com/rigsmith/rigsmith/core/shellrun"
	"github.com/rigsmith/rigsmith/internal/rig/config"
	"github.com/rigsmith/rigsmith/internal/rig/detect"
	"github.com/spf13/cobra"
)

// scriptVerbAnnotation marks the commands surfaced from a conventional scripts/
// or cmd/ directory. They are excluded from rig's verb prefix-matching (so a
// typo like `rig dev` can't expand into a repo-provided `dev-install`); the full
// name is always required to run one.
const scriptVerbAnnotation = "rigScriptVerb"

// isBuiltinVerb is the set of names rig owns, so custom commands and surfaced
// package.json scripts never shadow a built-in verb.
var isBuiltinVerb = map[string]bool{
	"build": true, "test": true, "run": true, "format": true, "lint": true,
	"typecheck": true, "rebuild": true, "install": true, "ci": true, "add": true,
	"uninstall": true, "outdated": true, "upgrade": true, "clean": true,
	"global": true, "dlx": true, "coverage": true, "kill": true, "doctor": true,
	"info": true, "ui": true, "cd": true, "init": true, "watch": true,
	"publish": true, "default": true, "setup": true, "completion": true,
	"config": true,
}

// scriptEntry is one runnable script rig surfaces: a .rig.json custom command, a
// package.json script, or a Go scripts//cmd verb. It is the shared source for
// both the top-level `rig <name>` subcommands (scriptEntryCmds) and the `run`
// picker's Scripts group (discoverScripts), so a script runs identically however
// it is invoked. eco/loc populate the picker's ecosystem and path columns.
type scriptEntry struct {
	name        string // the verb name
	eco         string // source: "custom", "node", "go"
	loc         string // where it is defined, for the picker's path column
	short       string // the cobra command's help line
	annotations map[string]string
	run         func(cmd *cobra.Command, args []string) error
}

// scriptEntryCmds turns script entries into rig subcommands. Unknown flags fall
// through to the underlying command while rig's own --dry-run/--quiet still bind.
func scriptEntryCmds(entries []scriptEntry) []*cobra.Command {
	var cmds []*cobra.Command
	for _, e := range entries {
		e := e
		cmds = append(cmds, &cobra.Command{
			Use:                e.name,
			Short:              e.short,
			Annotations:        e.annotations,
			FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
			RunE: func(cmd *cobra.Command, args []string) error {
				return e.run(cmd, args)
			},
		})
	}
	return cmds
}

// discoverScripts aggregates every runnable script at root for the `run` picker,
// applying the same precedence as the command wiring: a custom command wins over
// a package.json script of the same name, which wins over a Go scripts//cmd
// verb. Built-in verbs are already excluded by each source.
func discoverScripts(root string, cfg config.Config) []scriptEntry {
	var out []scriptEntry
	seen := map[string]bool{}
	add := func(entries []scriptEntry) {
		for _, e := range entries {
			if seen[e.name] {
				continue
			}
			seen[e.name] = true
			out = append(out, e)
		}
	}
	add(customScripts(cfg))
	add(nodeScripts(root))
	add(goScripts(root))
	return out
}

// customCmds turns each .rig.json "commands" entry into a rig subcommand.
// A string entry runs through the shell (`sh -c`), an argv array is exec'd
// directly, and the object form applies its per-OS override (macos | windows |
// linux), per-command env, and cwd. Names that collide with a built-in verb
// are skipped so the dev loop always wins.
func customCmds(cfg config.Config) []*cobra.Command {
	return scriptEntryCmds(customScripts(cfg))
}

// customScripts builds the script entries for the .rig.json custom commands.
func customScripts(cfg config.Config) []scriptEntry {
	if len(cfg.Commands) == 0 {
		return nil
	}
	names := make([]string, 0, len(cfg.Commands))
	for name := range cfg.Commands {
		names = append(names, name)
	}
	sort.Strings(names)

	loc := ".rig.json"
	if cfg.Path != "" {
		loc = filepath.Base(cfg.Path)
	}
	var entries []scriptEntry
	for _, name := range names {
		if isBuiltinVerb[name] {
			continue
		}
		name, def := name, cfg.Commands[name]
		entries = append(entries, scriptEntry{
			name:  name,
			eco:   "custom",
			loc:   loc,
			short: customShort(name, def),
			run: func(cmd *cobra.Command, args []string) error {
				cwd, _ := os.Getwd()
				root := resolveRoot(cwd)
				return runCustom(cmd, cfg, root, name, def, args)
			},
		})
	}
	return entries
}

// customShort picks the help line for a custom command: its description if it
// has one, otherwise the shell string (legacy behavior), otherwise the argv.
func customShort(name string, def *config.Command) string {
	if def.Description != "" {
		return def.Description
	}
	if spec := def.Resolve(); spec != nil {
		if spec.IsShell() {
			return "Custom command: " + spec.Shell
		}
		return "Custom command: " + strings.Join(spec.Argv, " ")
	}
	return "Custom command: " + name
}

// runCustom executes one custom command: resolves the spec for the current OS
// (a clean error when none applies), applies the command's cwd (relative to
// the repo root) and env (layered over .rig.json `env` and the ambient
// environment), then shells out or execs per the spec's form. Extra CLI args
// are appended.
func runCustom(cmd *cobra.Command, cfg config.Config, root, name string, def *config.Command, args []string) error {
	spec := def.Resolve()
	if spec == nil {
		return fmt.Errorf("command %q has no command defined for this OS", name)
	}

	dir := root
	if def.Cwd != "" {
		dir = filepath.Join(root, def.Cwd)
	}
	env := customEnv(cfg, def.Env)

	if spec.IsShell() {
		// A shell-string command runs cross-platform by default: through the
		// in-process portable shell, so one command line works on every OS. The
		// "system" mode (config-level or per-command) opts back into the OS
		// shell for scripts that need a real userland or OS-specific syntax.
		mode, err := shellrun.ShellMode(coalesceShell(def.Shell, cfg.Shell))
		if err != nil {
			return fmt.Errorf("command %q: %w", name, err)
		}
		if mode == shellrun.ShellPortable {
			return runPortableIn(cmd, dir, env, portableLine(spec.Shell, args))
		}
		display, argv := shellInvocation(spec.Shell, args)
		return runIn(cmd, dir, env, display, argv...)
	}

	if len(spec.Argv) == 0 {
		return fmt.Errorf("command %q has an empty argv", name)
	}
	argv := append(append([]string{}, spec.Argv...), args...)
	return runIn(cmd, dir, env, strings.Join(argv, " "), argv...)
}

// coalesceShell picks the command's own shell override, falling back to the
// config-level default (and ultimately "", which ShellMode resolves to
// portable).
func coalesceShell(cmdShell, cfgShell string) string {
	if strings.TrimSpace(cmdShell) != "" {
		return cmdShell
	}
	return cfgShell
}

// portableLine appends the forwarded args to a custom shell-string command,
// POSIX-quoted because the portable shell is POSIX on every OS.
func portableLine(line string, args []string) string {
	if len(args) == 0 {
		return line
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellArg(a)
	}
	return line + " " + strings.Join(quoted, " ")
}

// exitError carries a non-zero exit code from a command run through the
// in-process portable shell. It mirrors *exec.ExitError's ExitCode() so a
// caller extracts the child's code uniformly whichever shell ran it.
type exitError struct{ code int }

func (e *exitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }
func (e *exitError) ExitCode() int { return e.code }

// runPortableIn echoes line, then runs it through the in-process portable shell
// streaming live (so interactive and long-running commands behave like a real
// shell), honoring --dry-run. A non-zero exit surfaces as an *exitError, the
// ExitCode()-bearing parallel to the OS-shell path's *exec.ExitError.
func runPortableIn(cmd *cobra.Command, dir string, env []string, line string) error {
	echo(cmd, line)
	if dryRun {
		return nil
	}
	code, err := shellrun.RunPortable(cmd.Context(), line, dir, env, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	if code != 0 {
		return &exitError{code: code}
	}
	return nil
}

// runIn echoes display, then runs argv in dir with env (nil = inherit),
// honoring --dry-run.
func runIn(cmd *cobra.Command, dir string, env []string, display string, argv ...string) error {
	echo(cmd, display)
	if dryRun {
		return nil
	}
	c := exec.CommandContext(cmd.Context(), argv[0], argv[1:]...)
	c.Dir = dir
	c.Env = env
	c.Stdout = cmd.OutOrStdout()
	c.Stderr = cmd.ErrOrStderr()
	c.Stdin = os.Stdin
	return c.Run()
}

// customEnv builds the spawned process environment with the rig layering
// (low to high): .env/.env.local files, ambient, the config's shared `env`,
// then the command's own `env`. Returns nil (inherit) when nothing applies.
func customEnv(cfg config.Config, extra map[string]string) []string {
	var fileEnv map[string]string
	if cfg.Path != "" {
		fileEnv, _ = envstack.Load(filepath.Dir(cfg.Path))
	}
	if len(fileEnv) == 0 && len(cfg.Env) == 0 && len(extra) == 0 {
		return nil
	}
	return envstack.Environ(envstack.Merge(fileEnv, envstack.Ambient(), cfg.Env, extra))
}

// shellArg quotes a forwarded argument for the shell string form, so args with
// spaces or metacharacters survive the `sh -c` round trip.
func shellArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n\"'`$&|;<>()*?[]#~%{}\\") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellInvocation builds the OS shell run of a custom shell-string command
// with forwarded args appended: POSIX `sh -c` on unix, `cmd.exe /d /s /c` on
// Windows (args caret-escaped per the .NET rig's Exec.WinCmdArguments rules).
func shellInvocation(line string, args []string) (display string, argv []string) {
	if runtime.GOOS == "windows" {
		full := line
		if len(args) > 0 {
			esc := make([]string, len(args))
			for i, a := range args {
				esc[i] = winShellArg(a)
			}
			full = line + " " + strings.Join(esc, " ")
		}
		return full, []string{"cmd.exe", "/d", "/s", "/c", full}
	}
	full := line
	if len(args) > 0 {
		quoted := make([]string, len(args))
		for i, a := range args {
			quoted[i] = shellArg(a)
		}
		full = line + " " + strings.Join(quoted, " ")
	}
	return full, []string{"sh", "-c", full}
}

// scriptCmds surfaces every package.json script (in a Node repo) that isn't
// already a built-in verb as its own `rig <script>` subcommand — the parity to
// the Node rig's scripts→verbs. Each runs `<pm> run <script>` (package-manager
// detected) with any extra args forwarded.
func scriptCmds(root string) []*cobra.Command {
	return scriptEntryCmds(nodeScripts(root))
}

// nodeScripts builds the script entries for a Node repo's package.json scripts.
func nodeScripts(root string) []scriptEntry {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &pkg) != nil || len(pkg.Scripts) == 0 {
		return nil
	}
	pm := string(detect.DetectNodePM(root))

	names := make([]string, 0, len(pkg.Scripts))
	for name := range pkg.Scripts {
		names = append(names, name)
	}
	sort.Strings(names)

	var entries []scriptEntry
	for _, name := range names {
		if isBuiltinVerb[name] {
			continue // a built-in dev verb already maps this
		}
		script := name
		entries = append(entries, scriptEntry{
			name:  script,
			eco:   "node",
			loc:   "package.json",
			short: "Script: " + pkg.Scripts[script],
			run: func(cmd *cobra.Command, args []string) error {
				argv := append([]string{pm, "run", script}, args...)
				return runCommand(cmd, root, argv)
			},
		})
	}
	return entries
}

// goScriptCmds surfaces runnable Go tools that the workspace declares under a
// conventional scripts/ or cmd/ directory as their own `rig <name>` verb — the
// Go counterpart to scriptCmds' package.json scripts. The verb name is the
// tool's leaf directory (scripts/dev-install → `rig dev-install`) and it runs
// `go run ./<dir>` from the repo root with any extra args forwarded.
//
// Discovery is deliberately conservative — never an arbitrary executable found
// on disk. Helper commands under scripts/ are found directly on disk, so a
// single-module repo with no go.work still surfaces e.g. `rig dev-install`;
// additionally, any scripts/ or cmd/ `main` listed in a go.work `use` block is
// surfaced (multi-module workspaces). cmd/ is not auto-scanned: those are
// product binaries with their own names, kept out of rig's verb space unless a
// go.work entry opts them in. Names colliding with a built-in verb are skipped
// so the dev loop always wins.
func goScriptCmds(root string) []*cobra.Command {
	return scriptEntryCmds(goScripts(root))
}

// goScripts builds the script entries for the workspace's Go scripts//cmd verbs.
func goScripts(root string) []scriptEntry {
	dirs := append(goWorkUseDirs(root), scriptDirs(root)...)
	if len(dirs) == 0 {
		return nil
	}
	sort.Strings(dirs)

	var entries []scriptEntry
	seen := map[string]bool{}
	for _, rel := range dirs {
		if top := firstSegment(rel); top != "scripts" && top != "cmd" {
			continue // only conventional tool locations become bare verbs
		}
		name := filepath.Base(rel)
		if name == "" || isBuiltinVerb[name] || seen[name] {
			continue
		}
		if !isGoMainPackage(filepath.Join(root, filepath.FromSlash(rel))) {
			continue // a library module — nothing to run
		}
		seen[name] = true
		rel := rel
		entries = append(entries, scriptEntry{
			name:        name,
			eco:         "go",
			loc:         rel,
			short:       "Script: go run ./" + rel,
			annotations: map[string]string{scriptVerbAnnotation: "1"},
			run: func(cmd *cobra.Command, args []string) error {
				argv := append([]string{"go", "run", "./" + rel}, args...)
				return runCommand(cmd, root, argv)
			},
		})
	}
	return entries
}

// goWorkUseEntry matches one `./path` entry of a go.work `use` block, whether
// written as a single `use ./x` line or inside a `use ( … )` group.
var goWorkUseEntry = regexp.MustCompile(`(?m)^\s*(?:use\s+)?(\./[^\s()]+)`)

// goWorkUseDirs returns the module directories listed in root/go.work's use
// block as repo-relative slash paths (e.g. "scripts/dev-install"). Nil when
// there is no go.work.
func goWorkUseDirs(root string) []string {
	data, err := os.ReadFile(filepath.Join(root, "go.work"))
	if err != nil {
		return nil
	}
	var dirs []string
	for _, m := range goWorkUseEntry.FindAllStringSubmatch(string(data), -1) {
		dirs = append(dirs, strings.TrimPrefix(filepath.ToSlash(m[1]), "./"))
	}
	return dirs
}

// scriptDirs lists the immediate subdirectories of root/scripts as repo-relative
// slash paths (e.g. "scripts/dev-install"). These conventional helper-command
// locations become `rig <name>` verbs even without a go.work entry, so a
// single-module repo still surfaces them. Nil when there is no scripts/ dir.
func scriptDirs(root string) []string {
	entries, err := os.ReadDir(filepath.Join(root, "scripts"))
	if err != nil {
		return nil
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, "scripts/"+e.Name())
		}
	}
	return dirs
}

// firstSegment returns the leading path segment of a slash path ("scripts" for
// "scripts/dev-install").
func firstSegment(rel string) string {
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return rel
}

var goPackageMain = regexp.MustCompile(`(?m)^package main\b`)

// isGoMainPackage reports whether dir holds a `package main` (a runnable Go
// command), scanning its non-test .go files.
func isGoMainPackage(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		if data, err := os.ReadFile(filepath.Join(dir, n)); err == nil && goPackageMain.Match(data) {
			return true
		}
	}
	return false
}
