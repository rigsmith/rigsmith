package mover

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
		// In a dry run the rename is skipped, so the transcripts are still under
		// the old slug — count them there so dry-run counts match a real apply.
		scanDir := newDir
		if dryRun {
			scanDir = filepath.Join(projectsDir, mv.OldSlug)
		}
		n, err := rebaseTranscriptCwds(scanDir, mv.OldCwd, mv.NewCwd, dryRun)
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
		if tmp != nil {
			_ = tmp.Close() // nothing to write — close so the deferred Remove succeeds on Windows
		}
		return changed, nil
	}
	if err := out.Flush(); err != nil {
		return changed, err
	}
	if err := tmp.Close(); err != nil {
		return changed, err
	}
	// Close the input before replacing it: Windows refuses to rename over a file
	// that is still open (the deferred in.Close would otherwise run too late).
	if err := in.Close(); err != nil {
		return changed, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return changed, err
	}
	return changed, nil
}

// setTopLevelCwd replaces the value of the record's own cwd field, leaving every
// other byte of the record exactly as it was.
//
// Not a textual replace of the quoted path: a record carries more than one
// path-valued field, and replacing the first occurrence rewrote whichever came
// first while cwd itself stayed stale. Walking the top-level keys finds the
// field, and json.RawMessage hands back the value's exact source bytes, so the
// span to overwrite is known rather than guessed.
func setTopLevelCwd(line, newQ []byte) ([]byte, bool) {
	dec := json.NewDecoder(bytes.NewReader(line))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return line, false
	}
	for dec.More() {
		key, kerr := dec.Token()
		if kerr != nil {
			return line, false
		}
		name, _ := key.(string)
		var raw json.RawMessage
		if derr := dec.Decode(&raw); derr != nil {
			return line, false
		}
		if name != "cwd" {
			continue
		}
		// InputOffset is the end of the value just decoded, and RawMessage is
		// its verbatim source, so the value starts len(raw) bytes before it.
		end := int(dec.InputOffset())
		start := end - len(raw)
		if start < 0 || end > len(line) {
			return line, false
		}
		out := make([]byte, 0, len(line)-len(raw)+len(newQ))
		out = append(out, line[:start]...)
		out = append(out, newQ...)
		out = append(out, line[end:]...)
		return out, true
	}
	return line, false
}

// rebaseLineCwd rewrites the top-level cwd of one JSON record when that cwd
// rebases under oldCwd.
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
	newQ, _ := json.Marshal(rebased)
	return setTopLevelCwd(line, newQ)
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

// rewriteExactCwd rewrites records whose cwd is EXACTLY oldCwd, leaving deeper
// paths alone. rebaseTranscriptCwds is the other half of this pair: it rewrites
// anything under oldCwd, because there the directory moved and everything below
// it moved too. Here nothing moved, so a record naming /a/sub still names a real
// directory that is still there and must not be edited.
func rewriteExactCwd(path, dest, oldCwd, newCwd string, dryRun bool) (changed int, err error) {
	in, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	// With a dest the rewritten copy IS the move: it is written straight into
	// the new project directory and the caller drops the source once this
	// returns. Without one the transcript is rewritten where it lies.
	moving := dest != ""

	br := bufio.NewReaderSize(in, 1<<20)
	var out *bufio.Writer
	var w *os.File
	if !dryRun {
		if moving {
			// O_EXCL, not a Stat beforehand: a Stat reports what was true a
			// moment ago, and overwriting here would lose a conversation.
			w, err = os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err != nil {
				if errors.Is(err, fs.ErrExist) {
					return 0, fmt.Errorf("%s already exists — that session is already filed there", dest)
				}
				return 0, err
			}
			// A half-written transcript at the destination is worse than none:
			// it is not the conversation, and it blocks the retry that would
			// produce the real one.
			defer func() {
				if err != nil {
					_ = w.Close()
					_ = os.Remove(dest)
				}
			}()
		} else {
			w, err = os.CreateTemp(filepath.Dir(path), ".clauderig-reroot-*")
			if err != nil {
				return 0, err
			}
			defer func() { _ = os.Remove(w.Name()) }() // no-op once renamed
		}
		out = bufio.NewWriterSize(w, 1<<20)
	}

	newQ, _ := json.Marshal(newCwd)
	for {
		line, rerr := br.ReadBytes('\n')
		if len(line) > 0 {
			var probe struct {
				Cwd string `json:"cwd"`
			}
			if json.Unmarshal(bytes.TrimSpace(line), &probe) == nil && probe.Cwd == oldCwd {
				if rewritten, ok := setTopLevelCwd(line, newQ); ok {
					line = rewritten
					changed++
				}
			}
			if out != nil {
				if _, werr := out.Write(line); werr != nil {
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

	// Nothing to write back: a dry run, or a rewrite in place that changed
	// nothing. A move still has to land its copy even when no record named the
	// old root.
	if dryRun || (!moving && changed == 0) {
		if w != nil {
			_ = w.Close()
		}
		return changed, nil
	}
	if err = out.Flush(); err != nil {
		return changed, err
	}
	if err = w.Close(); err != nil {
		return changed, err
	}
	if moving {
		return changed, nil
	}
	// Close the input before replacing it: Windows refuses to rename over a
	// file that is still open.
	if err = in.Close(); err != nil {
		return changed, err
	}
	err = os.Rename(w.Name(), path)
	return changed, err
}
