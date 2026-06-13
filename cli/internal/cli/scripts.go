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

	"github.com/rigsmith/cli/internal/config"
	"github.com/rigsmith/cli/internal/detect"
	"github.com/rigsmith/cli/internal/envstack"
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
}

// customCmds turns each .rig.json "commands" entry into a rig subcommand.
// A string entry runs through the shell (`sh -c`), an argv array is exec'd
// directly, and the object form applies its per-OS override (macos | windows |
// linux), per-command env, and cwd. Names that collide with a built-in verb
// are skipped so the dev loop always wins.
func customCmds(cfg config.Config) []*cobra.Command {
	if len(cfg.Commands) == 0 {
		return nil
	}
	names := make([]string, 0, len(cfg.Commands))
	for name := range cfg.Commands {
		names = append(names, name)
	}
	sort.Strings(names)

	var cmds []*cobra.Command
	for _, name := range names {
		if isBuiltinVerb[name] {
			continue
		}
		name, def := name, cfg.Commands[name]
		cmds = append(cmds, &cobra.Command{
			Use:   name,
			Short: customShort(name, def),
			// Let unknown flags fall through to the command while rig's own
			// --dry-run/--quiet still bind.
			FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
			RunE: func(cmd *cobra.Command, args []string) error {
				cwd, _ := os.Getwd()
				root := resolveRoot(cwd)
				return runCustom(cmd, cfg, root, name, def, args)
			},
		})
	}
	return cmds
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
		display, argv := shellInvocation(spec.Shell, args)
		return runIn(cmd, dir, env, display, argv...)
	}

	if len(spec.Argv) == 0 {
		return fmt.Errorf("command %q has an empty argv", name)
	}
	argv := append(append([]string{}, spec.Argv...), args...)
	return runIn(cmd, dir, env, strings.Join(argv, " "), argv...)
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

	var cmds []*cobra.Command
	for _, name := range names {
		if isBuiltinVerb[name] {
			continue // a built-in dev verb already maps this
		}
		script := name
		cmds = append(cmds, &cobra.Command{
			Use:                script,
			Short:              "Script: " + pkg.Scripts[script],
			GroupID:            "",
			FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
			RunE: func(cmd *cobra.Command, args []string) error {
				argv := append([]string{pm, "run", script}, args...)
				return runCommand(cmd, root, argv)
			},
		})
	}
	return cmds
}

// goScriptCmds surfaces runnable Go tools that the workspace declares under a
// conventional scripts/ or cmd/ directory as their own `rig <name>` verb — the
// Go counterpart to scriptCmds' package.json scripts. The verb name is the
// tool's leaf directory (scripts/dev-install → `rig dev-install`) and it runs
// `go run ./<dir>` from the repo root with any extra args forwarded.
//
// Discovery is deliberately conservative: only `main` packages that appear in
// go.work's `use` block are considered (author opt-in via a committed file —
// never an arbitrary executable found on disk), and only those under scripts/
// or cmd/. Names colliding with a built-in verb are skipped so the dev loop
// always wins.
func goScriptCmds(root string) []*cobra.Command {
	dirs := goWorkUseDirs(root)
	if len(dirs) == 0 {
		return nil
	}
	sort.Strings(dirs)

	var cmds []*cobra.Command
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
		cmds = append(cmds, &cobra.Command{
			Use:                name,
			Short:              "Script: go run ./" + rel,
			Annotations:        map[string]string{scriptVerbAnnotation: "1"},
			FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
			RunE: func(cmd *cobra.Command, args []string) error {
				argv := append([]string{"go", "run", "./" + rel}, args...)
				return runCommand(cmd, root, argv)
			},
		})
	}
	return cmds
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
