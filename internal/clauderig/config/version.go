package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// DetectClaudeVersion reads the Claude Code version from the CLI root. The
// sessions/ registry (one file per live process) carries the current running
// version; it's the most reliable source (.last-update-result.json is the last
// auto-update, which lags). Falls back to a recent transcript's header version,
// then "". claudeHome is the resolved ~/.claude location.
func DetectClaudeVersion(claudeHome string) string {
	if v := versionFromSessions(filepath.Join(claudeHome, "sessions")); v != "" {
		return v
	}
	return versionFromTranscripts(filepath.Join(claudeHome, "projects"))
}

type versioned struct {
	Version string `json:"version"`
}

func versionFromSessions(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	// newest session file first
	sort.Slice(entries, func(i, j int) bool {
		fi, _ := entries[i].Info()
		fj, _ := entries[j].Info()
		return fi != nil && fj != nil && fi.ModTime().After(fj.ModTime())
	})
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var v versioned
		if json.Unmarshal(b, &v) == nil && v.Version != "" {
			return v.Version
		}
	}
	return ""
}

// versionFromTranscripts scans the first transcript it finds for a header version.
func versionFromTranscripts(projects string) string {
	var found string
	filepath.WalkDir(projects, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".jsonl" || found != "" {
			return nil
		}
		b, _ := os.ReadFile(p)
		for _, line := range splitLines(b, 20) {
			var v versioned
			if json.Unmarshal(line, &v) == nil && v.Version != "" {
				found = v.Version
				return filepath.SkipAll
			}
		}
		return nil
	})
	return found
}

// splitLines returns up to max newline-separated lines from b.
func splitLines(b []byte, max int) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i < len(b) && len(out) < max; i++ {
		if b[i] == '\n' {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	return out
}
