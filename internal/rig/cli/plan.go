package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/envstack"
	"github.com/rigsmith/rigsmith/core/shellrun"
	"github.com/rigsmith/rigsmith/internal/rig/config"
	"github.com/rigsmith/rigsmith/internal/rig/detect"
)

// A verb's resolution — what will actually run, where, and with what
// environment — is decided in one place and read by two: the run paths, and
// `rig explain`. They share the resolvers below rather than each deriving the
// answer, because an explain that can disagree with a run is worse than no
// explain at all: it would describe a command nobody executes.

// planKind is how a resolved command executes.
type planKind int

const (
	planArgv   planKind = iota // exec'd directly, no shell involved
	planShell                  // a command line handed to a shell
	planScript                 // a Tengo script run in-process
)

// commandPlan is one verb resolved. Everything here is decided before anything
// runs, so it can be printed as truthfully as it can be executed.
type commandPlan struct {
	verb   string
	origin string // where the verb is defined: ".rig.json", "package.json", an ecosystem
	detail string // the origin's specifics: a config path, a package manager, a dir
	kind   planKind
	line   string   // the command as a human reads it (and as rig echoes it)
	argv   []string // planArgv, and the shell invocation for a system-shell plan
	code   string   // planScript: the Tengo source
	shell  string   // planShell/planScript: shellrun.ShellPortable | ShellSystem
	dir    string   // working directory
	layers []envLayer
	notes  []string // anything true about this resolution that isn't obvious

	// root and args are carried for the Tengo form, whose ctx is built from
	// them at run time.
	root string
	args []string
}

// envLayer is one contributor to a spawned command's environment, named. A
// layer set is three of them — the repo's .env files, `.rig.json` env, and the
// caller's own top layer — applied in that order with the ambient process
// environment just above the file layer, which is the order rig has always
// used: a real exported variable beats a .env file, config and command env beat
// both.
//
// The layers are declared once, here, and serve both the environment a command
// actually gets and the listing `explain` prints. Without that, explain would
// be re-deriving a merge order it can only get wrong.
type envLayer struct {
	name string
	vars map[string]string
}

// verbEnvLayers are the layers rig contributes to a built-in verb's command:
// the repo's .env files, `.rig.json` env, and any active env presets. `--no-env`
// drops the file layer.
func verbEnvLayers(root string) []envLayer {
	var fileEnv map[string]string
	if !noEnv {
		fileEnv, _ = envstack.Load(root)
	}
	cfg, _ := config.LoadMerged(root)
	return []envLayer{
		{".env / .env.local", fileEnv},
		{".rig.json env", cfg.Env},
		{"env preset", presetEnv},
	}
}

// customCommandLayers are the layers for a custom command: the .env files next
// to the config that declared it, `.rig.json` env, then the command's own env.
func customCommandLayers(cfg config.Config, own map[string]string) []envLayer {
	return []envLayer{
		{".env / .env.local", customFileEnv(cfg)},
		{".rig.json env", cfg.Env},
		{"command env", own},
	}
}

// mergedEnv folds the layers into the map a command runs with, ambient
// included. A layer set is always the three envstack.Merge takes — files,
// config, then the caller's own — built by verbEnvLayers or
// customCommandLayers.
func mergedEnv(layers []envLayer) map[string]string {
	return envstack.Merge(layers[0].vars, envstack.Ambient(), layers[1].vars, layers[2].vars)
}

// layeredEnviron is mergedEnv as an environ slice, or nil when no layer
// contributes anything — nil means "inherit", which is not the same as handing
// a child an explicit copy of the parent's environment.
func layeredEnviron(layers []envLayer) []string {
	if !contributes(layers) {
		return nil
	}
	return envstack.Environ(mergedEnv(layers))
}

// contributes reports whether any layer sets a variable.
func contributes(layers []envLayer) bool {
	for _, l := range layers {
		if len(l.vars) > 0 {
			return true
		}
	}
	return false
}

// customPlan resolves a `.rig.json` custom command: the Tengo script form, or —
// after picking the spec for this OS — the shell-string or argv form, with the
// command's cwd and env layers applied and any extra CLI args folded in exactly
// as the run folds them. Every way a command can be misconfigured is an error
// here, so `explain` reports it in the same words a run would.
func customPlan(cfg config.Config, root, name string, def *config.Command, args []string) (commandPlan, error) {
	dir := root
	if def.Cwd != "" {
		dir = filepath.Join(root, def.Cwd)
	}
	origin := ".rig.json"
	if cfg.Path != "" {
		origin = cfg.Path
	}
	p := commandPlan{
		verb:   name,
		origin: "custom command",
		detail: origin,
		dir:    dir,
		layers: customCommandLayers(cfg, def.Env),
		root:   root,
		args:   args,
	}

	// The Tengo script form is mutually exclusive with the command/argv/os forms.
	if def.Script != nil {
		if def.Spec != nil || def.OS != nil {
			return commandPlan{}, fmt.Errorf("command %q sets both %q and %q — use one", name, "command", "script")
		}
		if def.Script.File != "" {
			// loadCommandScripts left File set, so the read failed (a Warning says why).
			return commandPlan{}, fmt.Errorf("command %q: script file %q could not be loaded (see `rig info`)", name, def.Script.File)
		}
		mode, err := shellrun.ShellMode(coalesceShell(def.Shell, cfg.Shell))
		if err != nil {
			return commandPlan{}, fmt.Errorf("command %q: %w", name, err)
		}
		p.kind = planScript
		p.shell = mode
		p.code = def.Script.Code
		p.line = "(tengo script)"
		return p, nil
	}

	spec := def.Resolve()
	if spec == nil {
		return commandPlan{}, fmt.Errorf("command %q has no command defined for this OS", name)
	}
	if key, ok := osOverrideKey(def); ok {
		p.notes = append(p.notes, fmt.Sprintf("this command has an %q map; the %q entry applies here", "os", key))
	}

	if spec.IsShell() {
		// A shell-string command runs cross-platform by default: through the
		// in-process portable shell, so one command line works on every OS. The
		// "system" mode (config-level or per-command) opts back into the OS
		// shell for scripts that need a real userland or OS-specific syntax.
		mode, err := shellrun.ShellMode(coalesceShell(def.Shell, cfg.Shell))
		if err != nil {
			return commandPlan{}, fmt.Errorf("command %q: %w", name, err)
		}
		p.kind = planShell
		p.shell = mode
		if mode == shellrun.ShellPortable {
			p.line = portableLine(spec.Shell, args)
			return p, nil
		}
		p.line, p.argv = shellInvocation(spec.Shell, args)
		return p, nil
	}

	if len(spec.Argv) == 0 {
		return commandPlan{}, fmt.Errorf("command %q has an empty argv", name)
	}
	p.kind = planArgv
	p.argv = append(append([]string{}, spec.Argv...), args...)
	p.line = strings.Join(p.argv, " ")
	return p, nil
}

// osOverrideKey reports which key of a command's `os` map applies on this
// machine, and whether the map has one at all — a command can carry an `os` map
// that says nothing about the OS you are on, in which case the top-level
// command is what runs and there is nothing to point out.
func osOverrideKey(def *config.Command) (string, bool) {
	if len(def.OS) == 0 {
		return "", false
	}
	key := "linux"
	switch runtime.GOOS {
	case "windows":
		key = "windows"
	case "darwin":
		key = "macos"
	}
	for k := range def.OS {
		if strings.EqualFold(k, key) {
			return k, true
		}
	}
	return "", false
}

// nodeScriptPlan resolves a package.json script to the package-manager run it
// becomes. The argv is built here and nowhere else, so the verb rig surfaces
// and the command explain prints are the same string.
func nodeScriptPlan(root, pm, script string, args []string) commandPlan {
	argv := append([]string{pm, "run", script}, args...)
	return commandPlan{
		verb:   script,
		origin: "package.json script",
		detail: filepath.Join(root, "package.json"),
		kind:   planArgv,
		argv:   argv,
		line:   strings.Join(argv, " "),
		dir:    root,
		layers: verbEnvLayers(root),
		root:   root,
		args:   args,
	}
}

// goScriptPlan resolves a scripts//cmd Go tool to the `go run` that executes it.
func goScriptPlan(root, rel string, args []string) commandPlan {
	argv := append([]string{"go", "run", "./" + rel}, args...)
	return commandPlan{
		verb:   filepath.Base(rel),
		origin: "go script directory",
		detail: rel,
		kind:   planArgv,
		argv:   argv,
		line:   strings.Join(argv, " "),
		dir:    root,
		layers: verbEnvLayers(root),
		root:   root,
		args:   args,
	}
}

// ecosystemPlan resolves a built-in verb to the ecosystem command it runs at
// root — through resolveVerbCommand, the same resolver the dev verbs, `--all`,
// `info` and completion use. ok=false means this ecosystem maps no such verb.
func ecosystemPlan(eco, root, verb string, args []string) (commandPlan, bool) {
	argv, ok := resolveVerbCommand(eco, verb, root)
	if !ok {
		return commandPlan{}, false
	}
	argv = append(append([]string{}, argv...), args...)
	detail := eco
	if eco == detect.Node {
		detail += " · " + string(detect.DetectNodePM(root))
	}
	return commandPlan{
		verb:   verb,
		origin: "ecosystem convention",
		detail: detail,
		kind:   planArgv,
		argv:   argv,
		line:   strings.Join(argv, " "),
		dir:    root,
		layers: verbEnvLayers(root),
		root:   root,
		args:   args,
	}, true
}

// resolvedEnv is one environment variable a plan sets, with the layer that
// decided its value — and whether the ambient environment overrides it, which
// is true of a .env entry whenever the variable is already exported.
type resolvedEnv struct {
	key       string
	value     string
	from      string
	overriden bool
}

// planEnv resolves a plan's layers to the variables it actually sets, in key
// order. Ambient variables are not listed — they are inherited, not set — but a
// file-layer value the ambient environment shadows is marked, since that is a
// variable whose stated value is not the one the command will see.
func planEnv(layers []envLayer) []resolvedEnv {
	winner := map[string]resolvedEnv{}
	for i, l := range layers {
		for k, v := range l.vars {
			e := resolvedEnv{key: k, value: v, from: l.name}
			// The ambient environment sits above the file layer only.
			if i == 0 {
				if ambient, ok := os.LookupEnv(k); ok {
					e.value, e.overriden = ambient, true
				}
			}
			winner[k] = e
		}
	}
	out := make([]resolvedEnv, 0, len(winner))
	for _, e := range winner {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}
