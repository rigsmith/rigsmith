package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// What rig has installed on this machine — which binary is answering, which
// family members are alongside it, and what it has registered in the user's
// shell — is state rig writes but has never read back. `rig setup` and `rig
// alias install` splice marked blocks into an rc file; nothing until now
// reported whether those blocks are there, current, or shadowed by a second
// copy of rig earlier on PATH. The readers live here, shared by `rig doctor`'s
// Setup group and `rig alias list`.

// setupShellName is the shell rig would install into: $SHELL's base name, with
// pwsh folded onto powershell, and Windows — where $SHELL is normally unset —
// assumed to be PowerShell. "" when it resolves to a shell rig has no snippet
// for (sh, csh, an unset $SHELL on Unix), which is a fact to report rather than
// a problem to fix.
func setupShellName() string {
	shell := shellFromEnv()
	if shell == "pwsh" {
		shell = "powershell"
	}
	if !isSetupShell(shell) && runtime.GOOS == "windows" {
		shell = "powershell"
	}
	if !isSetupShell(shell) {
		return ""
	}
	return shell
}

// shellState is what rig has registered in the user's shell: the rc file it
// writes to, and each of its two marked blocks as they exist there right now.
// A block is "" when absent. shell is "" when rig has no snippet for this
// shell, in which case nothing was read and every block is empty — callers must
// say "couldn't tell" rather than "none installed".
type shellState struct {
	shell   string
	rcPath  string
	setup   string // the `rig setup` block: cd wrapper + completions
	aliases string // the `rig alias install` block
	err     error  // the rc file couldn't be located or read (missing is not an error)
}

// readShellState reads both managed blocks out of the user's rc file. A missing
// rc file is not an error — it is the ordinary "never ran setup" state, and
// reports as blocks that aren't there.
func readShellState() shellState {
	st := shellState{shell: setupShellName()}
	if st.shell == "" {
		return st
	}
	rcPath, err := rcFileFor(st.shell)
	if err != nil {
		st.err = err
		return st
	}
	st.rcPath = rcPath
	data, err := os.ReadFile(rcPath)
	if err != nil {
		if !os.IsNotExist(err) {
			st.err = err
		}
		return st
	}
	content := string(data)
	st.setup, _ = extractBlock(content, markerBegin(integrationBase), markerEnd(integrationBase))
	st.aliases, _ = extractBlock(content, aliasMarkerBegin(), aliasMarkerEnd())
	return st
}

// devBlockPresent reports whether the rc file carries a `--dev` launcher's
// block. On its own that is fine; alongside a missing canonical block it is the
// explanation for "I ran setup and `rig cd` still doesn't work" — the block
// that got installed binds rig-dev.
func (s shellState) devBlockPresent() bool {
	if s.rcPath == "" {
		return false
	}
	data, err := os.ReadFile(s.rcPath)
	if err != nil {
		return false
	}
	dev := integrationBase + "-dev"
	_, ok := extractBlock(string(data), markerBegin(dev), markerEnd(dev))
	return ok
}

// runningBinary is the path of the rig that is executing, symlinks resolved so
// a launcher shim reports what it actually runs. "" when it can't be resolved.
func runningBinary() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

// pathCopies lists every executable named `name` on PATH, in PATH order,
// deduplicated by resolved path. More than one is the shadowing case: the copy
// the shell finds first is the one that runs, which may not be the one that
// just answered `rig --version`.
func pathCopies(name string) []string {
	var out []string
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		for _, candidate := range executableNames(name) {
			full := filepath.Join(dir, candidate)
			if err := isExecutableFile(full); err != nil {
				continue
			}
			resolved := full
			if r, err := filepath.EvalSymlinks(full); err == nil {
				resolved = r
			}
			if seen[resolved] {
				continue
			}
			seen[resolved] = true
			out = append(out, resolved)
		}
	}
	return out
}

// executableNames are the file names a command can have on this OS (Windows
// resolves `rig` to rig.exe / rig.cmd / rig.bat via PATHEXT).
func executableNames(name string) []string {
	if runtime.GOOS != "windows" {
		return []string{name}
	}
	names := []string{name}
	exts := os.Getenv("PATHEXT")
	if exts == "" {
		exts = ".COM;.EXE;.BAT;.CMD"
	}
	for _, ext := range strings.Split(exts, ";") {
		if ext = strings.TrimSpace(ext); ext != "" {
			names = append(names, name+strings.ToLower(ext))
		}
	}
	return names
}

// isExecutableFile reports (as an error, nil when yes) whether path is a file
// this OS would run. It is exec.LookPath's per-file test, applied to a path we
// already have rather than searching for one.
func isExecutableFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.ErrNotExist
	}
	if runtime.GOOS == "windows" {
		return nil // PATHEXT decided it above
	}
	if info.Mode()&0o111 == 0 {
		return os.ErrPermission
	}
	return nil
}

// onPath reports whether a command resolves on PATH, and where.
func onPath(name string) (string, bool) {
	p, err := exec.LookPath(name)
	if err != nil {
		return "", false
	}
	if resolved, rerr := filepath.EvalSymlinks(p); rerr == nil {
		return resolved, true
	}
	return p, true
}
