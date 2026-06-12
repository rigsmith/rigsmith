package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigsmith/cli/internal/config"
	"github.com/rigsmith/cli/internal/detect"
	"github.com/rigsmith/cli/internal/envstack"
	"github.com/spf13/cobra"
)

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
				root := detect.Root(cwd)
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
		line := spec.Shell
		if len(args) > 0 {
			quoted := make([]string, len(args))
			for i, a := range args {
				quoted[i] = shellArg(a)
			}
			line = line + " " + strings.Join(quoted, " ")
		}
		return runIn(cmd, dir, env, line, "sh", "-c", line)
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
