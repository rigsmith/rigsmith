package account

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Instance is a running Claude Code process detected from ~/.claude state. The
// guard on `switch` uses these to refuse mutating the live credential out from
// under an in-flight session (the failure mode that forces a re-login).
type Instance struct {
	PID    int    // the process id
	Kind   string // entrypoint/ide name, e.g. "cli", "claude-vscode", "VS Code"
	Source string // "session" | "ide"
}

// sessionFile mirrors ~/.claude/sessions/{pid}.json (only the fields we read).
type sessionFile struct {
	PID        int    `json:"pid"`
	Entrypoint string `json:"entrypoint"`
	Kind       string `json:"kind"`
}

// ideLock mirrors ~/.claude/ide/{port}.lock (only the fields we read).
type ideLock struct {
	PID     int    `json:"pid"`
	IDEName string `json:"ideName"`
}

// RunningInstances returns the live Claude Code processes recorded under
// claudeHome (~/.claude): CLI/IDE sessions in sessions/ and IDE bridges in ide/.
// Stale records whose process is gone are skipped, and a pid seen in both dirs is
// reported once. Order is by pid for stable output.
func RunningInstances(claudeHome string) []Instance {
	seen := map[int]Instance{}

	// sessions/{pid}.json — CLI and IDE-hosted sessions.
	sessDir := filepath.Join(claudeHome, "sessions")
	if entries, err := os.ReadDir(sessDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			var s sessionFile
			if readJSON(filepath.Join(sessDir, e.Name()), &s) != nil || s.PID <= 1 {
				continue
			}
			if !pidAlive(s.PID) {
				continue
			}
			kind := s.Entrypoint
			if kind == "" {
				kind = s.Kind
			}
			seen[s.PID] = Instance{PID: s.PID, Kind: kind, Source: "session"}
		}
	}

	// ide/{port}.lock — IDE bridge processes.
	ideDir := filepath.Join(claudeHome, "ide")
	if entries, err := os.ReadDir(ideDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".lock") {
				continue
			}
			var l ideLock
			if readJSON(filepath.Join(ideDir, e.Name()), &l) != nil || l.PID <= 1 {
				continue
			}
			if !pidAlive(l.PID) {
				continue
			}
			if _, ok := seen[l.PID]; ok {
				continue // already counted from sessions/
			}
			kind := l.IDEName
			if kind == "" {
				kind = "ide"
			}
			seen[l.PID] = Instance{PID: l.PID, Kind: kind, Source: "ide"}
		}
	}

	out := make([]Instance, 0, len(seen))
	for _, inst := range seen {
		out = append(out, inst)
	}
	sortByPID(out)
	return out
}

// KillInstances ends the given processes: SIGTERM first (graceful — lets editors
// save), and after the grace period SIGKILLs any straggler. It returns the
// instances that were still alive at the end (couldn't be killed, e.g. owned by
// another user). Cross-platform: TerminateProcess on Windows.
func KillInstances(insts []Instance, grace time.Duration) (failed []Instance) {
	for _, in := range insts {
		_ = terminate(in.PID)
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) && anyAlive(insts) {
		time.Sleep(100 * time.Millisecond)
	}
	for _, in := range insts {
		if pidAlive(in.PID) {
			_ = forceKill(in.PID)
		}
	}
	// brief settle for SIGKILL to take effect before reporting survivors
	for i := 0; i < 20 && anyAlive(insts); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	for _, in := range insts {
		if pidAlive(in.PID) {
			failed = append(failed, in)
		}
	}
	return failed
}

func anyAlive(insts []Instance) bool {
	for _, in := range insts {
		if pidAlive(in.PID) {
			return true
		}
	}
	return false
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func sortByPID(in []Instance) {
	// small slices; simple insertion sort keeps it dependency-free and stable
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j-1].PID > in[j].PID; j-- {
			in[j-1], in[j] = in[j], in[j-1]
		}
	}
}
