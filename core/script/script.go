// Package script is the shared Tengo scripting runtime: a sandboxed evaluator
// for expressions (used to gate `if` conditions and compute values) and a step
// runner for full scripts with side-effecting helpers (sh, cp/mv/rm/mkdir, log,
// fail). The host supplies a ctx object and a Host for the side effects, so the
// same runtime drives both shiprig release steps and rig custom commands.
package script

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/d5/tengo/v2"
	"github.com/d5/tengo/v2/stdlib"
	"github.com/rigsmith/rigsmith/core/shellrun"
)

// Modules is the safe Tengo stdlib available via import(...): string/format/
// math/json helpers, but NOT os/exec — pure expressions with no side effects.
var Modules = []string{"text", "fmt", "math", "times", "rand", "json", "base64", "hex", "enum"}

// Globals are the builtin modules pre-bound as globals, so a one-line
// expression can call e.g. text.re_match / fmt.sprintf without an import.
var Globals = []string{"text", "fmt", "math", "times", "rand", "json", "base64", "hex"}

// Timeout bounds a single evaluation or script run so a runaway loop can't hang
// the host.
const Timeout = 10 * time.Second

// Host provides the side-effecting capabilities the script builtins need. Every
// side effect flows through the host, which owns the single dry-run policy:
// whether to preview or perform, and how to report. The runtime stays pure.
type Host interface {
	// Sh runs (or, in a dry run, previews) a shell command line in the host's
	// working directory and returns its stdout. The host announces the command
	// and reports output through its own sink. A non-zero exit must be returned
	// as a non-nil error so the script aborts (Tengo has no try/catch, so this
	// is the safe `set -e`-like default).
	Sh(cmd string) (stdout string, err error)
	// FileOp runs (or, in a dry run, previews) a cross-platform file command
	// (name is cp/mv/rm/mkdir) with coreutils-style args in the host's working
	// directory. shellrun.FileOp is the standard execute path.
	FileOp(name string, args []string) error
	// Report emits one line to the host's output (used by log()).
	Report(line string)
}

// Builtins is the side-effecting API injected into a script run.
func Builtins(h Host) map[string]*tengo.UserFunction {
	return map[string]*tengo.UserFunction{
		"sh":    {Name: "sh", Value: shFunc(h)},
		"cp":    {Name: "cp", Value: fileOpFunc(h, "cp")},
		"mv":    {Name: "mv", Value: fileOpFunc(h, "mv")},
		"rm":    {Name: "rm", Value: fileOpFunc(h, "rm")},
		"mkdir": {Name: "mkdir", Value: fileOpFunc(h, "mkdir")},
		"log":   {Name: "log", Value: logFunc(h)},
		"fail":  {Name: "fail", Value: failFunc},
	}
}

// shFunc runs a shell command through the host and returns its stdout.
func shFunc(h Host) tengo.CallableFunc {
	return func(args ...tengo.Object) (tengo.Object, error) {
		if len(args) != 1 {
			return nil, tengo.ErrWrongNumArguments
		}
		cmd, ok := tengo.ToString(args[0])
		if !ok {
			return nil, tengo.ErrInvalidArgumentType{Name: "command", Expected: "string", Found: args[0].TypeName()}
		}
		out, err := h.Sh(cmd)
		if err != nil {
			return nil, err
		}
		return &tengo.String{Value: out}, nil
	}
}

// fileOpFunc adapts a cross-platform file op (cp/mv/rm/mkdir) to a Tengo
// function, delegating to the host so dry-run and reporting stay in one place.
func fileOpFunc(h Host, name string) tengo.CallableFunc {
	return func(args ...tengo.Object) (tengo.Object, error) {
		strs, err := stringArgs(args)
		if err != nil {
			return nil, err
		}
		if err := h.FileOp(name, strs); err != nil {
			return nil, err
		}
		return tengo.UndefinedValue, nil
	}
}

// logFunc writes a line to the host output. Non-string arguments are formatted
// via their Tengo representation rather than silently dropped.
func logFunc(h Host) tengo.CallableFunc {
	return func(args ...tengo.Object) (tengo.Object, error) {
		parts := make([]string, len(args))
		for i, a := range args {
			if s, ok := tengo.ToString(a); ok {
				parts[i] = s
			} else {
				parts[i] = a.String()
			}
		}
		h.Report(strings.Join(parts, " "))
		return tengo.UndefinedValue, nil
	}
}

// failFunc aborts the script with a message.
func failFunc(args ...tengo.Object) (tengo.Object, error) {
	msg := "script called fail()"
	if len(args) > 0 {
		if s, ok := tengo.ToString(args[0]); ok {
			msg = s
		}
	}
	return nil, errors.New(msg)
}

func stringArgs(args []tengo.Object) ([]string, error) {
	out := make([]string, len(args))
	for i, a := range args {
		s, ok := tengo.ToString(a)
		if !ok {
			return nil, tengo.ErrInvalidArgumentType{Name: fmt.Sprintf("arg %d", i), Expected: "string", Found: a.TypeName()}
		}
		out[i] = s
	}
	return out, nil
}

// Run executes a full Tengo script with the ctx object, the stdlib globals, and
// the side-effecting Builtins bound. A returned error (setup or runtime) is the
// caller's to report and means the script failed.
func Run(code string, ctx map[string]interface{}, h Host) error {
	s := tengo.NewScript([]byte(code))
	s.SetImports(stdlib.GetModuleMap(Modules...))

	// A failed Add would leave the script missing an expected binding, so
	// surface it up front rather than as a confusing "unresolved name" later.
	var addErr error
	add := func(name string, value interface{}) {
		if addErr == nil {
			addErr = s.Add(name, value)
		}
	}
	add("ctx", ctx)
	for _, name := range Globals {
		if mod, ok := stdlib.BuiltinModules[name]; ok {
			add(name, &tengo.ImmutableMap{Value: mod})
		}
	}
	for name, fn := range Builtins(h) {
		add(name, fn)
	}
	if addErr != nil {
		return fmt.Errorf("script setup error: %w", addErr)
	}

	runCtx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	_, err := s.RunContext(runCtx)
	return err
}

// RunnerHost returns a ready-made Host backed by a shellrun.Runner, so a caller
// with no special reporting needs (e.g. rig custom commands) gets the standard
// behavior without writing an adapter: sh() runs the command line through the
// runner, cp/mv/rm/mkdir use the in-process file ops, and in a dry run every
// side effect is previewed via report instead of performed. dir is the working
// directory; report receives command output, log() lines, and dry-run previews.
//
// A host that needs to hook each command (announce lines, secret masking) — as
// shiprig's release pipeline does — should implement Host directly instead.
func RunnerHost(r shellrun.Runner, dir string, dryRun bool, report func(line string)) Host {
	return &runnerHost{r: r, dir: dir, dryRun: dryRun, report: report}
}

type runnerHost struct {
	r      shellrun.Runner
	dir    string
	dryRun bool
	report func(string)
}

func (h *runnerHost) Sh(cmd string) (string, error) {
	if h.dryRun {
		h.report("would run: " + cmd)
		return "", nil
	}
	output, code, err := h.r(true, []string{cmd}, h.dir)
	if err != nil {
		output = append(output, err.Error())
		if code == 0 {
			code = -1
		}
	}
	for _, line := range output {
		h.report(line)
	}
	if code != 0 {
		return "", fmt.Errorf("sh: command exited %d: %s", code, cmd)
	}
	return strings.Join(output, "\n"), nil
}

func (h *runnerHost) FileOp(name string, args []string) error {
	if h.dryRun {
		h.report("would " + name + " " + strings.Join(args, " "))
		return nil
	}
	return shellrun.FileOp(name, h.dir, args)
}

func (h *runnerHost) Report(line string) { h.report(line) }

// Eval evaluates a Tengo expression against ctx and returns the resulting
// variable. The expression is wrapped in an assignment so a bare expression
// (the common case for `if`/computed vars) is what callers write.
func Eval(expr string, ctx map[string]interface{}) (*tengo.Variable, error) {
	s := tengo.NewScript([]byte("__out__ := (" + expr + ")"))
	s.SetImports(stdlib.GetModuleMap(Modules...))
	for _, name := range Globals {
		if mod, ok := stdlib.BuiltinModules[name]; ok {
			_ = s.Add(name, &tengo.ImmutableMap{Value: mod})
		}
	}
	if err := s.Add("ctx", ctx); err != nil {
		return nil, err
	}

	runCtx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	compiled, err := s.RunContext(runCtx)
	if err != nil {
		return nil, err
	}
	return compiled.Get("__out__"), nil
}

// EvalBool evaluates an expression for truthiness (Tengo's rules: non-zero
// numbers, non-empty strings/collections, and true are truthy).
func EvalBool(expr string, ctx map[string]interface{}) (bool, error) {
	v, err := Eval(expr, ctx)
	if err != nil {
		return false, err
	}
	return v.Bool(), nil
}

// EvalString evaluates an expression and renders its result as a string (for a
// computed variable's value).
func EvalString(expr string, ctx map[string]interface{}) (string, error) {
	v, err := Eval(expr, ctx)
	if err != nil {
		return "", err
	}
	switch x := v.Value().(type) {
	case string:
		return x, nil
	case nil:
		return "", nil
	default:
		return fmt.Sprintf("%v", x), nil
	}
}
