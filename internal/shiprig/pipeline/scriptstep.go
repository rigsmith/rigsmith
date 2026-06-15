package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/d5/tengo/v2"
	"github.com/d5/tengo/v2/stdlib"
)

// runScriptStep executes a step's "script" (Tengo code) as its action. The
// script gets the same `ctx` as if/computed-vars plus side-effecting helpers —
// sh, cp/mv/rm/mkdir, log, fail. In a dry run those side effects are previewed
// (reported, not executed) while the script's own logic still runs.
func (p *Pipeline) runScriptStep(step ResolvedStep) bool {
	s := tengo.NewScript([]byte(step.Script))
	s.SetImports(stdlib.GetModuleMap(scriptModules...))
	for _, name := range scriptGlobals {
		if mod, ok := stdlib.BuiltinModules[name]; ok {
			_ = s.Add(name, &tengo.ImmutableMap{Value: mod})
		}
	}
	_ = s.Add("ctx", p.scriptCtx)
	for name, fn := range p.scriptFuncs() {
		_ = s.Add(name, fn)
	}

	runCtx, cancel := context.WithTimeout(context.Background(), scriptTimeout)
	defer cancel()
	if _, err := s.RunContext(runCtx); err != nil {
		p.reporter.CommandOutput([]string{err.Error()})
		p.reporter.CommandFailed(step.Name+" (script)", -1)
		return false
	}
	return true
}

// scriptFuncs is the side-effecting API injected into a script step.
func (p *Pipeline) scriptFuncs() map[string]*tengo.UserFunction {
	return map[string]*tengo.UserFunction{
		"sh":    {Name: "sh", Value: p.scriptSh},
		"cp":    {Name: "cp", Value: p.scriptFileOp("cp", fileOpCp)},
		"mv":    {Name: "mv", Value: p.scriptFileOp("mv", fileOpMv)},
		"rm":    {Name: "rm", Value: p.scriptFileOp("rm", fileOpRm)},
		"mkdir": {Name: "mkdir", Value: p.scriptFileOp("mkdir", fileOpMkdir)},
		"log":   {Name: "log", Value: p.scriptLog},
		"fail":  {Name: "fail", Value: scriptFail},
	}
}

// scriptSh runs a shell command through the (portable) runner and returns its
// stdout. A non-zero exit aborts the script (Tengo has no try/catch, so this is
// the safe `set -e`-like default). In a dry run it is previewed, returning "".
func (p *Pipeline) scriptSh(args ...tengo.Object) (tengo.Object, error) {
	if len(args) != 1 {
		return nil, tengo.ErrWrongNumArguments
	}
	cmd, ok := tengo.ToString(args[0])
	if !ok {
		return nil, tengo.ErrInvalidArgumentType{Name: "command", Expected: "string", Found: args[0].TypeName()}
	}
	if p.scriptDryRun {
		p.reporter.CommandOutput([]string{"would run: " + cmd})
		return &tengo.String{Value: ""}, nil
	}
	p.reporter.CommandStarted("script", ShellCommand(cmd))
	output, code := dispatch(p.runner, ShellCommand(cmd), p.workDir)
	p.reporter.CommandOutput(output)
	if code != 0 {
		return nil, fmt.Errorf("sh: command exited %d: %s", code, cmd)
	}
	return &tengo.String{Value: strings.Join(output, "\n")}, nil
}

// scriptFileOp adapts a cross-platform file op (cp/mv/rm/mkdir) to a Tengo
// function. In a dry run it reports what it would do without touching the disk.
func (p *Pipeline) scriptFileOp(name string, op func(dir string, args []string) error) tengo.CallableFunc {
	return func(args ...tengo.Object) (tengo.Object, error) {
		strs, err := tengoStringArgs(args)
		if err != nil {
			return nil, err
		}
		if p.scriptDryRun {
			p.reporter.CommandOutput([]string{"would " + name + " " + strings.Join(strs, " ")})
			return tengo.UndefinedValue, nil
		}
		if err := op(p.workDir, strs); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return tengo.UndefinedValue, nil
	}
}

// scriptLog writes a line to the release output.
func (p *Pipeline) scriptLog(args ...tengo.Object) (tengo.Object, error) {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i], _ = tengo.ToString(a)
	}
	p.reporter.CommandOutput([]string{strings.Join(parts, " ")})
	return tengo.UndefinedValue, nil
}

// scriptFail aborts the script (and thus fails the step) with a message.
func scriptFail(args ...tengo.Object) (tengo.Object, error) {
	msg := "script called fail()"
	if len(args) > 0 {
		if s, ok := tengo.ToString(args[0]); ok {
			msg = s
		}
	}
	return nil, errors.New(msg)
}

func tengoStringArgs(args []tengo.Object) ([]string, error) {
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
