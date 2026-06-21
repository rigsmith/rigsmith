package config

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
)

// CommandSpec is a shell command (single string, run via the shell) or an
// explicit argv array (exec'd directly, bypassing the shell).
type CommandSpec struct {
	// Shell is the command line for the string form.
	Shell string
	// Argv is the program + args for the array form; nil for the string form.
	Argv []string
}

// IsShell reports whether the spec is the string (shell) form.
func (s *CommandSpec) IsShell() bool { return s.Argv == nil }

// UnmarshalJSON accepts a JSON string (shell form) or array of strings (argv).
func (s *CommandSpec) UnmarshalJSON(data []byte) error {
	switch t := firstToken(data); t {
	case '"':
		return json.Unmarshal(data, &s.Shell)
	case '[':
		argv := []string{}
		if err := json.Unmarshal(data, &argv); err != nil {
			return err
		}
		s.Argv = argv
		return nil
	default:
		return fmt.Errorf("a command must be a string or an array of strings")
	}
}

// Command is a custom command entry under "commands". Accepts three JSON
// shapes: a bare string (shell command), a string array (argv), or an object
// with description/command/os/env/cwd/shell.
type Command struct {
	// Description is the help line for the generated subcommand.
	Description string
	// Spec is the object form's "command" value (or the whole bare
	// string/array entry).
	Spec *CommandSpec
	// OS maps macos | windows | linux to a per-OS spec override.
	OS map[string]*CommandSpec
	// Env is extra environment for this command only.
	Env map[string]string
	// Cwd is the working directory, relative to the repo root.
	Cwd string
	// Shell overrides the config-level shell for this command's shell-string
	// form ("portable" | "system"); "" inherits the config default. Ignored for
	// the argv form and the script form.
	Shell string
	// Script is the Tengo script form (object form only): a cross-platform
	// command body with sh()/cp()/mv()/rm()/mkdir()/log()/fail() builtins.
	// Mutually exclusive with the command/argv/os forms.
	Script *ScriptSpec
}

// ScriptSpec is a custom command's Tengo script, written in JSONC as one of:
//
//   - a string:          "mkdir(`-p`, `dist`)"
//   - an array of lines: ["mkdir(`-p`, `dist`)", "sh(`build`)"]  (joined with newlines)
//   - a file reference:  { "file": "./scripts/clean.tengo" }
//
// Tip: write Tengo string literals with backticks (raw strings) so they don't
// collide with JSON's double quotes — no \" escaping needed.
type ScriptSpec struct {
	// Code is the inline script (a string, or an array of lines joined with
	// newlines), or the file's contents once File has been loaded.
	Code string
	// File is the path to a .tengo file (resolved relative to the config file),
	// or "" for an inline script. Cleared once the file is inlined at load.
	File string
}

// UnmarshalJSON reads the string / array-of-lines / { "file": … } shapes.
func (s *ScriptSpec) UnmarshalJSON(data []byte) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	switch v := raw.(type) {
	case string:
		s.Code = v
	case []any:
		lines := make([]string, len(v))
		for i, item := range v {
			line, ok := item.(string)
			if !ok {
				return fmt.Errorf("a script array must contain only strings (lines)")
			}
			lines[i] = line
		}
		s.Code = strings.Join(lines, "\n")
	case map[string]any:
		file, ok := v["file"].(string)
		if !ok || file == "" {
			return fmt.Errorf(`a script object must set "file" to a path`)
		}
		s.File = file
	default:
		return fmt.Errorf(`a script must be a string, an array of lines, or { "file": … }`)
	}
	return nil
}

// Resolve returns the command for the current OS: an `os` entry if present,
// otherwise the top-level spec. May be nil (no spec for this OS) — the caller
// reports a clean error.
func (c *Command) Resolve() *CommandSpec {
	if c.OS != nil {
		key := currentOSKey()
		for k, v := range c.OS {
			if strings.EqualFold(k, key) {
				return v
			}
		}
	}
	return c.Spec
}

// currentOSKey maps runtime.GOOS to the schema's os keys (macos|windows|linux,
// matching the .NET rig; everything non-Windows/mac counts as linux).
func currentOSKey() string {
	switch runtime.GOOS {
	case "windows":
		return "windows"
	case "darwin":
		return "macos"
	default:
		return "linux"
	}
}

// UnmarshalJSON accepts the three entry shapes: string, array, or object.
func (c *Command) UnmarshalJSON(data []byte) error {
	switch t := firstToken(data); t {
	case '"', '[':
		var spec CommandSpec
		if err := json.Unmarshal(data, &spec); err != nil {
			return err
		}
		c.Spec = &spec
		return nil
	case '{':
		// Field names match case-insensitively and unknown keys are skipped,
		// like every other part of the schema.
		var obj struct {
			Description string                  `json:"description"`
			Command     *CommandSpec            `json:"command"`
			OS          map[string]*CommandSpec `json:"os"`
			Env         map[string]string       `json:"env"`
			Cwd         string                  `json:"cwd"`
			Shell       string                  `json:"shell"`
			Script      *ScriptSpec             `json:"script"`
		}
		if err := json.Unmarshal(data, &obj); err != nil {
			return err
		}
		c.Description = obj.Description
		c.Spec = obj.Command
		c.OS = obj.OS
		c.Env = obj.Env
		c.Cwd = obj.Cwd
		c.Shell = obj.Shell
		c.Script = obj.Script
		return nil
	default:
		return fmt.Errorf("a command entry must be a string, array, or object")
	}
}

// firstToken returns the first non-whitespace byte of raw JSON, 0 when empty.
func firstToken(data []byte) byte {
	for _, b := range data {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		}
		return b
	}
	return 0
}
