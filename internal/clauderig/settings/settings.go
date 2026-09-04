// Package settings resolves which Claude Code settings.json a clauderig command
// should touch. Claude Code merges settings from several tiers; clauderig commands
// pick a Scope and this package maps it to a concrete file. It's the shared seam
// for any command that writes settings — sync hooks belong at user scope (they
// travel with clauderig's ~/.claude sync), the guard belongs at project scope (it
// travels with the repo), and personal tweaks belong at local scope.
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Scope is a Claude Code settings tier.
type Scope string

const (
	// User is ~/.claude/settings.json — applies to every project on the machine.
	User Scope = "user"
	// Project is <repo>/.claude/settings.json — committed, shared with the team.
	Project Scope = "project"
	// Local is <repo>/.claude/settings.local.json — gitignored, just this checkout.
	Local Scope = "local"
)

// All lists the scopes in precedence order (broadest to narrowest), which is also
// the order to report or sweep them in.
//
// Precedence is per key, and not every key takes part. Claude Code reads a few
// only from the broad tiers: `defaultMode: "bypassPermissions"` (since the
// 2026-09-02 release) and `defaultMode: "auto"` are honoured from user or
// managed settings and ignored, silently, in a project or local file. The
// narrower tiers therefore cannot always override the broader ones, and a
// value committed to a repo's `.claude/settings.json` may be one Claude Code
// never reads there. IgnoredAt names those values for a file, and
// `clauderig doctor` reports them.
var All = []Scope{User, Project, Local}

// Parse turns a flag value into a Scope ("global" is accepted as an alias for
// user). An empty string is rejected — callers handle "no scope given" themselves.
func Parse(s string) (Scope, error) {
	switch Scope(s) {
	case User, Project, Local:
		return Scope(s), nil
	case "global":
		return User, nil
	}
	return "", fmt.Errorf("unknown scope %q (want user|project|local)", s)
}

// Path returns the settings file for the scope. home is required for User scope;
// repoRoot is required for Project and Local. A missing requirement is an error,
// so a project-scoped command run outside a repo fails loudly instead of writing
// to the wrong file.
func (s Scope) Path(home, repoRoot string) (string, error) {
	switch s {
	case User:
		if home == "" {
			return "", fmt.Errorf("cannot resolve home directory for user-scope settings")
		}
		return filepath.Join(home, ".claude", "settings.json"), nil
	case Project:
		if repoRoot == "" {
			return "", fmt.Errorf("project scope needs a git repository (run inside one)")
		}
		return filepath.Join(repoRoot, ".claude", "settings.json"), nil
	case Local:
		if repoRoot == "" {
			return "", fmt.Errorf("local scope needs a git repository (run inside one)")
		}
		return filepath.Join(repoRoot, ".claude", "settings.local.json"), nil
	}
	return "", fmt.Errorf("unknown scope %q", s)
}

// Label is a short human description used in command output.
func (s Scope) Label() string {
	switch s {
	case User:
		return "user (~/.claude/settings.json)"
	case Project:
		return "project (.claude/settings.json)"
	case Local:
		return "local (.claude/settings.local.json)"
	}
	return string(s)
}

// Ignored is a value Claude Code reads from a settings file at one scope and
// honours at another. The precedence order in All is not the whole story: a
// few keys are accepted from user or managed settings only, and a project or
// local file that carries them is silently a no-op for that key. clauderig
// never writes these itself, but it is the tool that reads the project
// tiers on every doctor run — and Claude Code gives no error when a value
// that used to work there stops being read, so this is where it gets said.
type Ignored struct {
	Key   string // the settings key, e.g. "defaultMode"
	Value string // the value that is ignored at this scope
	// Where names the scopes that do honour it; empty when no scope does.
	Where string
	// Fix says what to change when the key itself is the mistake — a
	// top-level defaultMode, which belongs under permissions.
	Fix string
}

func (i Ignored) String() string { return fmt.Sprintf("%s: %q", i.Key, i.Value) }

// ignoredModes lists the `defaultMode` values Claude Code drops at project and
// local scope: "auto" always was, and "bypassPermissions" joined it in the
// 2026-09-02 release. Both are honoured from user or managed settings, or via
// `--permission-mode` on the command line.
var ignoredModes = map[string]bool{"bypassPermissions": true, "auto": true}

// IgnoredAt reports the values in the settings file at path that Claude Code
// will not honour at scope s — `permissions.defaultMode` at project or local
// scope, where Claude Code keeps the mode but reads only some values, and a
// top-level `defaultMode` at any scope, which it never reads. A missing or
// empty file has none. It parses what it needs and nothing else, so an
// otherwise malformed file is reported as an error rather than as clean;
// IsParseError tells that apart from a file that could not be read.
func IgnoredAt(s Scope, path string) ([]Ignored, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, nil
	}
	// Claude Code keeps the mode under the permissions object; a bare
	// top-level defaultMode is not a shape it reads, but one people write by
	// mistake, so it is reported too rather than silently passed over.
	var m struct {
		Permissions struct {
			DefaultMode string `json:"defaultMode"`
		} `json:"permissions"`
		DefaultMode string `json:"defaultMode"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	const honoured = "user or managed settings, or --permission-mode on the command line"
	var out []Ignored
	// User settings honour every mode; the two dropped ones are dropped at
	// the narrower scopes only.
	if s != User && ignoredModes[m.Permissions.DefaultMode] {
		out = append(out, Ignored{Key: "permissions.defaultMode", Value: m.Permissions.DefaultMode, Where: honoured})
	}
	// A top-level defaultMode is never read, at any scope, whatever it
	// says: the key is permissions.defaultMode. Reported for any value, then,
	// not only the ones a scope would drop — the person who wrote it meant
	// something. Where names the scopes that honour the value once it is
	// under the right key, which for a dropped mode at a narrow scope is
	// still not this one.
	if m.DefaultMode != "" {
		i := Ignored{Key: "defaultMode", Value: m.DefaultMode,
			Fix: "move it under permissions — Claude Code never reads a top-level defaultMode"}
		if s != User && ignoredModes[m.DefaultMode] {
			i.Where = honoured
		}
		out = append(out, i)
	}
	return out, nil
}

// IsParseError reports whether an error from IgnoredAt came from the file's
// content rather than from reading it — the difference between "fix the
// JSON" and "make the file readable".
func IsParseError(err error) bool {
	var syntax *json.SyntaxError
	var typ *json.UnmarshalTypeError
	return errors.As(err, &syntax) || errors.As(err, &typ)
}
