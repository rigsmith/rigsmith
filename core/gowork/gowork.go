// Package gowork discovers the runnable rigsmith tools under cmd/, so the source
// and dev installers can enumerate (path, command name) pairs without each
// re-scanning the tree. (It is named for the go.work workspace it scanned before
// the single-module consolidation; it now walks cmd/.)
package gowork

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

// Tool is a runnable command under cmd/: its repo-relative slash path (e.g.
// "cmd/rig") and the command name from its main.go `// Command <name>` line
// (e.g. "rig").
type Tool struct {
	Module string
	Name   string
}

// FindRoot walks up from dir to the rigsmith module root (the directory holding
// go.mod).
func FindRoot(dir string) (string, error) {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod found above %s; run this from inside the rigsmith repo", dir)
		}
		dir = parent
	}
}

var commandDoc = regexp.MustCompile(`(?m)^// Command (\S+)`)

// Tools returns the runnable tools under repo/cmd: each subdirectory whose
// main.go declares `// Command <name>`. Directories without such a main.go are
// skipped, so adding a cmd/<tool> with a `// Command` line makes it install
// automatically. Module paths are repo-relative slash paths ("cmd/<dir>").
func Tools(repo string) ([]Tool, error) {
	cmdDir := filepath.Join(repo, "cmd")
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var tools []Tool
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if name := commandName(filepath.Join(cmdDir, e.Name())); name != "" {
			tools = append(tools, Tool{Module: "cmd/" + e.Name(), Name: name})
		}
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Module < tools[j].Module })
	return tools, nil
}

// commandName reads "// Command <name>" from the directory's main.go. Returns ""
// when the directory has no main.go (not a runnable command).
func commandName(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "main.go"))
	if err != nil {
		return ""
	}
	if m := commandDoc.FindStringSubmatch(string(data)); m != nil {
		return m[1]
	}
	return ""
}
