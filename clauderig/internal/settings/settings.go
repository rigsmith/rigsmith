// Package settings resolves which Claude Code settings.json a clauderig command
// should touch. Claude Code merges settings from several tiers; clauderig commands
// pick a Scope and this package maps it to a concrete file. It's the shared seam
// for any command that writes settings — sync hooks belong at user scope (they
// travel with clauderig's ~/.claude sync), the guard belongs at project scope (it
// travels with the repo), and personal tweaks belong at local scope.
package settings

import (
	"fmt"
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
