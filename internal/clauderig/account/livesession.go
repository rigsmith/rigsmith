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
	Cwd    string // session working directory (sessions only; "" for ide locks)
}

// sessionFile mirrors ~/.claude/sessions/{pid}.json (only the fields we read).
type sessionFile struct {
	PID        int    `json:"pid"`
	Entrypoint string `json:"entrypoint"`
	Kind       string `json:"kind"`
	Cwd        string `json:"cwd"`
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
			seen[s.PID] = Instance{PID: s.PID, Kind: kind, Source: "session", Cwd: s.Cwd}
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

	// Process scan. The two registries above are the richer source — they carry
	// cwd and entrypoint — but they cannot be RELIED on: Claude Code 2.1.227
	// writes neither, so on a current install both directories sit empty and this
	// function used to report "nothing running" with a live session in the room.
	// That is the worst possible failure for a guard whose entire job is to
	// refuse: it does not warn, it silently permits, and swapping the credential
	// under a live session is exactly what logs that session out.
	//
	// So the process table is consulted too, and it is authoritative for
	// existence. A pid already described by a registry keeps that description.
	for _, p := range liveClaudeProcesses(claudeHome) {
		if _, ok := seen[p.PID]; !ok {
			seen[p.PID] = p
		}
	}

	out := make([]Instance, 0, len(seen))
	for _, inst := range seen {
		out = append(out, inst)
	}
	sortByPID(out)
	return out
}

// Indirected so tests can supply a process table instead of the machine's own —
// a guard this important needs its logic covered, not just its plumbing.
var (
	claudeProcessPIDs = claudeProcessPIDsImpl
	processConfigDir  = processConfigDirImpl
)

// liveClaudeProcesses returns running Claude Code processes that use the LIVE
// credential — the only ones a switch endangers.
//
// Sessions started by `clauderig account run` are deliberately excluded: they
// point CLAUDE_CONFIG_DIR at their own profile and authenticate from it, so
// swapping the machine-wide credential does not touch them. Blocking on those
// would make `switch` unusable on exactly the setup clauderig encourages —
// several accounts running side by side.
func liveClaudeProcesses(claudeHome string) []Instance {
	var out []Instance
	for _, pid := range claudeProcessPIDs() {
		if pid <= 1 || !pidAlive(pid) {
			continue
		}
		dir, known := processConfigDir(pid)
		switch {
		case !known:
			// Environment unreadable (another user's process, or a platform where
			// we cannot ask). Assume it is live: over-reporting costs a refusal the
			// user can override, under-reporting costs them their login.
		case dir != "" && !sameDir(dir, claudeHome):
			continue // isolated `account run` profile — unaffected by a swap
		}
		out = append(out, Instance{PID: pid, Kind: "cli", Source: "process"})
	}
	return out
}

// sameDir compares two directory paths for the isolation check, tolerating a
// trailing separator and resolving symlinks where possible so a profile that
// happens to point at ~/.claude by another name is not mistaken for isolated.
func sameDir(a, b string) bool {
	clean := func(p string) string {
		p = filepath.Clean(p)
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	return clean(a) == clean(b)
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
