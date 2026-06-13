// Package gowork discovers the runnable tools declared in a rigsmith go.work
// workspace, so the source and dev installers can enumerate (module, command
// name) pairs without each re-parsing go.work.
package gowork

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Tool is a runnable workspace module: its repo-relative slash path (e.g.
// "cli") and the command name from its main.go `// Command <name>` line (e.g.
// "rig").
type Tool struct {
	Module string
	Name   string
}

// FindRoot walks up from dir to the directory containing go.work.
func FindRoot(dir string) (string, error) {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.work found above %s; run this from inside the rigsmith repo", dir)
		}
		dir = parent
	}
}

var (
	useEntry   = regexp.MustCompile(`(?m)^\s*(?:use\s+)?(\./[^\s()]+)`)
	commandDoc = regexp.MustCompile(`(?m)^// Command (\S+)`)
)

// Tools returns the runnable tools listed in repo's go.work use block: modules
// whose main.go declares `// Command <name>`. Library modules (no such main.go)
// are omitted. Module paths are repo-relative slash paths.
func Tools(repo string) ([]Tool, error) {
	data, err := os.ReadFile(filepath.Join(repo, "go.work"))
	if err != nil {
		return nil, err
	}
	var tools []Tool
	for _, m := range useEntry.FindAllStringSubmatch(string(data), -1) {
		mod := strings.TrimPrefix(filepath.ToSlash(m[1]), "./")
		if name := commandName(filepath.Join(repo, mod)); name != "" {
			tools = append(tools, Tool{Module: mod, Name: name})
		}
	}
	return tools, nil
}

// commandName reads "// Command <name>" from the module's main.go. Returns ""
// when the module has no main.go (a library).
func commandName(moduleDir string) string {
	data, err := os.ReadFile(filepath.Join(moduleDir, "main.go"))
	if err != nil {
		return ""
	}
	if m := commandDoc.FindStringSubmatch(string(data)); m != nil {
		return m[1]
	}
	return ""
}
