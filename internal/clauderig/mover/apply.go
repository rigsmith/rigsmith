package mover

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Report summarises what Apply did (or, in a dry run, would do).
type Report struct {
	MovedDir     bool
	SlugsRenamed int
	Transcripts  int // transcript lines whose cwd was rebased
	DesktopFiles int
	SettingsFile bool
	DryRun       bool
}

// Apply executes the plan: it moves the directory (when MoveDir), renames each
// project slug dir, rebases the cwd inside the transcripts, and rewrites the
// Desktop session metadata and settings additionalDirectories. A dry run reports
// the same counts without touching disk.
//
// Apply refuses when the plan has a live-session blocker or a slug collision —
// both are checked again here so the package is safe to drive from any caller,
// not only the command that already previewed them.
func (p *Plan) Apply(projectsDir string, dryRun bool) (Report, error) {
	rep := Report{DryRun: dryRun}
	if len(p.LiveBlockers) > 0 {
		return rep, fmt.Errorf("refusing: %d running Claude session(s) are inside %s — close them first", len(p.LiveBlockers), p.Src)
	}
	if p.HasCollision() {
		return rep, fmt.Errorf("refusing: a destination slug dir already exists (a session was opened at the destination); merge or remove it first")
	}

	if p.MoveDir {
		rep.MovedDir = true
		if !dryRun {
			if err := os.Rename(p.Src, p.Dst); err != nil {
				return rep, fmt.Errorf("move %s → %s: %w", p.Src, p.Dst, err)
			}
		}
	}

	for _, mv := range p.Projects {
		newDir := filepath.Join(projectsDir, mv.NewSlug)
		if mv.NewSlug != mv.OldSlug {
			rep.SlugsRenamed++
			if !dryRun {
				if err := os.Rename(filepath.Join(projectsDir, mv.OldSlug), newDir); err != nil {
					return rep, fmt.Errorf("rename slug %s → %s: %w", mv.OldSlug, mv.NewSlug, err)
				}
			}
		}
		n, err := rebaseTranscriptCwds(newDir, mv.OldCwd, mv.NewCwd, dryRun)
		if err != nil {
			return rep, err
		}
		rep.Transcripts += n
	}

	for _, f := range p.Desktop {
		changed, err := rebaseJSONFile(f, p.Src, p.Dst, dryRun)
		if err != nil {
			return rep, err
		}
		if changed {
			rep.DesktopFiles++
		}
	}

	if p.Settings != "" {
		changed, err := rebaseJSONFile(p.Settings, p.Src, p.Dst, dryRun)
		if err != nil {
			return rep, err
		}
		rep.SettingsFile = changed
	}
	return rep, nil
}

// rebaseTranscriptCwds rewrites the top-level "cwd" field of every record in
// every .jsonl in dir from a path under oldCwd to the matching path under newCwd.
// It replaces only the quoted cwd value in place, leaving the rest of each line
// byte-for-byte intact (transcripts are large and full of unrelated path strings
// in tool output we must not touch). Returns the number of records rewritten.
func rebaseTranscriptCwds(dir, oldCwd, newCwd string, dryRun bool) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	total := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		n, err := rebaseOneTranscript(filepath.Join(dir, e.Name()), oldCwd, newCwd, dryRun)
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

func rebaseOneTranscript(path, oldCwd, newCwd string, dryRun bool) (int, error) {
	in, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	// First pass (and only pass for a dry run): count records whose cwd rebases.
	br := bufio.NewReaderSize(in, 1<<20)
	changed := 0
	var out *bufio.Writer
	var tmp *os.File
	if !dryRun {
		tmp, err = os.CreateTemp(filepath.Dir(path), ".clauderig-mv-*")
		if err != nil {
			return 0, err
		}
		defer func() { _ = os.Remove(tmp.Name()) }() // no-op once renamed
		out = bufio.NewWriterSize(tmp, 1<<20)
	}

	for {
		line, rerr := br.ReadBytes('\n')
		if len(line) > 0 {
			rewritten, did := rebaseLineCwd(line, oldCwd, newCwd)
			if did {
				changed++
			}
			if out != nil {
				if _, werr := out.Write(rewritten); werr != nil {
					return changed, werr
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return changed, rerr
		}
	}

	if dryRun || changed == 0 {
		return changed, nil
	}
	if err := out.Flush(); err != nil {
		return changed, err
	}
	if err := tmp.Close(); err != nil {
		return changed, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return changed, err
	}
	return changed, nil
}

// rebaseLineCwd rewrites the top-level cwd of one JSON record. It decodes only
// the cwd field, and when that value rebases under oldCwd it textually replaces
// the first occurrence of the quoted old value with the quoted new value — the
// cwd is among the first keys, so the first quoted-path match is it.
func rebaseLineCwd(line []byte, oldCwd, newCwd string) ([]byte, bool) {
	var probe struct {
		Cwd string `json:"cwd"`
	}
	if json.Unmarshal(bytes.TrimSpace(line), &probe) != nil || probe.Cwd == "" {
		return line, false
	}
	rebased, under := rebase(probe.Cwd, oldCwd, newCwd)
	if !under {
		return line, false
	}
	oldQ, _ := json.Marshal(probe.Cwd)
	newQ, _ := json.Marshal(rebased)
	return bytes.Replace(line, oldQ, newQ, 1), true
}

// rebaseJSONFile rewrites every string value in a JSON file that is a path under
// src so it points under dst, preserving everything else. Used for Desktop
// session metadata (cwd/originCwd/planPath) and settings additionalDirectories.
func rebaseJSONFile(path, src, dst string, dryRun bool) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false, nil // not JSON we can rewrite — leave it
	}
	v, n := rebaseJSONValues(v, src, dst)
	if n == 0 {
		return false, nil
	}
	if dryRun {
		return true, nil
	}
	encoded, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return false, err
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// rebaseJSONValues walks a parsed JSON value and rebases every string that is a
// path under src, returning the value and the count changed.
func rebaseJSONValues(v any, src, dst string) (any, int) {
	n := 0
	var walk func(any) any
	walk = func(node any) any {
		switch t := node.(type) {
		case map[string]any:
			for k, val := range t {
				t[k] = walk(val)
			}
			return t
		case []any:
			for i, val := range t {
				t[i] = walk(val)
			}
			return t
		case string:
			if rebased, under := rebase(t, src, dst); under {
				n++
				return rebased
			}
			return t
		default:
			return node
		}
	}
	return walk(v), n
}

// fileReferencesSrc reports whether a JSON file has any string value that is a
// path under src — the cheap predicate BuildPlan uses to decide whether a file
// is worth rewriting at all.
func fileReferencesSrc(path, src string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	_, n := rebaseJSONValues(v, src, src) // rebase to itself just to count matches
	return n > 0
}
