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
// with description/command/os/env/cwd.
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
		}
		if err := json.Unmarshal(data, &obj); err != nil {
			return err
		}
		c.Description = obj.Description
		c.Spec = obj.Command
		c.OS = obj.OS
		c.Env = obj.Env
		c.Cwd = obj.Cwd
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
