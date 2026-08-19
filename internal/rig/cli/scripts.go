package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/rigsmith/rigsmith/core/envstack"
	"github.com/rigsmith/rigsmith/core/script"
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

// builtinVerbs is the set of names rig owns, so custom commands and surfaced
// package.json scripts never shadow a built-in verb.
//
// DERIVED from the command tree rather than listed by hand. A literal list is a
// second source of truth that drifts the moment a verb is added — `deps`,
// `worktree`, `prune`, `copy`, `self-update` and `alias` were all missing from
// it — and the failure is silent: a same-named config entry gets no shadow
// warning and is treated as a discoverable script.
//
// newRootCmd() builds only the built-ins (Execute adds custom and package.json
// commands afterwards), so this sees exactly the owned names, plus their
// aliases, which shadow just as effectively.
// Resolved on first use rather than at init: a package-level var referring to
// newRootCmd is an initialization cycle, since building the tree reaches back
// into this file.
var (
	builtinVerbsOnce sync.Once
	builtinVerbsSet  map[string]bool
)

func builtinVerbs() map[string]bool {
	builtinVerbsOnce.Do(func() {
		owned := map[string]bool{}
		for _, c := range newRootCmd().Commands() {
			owned[c.Name()] = true
			for _, a := range c.Aliases {
				owned[a] = true
			}
		}
		builtinVerbsSet = owned
	})
	return builtinVerbsSet
}

// isBuiltinVerbName reports whether name is a command rig itself owns.
func isBuiltinVerbName(name string) bool { return builtinVerbs()[name] }

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
	// plan resolves the entry without running it, for `rig explain`. It is the
	// same resolution run performs — both call one resolver — so the two can't
	// describe different commands. err is the resolution error a run would
	// report (a custom command with no spec for this OS, an unloadable script).
	plan func(args []string) (commandPlan, error)
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
		if isBuiltinVerbName(name) {
			continue // shadowed by rig's own verb — reported by shadowedCommands
		}
		name, def := name, cfg.Commands[name]
		entries = append(entries, scriptEntry{
			name:  name,
			eco:   "custom",
			loc:   loc,
			short: customShort(name, def),
			plan: func(args []string) (commandPlan, error) {
				cwd, _ := os.Getwd()
				return customPlan(cfg, resolveRoot(cwd), name, def, args)
			},
			run: func(cmd *cobra.Command, args []string) error {
				cwd, _ := os.Getwd()
				root := resolveRoot(cwd)
				return runCustom(cmd, cfg, root, name, def, args)
			},
		})
	}
	return entries
}

// shadowedCommands lists the `commands` entries that name a verb rig already
// owns, in name order. Such an entry is skipped when the command tree is built,
// so it never runs — and because the built-in verb still works, the symptom is
// rig quietly doing something other than what the config says, which reads as
// rig malfunctioning rather than as a naming collision. Callers report it.
func shadowedCommands(cfg config.Config) []string {
	var out []string
	for name := range cfg.Commands {
		if isBuiltinVerbName(name) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// configProblems is everything non-fatal that is wrong with the loaded config:
// the parse-time warnings (a malformed file degraded to defaults, an unknown
// top-level key, a script file that wouldn't load) followed by every `commands`
// entry a built-in verb shadows. One list, so the per-run notice, `rig info`
// and `rig explain` report the same problems in the same words.
func configProblems(cfg config.Config) []string {
	problems := append([]string(nil), cfg.Warnings...)
	for _, name := range shadowedCommands(cfg) {
		problems = append(problems, shadowWarning(name, commandOrigin(cfg, name)))
	}
	return problems
}

// reportConfigProblems prints them once per run, ahead of whatever the command
// was actually asked to do.
//
// The parse warnings were collected and never shown until now, which is the
// worst of both worlds — the config reads as accepted while a key of it does
// nothing. Two commands are exempt because they report the same problems in
// their own output, and completion is exempt because a shell parses it.
func reportConfigProblems(cmd *cobra.Command, cfg config.Config) {
	switch cmd.Name() {
	case cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
		return
	case "explain", "info":
		return
	}
	w := cmd.ErrOrStderr()
	for _, p := range configProblems(cfg) {
		fmt.Fprintln(w, warnStyle.Render("rig: "+p))
	}
}

// printConfigProblems renders the problems as a section of a report (`rig info`,
// `rig explain`), or nothing when the config is clean.
func printConfigProblems(w io.Writer, cfg config.Config, header string) {
	problems := configProblems(cfg)
	if len(problems) == 0 {
		return
	}
	if header != "" {
		fmt.Fprintln(w, headerStyle.Render(header))
	}
	for _, p := range problems {
		fmt.Fprintln(w, warnStyle.Render("  ! "+p))
	}
	fmt.Fprintln(w)
}

// shadowWarning is the sentence rig prints for a shadowed custom command. It
// names the config file because the collision can come from the user-wide
// ~/.rig.json, where it is least expected.
// commandOrigin is the file that DECLARED a command, which is not necessarily
// cfg.Path: after a merge, a global-only command sits in a config whose Path
// names the repo file, so reporting Path would send the user to edit a file the
// command is not in.
func commandOrigin(cfg config.Config, name string) string {
	if p := cfg.CommandPaths[name]; p != "" {
		return p
	}
	return cfg.Path
}

func shadowWarning(name, configPath string) string {
	where := config.FileName
	if configPath != "" {
		where = configPath
	}
	return fmt.Sprintf("%q in %s is a built-in rig verb, so that entry never runs — rename it (e.g. %q)",
		name, where, name+":custom")
}

// customShort picks the help line for a custom command: its description if it
// has one, then a tengo-script marker, otherwise the shell string (legacy
// behavior), otherwise the argv.
func customShort(name string, def *config.Command) string {
	if def.Description != "" {
		return def.Description
	}
	if def.Script != nil {
		return "Custom command: (tengo script)"
	}
	if spec := def.Resolve(); spec != nil {
		if spec.IsShell() {
			return "Custom command: " + spec.Shell
		}
		return "Custom command: " + strings.Join(spec.Argv, " ")
	}
	return "Custom command: " + name
}

// runCustom executes one custom command: it resolves the command (customPlan —
// the Tengo script form, or the shell-string / argv form for this OS, with cwd,
// env layers and extra args folded in) and then runs what came back. Resolution
// lives in customPlan so `rig explain` reads the same answer rather than
// deriving its own.
func runCustom(cmd *cobra.Command, cfg config.Config, root, name string, def *config.Command, args []string) error {
	p, err := customPlan(cfg, root, name, def, args)
	if err != nil {
		return err
	}
	return runPlan(cmd, cfg, p)
}

// runPlan executes a resolved plan: a shell line through the portable or system
// shell, an argv exec'd directly, or a Tengo script in-process.
func runPlan(cmd *cobra.Command, cfg config.Config, p commandPlan) error {
	env := layeredEnviron(p.layers)
	switch p.kind {
	case planScript:
		return runScript(cmd, cfg, p)
	case planShell:
		if p.shell == shellrun.ShellPortable {
			return runPortableIn(cmd, p.dir, env, p.line)
		}
		return runIn(cmd, p.dir, env, p.line, p.argv...)
	default:
		return runIn(cmd, p.dir, env, p.line, p.argv...)
	}
}

// runScript runs a custom command's Tengo script through the shared core/script
// runtime: a rig ctx (args, env, root, cwd, ecosystem, os) plus the
// side-effecting sh()/cp()/mv()/rm()/mkdir()/log()/fail() builtins. sh() and the
// file ops go through the portable shell by default (system mode via the
// command/config `shell`), so the script is cross-platform; in a dry run the
// side effects are previewed, not performed.
func runScript(cmd *cobra.Command, cfg config.Config, p commandPlan) error {
	envMap := mergedEnv(p.layers)
	runnerEnv := envstack.Environ(envMap)
	runner := shellrun.NewPortableRunner(runnerEnv)
	if p.shell == shellrun.ShellSystem {
		runner = shellrun.NewExecRunner(runnerEnv)
	}

	echo(cmd, "(tengo script)")
	// In a dry run RunnerHost previews sh()/file ops while the script's own logic
	// still runs, so the command is exercised without side effects.
	report := func(line string) { fmt.Fprintln(cmd.OutOrStdout(), line) }
	host := script.RunnerHost(runner, p.dir, dryRun, report)
	if err := script.Run(p.code, scriptContext(cfg, p.root, p.dir, p.args, envMap), host); err != nil {
		return fmt.Errorf("command %q: %w", p.verb, err)
	}
	return nil
}

// scriptContext builds the ctx object a custom command's Tengo script sees: the
// passthrough args, the layered environment, the repo root, the working dir, the
// resolved ecosystem, and the OS.
func scriptContext(cfg config.Config, root, dir string, args []string, envMap map[string]string) map[string]interface{} {
	argList := make([]interface{}, len(args))
	for i, a := range args {
		argList[i] = a
	}
	env := make(map[string]interface{}, len(envMap))
	for k, v := range envMap {
		env[k] = v
	}
	return map[string]interface{}{
		"args":      argList,
		"env":       env,
		"root":      root,
		"cwd":       dir,
		"ecosystem": scriptEcosystem(cfg, dir),
		"os":        runtime.GOOS,
	}
}

// scriptEcosystem resolves ctx.ecosystem: the pinned .rig.json value if set,
// else the nearest-manifest detection from dir (id "" when none).
func scriptEcosystem(cfg config.Config, dir string) string {
	if cfg.Ecosystem != "" {
		return cfg.Ecosystem
	}
	id, _ := detect.NearestEcosystem(dir)
	return id
}

// customEnvMap returns the merged environment for a custom command as a map
// (low→high: .env/.env.local, ambient, config env, command env), the shared
// source for both a script's ctx.env and its runner environment.
func customEnvMap(cfg config.Config, extra map[string]string) map[string]string {
	return mergedEnv(customCommandLayers(cfg, extra))
}

// customFileEnv reads the .env/.env.local layer that sits under a custom
// command's environment — from the directory of the config that declared it —
// or nothing when --no-env drops the file layer. It is the single place the
// flag is honoured, so every command form (shell, argv, script) gets it: the
// script form's ctx.env and runner env come from customEnvMap, the rest from
// customEnv. A read error degrades to no file layer, as it always has.
func customFileEnv(cfg config.Config) map[string]string {
	if cfg.Path == "" || noEnv {
		return nil
	}
	fileEnv, _ := envstack.Load(filepath.Dir(cfg.Path))
	return fileEnv
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
// then the command's own `env`. `--no-env` drops the file layer, the same as it
// does for the built-in verbs (see commandEnv). Returns nil (inherit) when
// nothing applies.
func customEnv(cfg config.Config, extra map[string]string) []string {
	return layeredEnviron(customCommandLayers(cfg, extra))
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
		if isBuiltinVerbName(name) {
			continue // a built-in dev verb already maps this
		}
		script := name
		entries = append(entries, scriptEntry{
			name:  script,
			eco:   "node",
			loc:   "package.json",
			short: "Script: " + pkg.Scripts[script],
			plan:  func(args []string) (commandPlan, error) { return nodeScriptPlan(root, pm, script, args), nil },
			run: func(cmd *cobra.Command, args []string) error {
				return runCommand(cmd, root, nodeScriptPlan(root, pm, script, args).argv)
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
		if name == "" || isBuiltinVerbName(name) || seen[name] {
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
			plan:        func(args []string) (commandPlan, error) { return goScriptPlan(root, rel, args), nil },
			run: func(cmd *cobra.Command, args []string) error {
				return runCommand(cmd, root, goScriptPlan(root, rel, args).argv)
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
