// Package mover relinks a directory's Claude Code history when the directory is
// moved or renamed. Claude keys a project's sessions by a slug derived from its
// absolute cwd (~/.claude/projects/<Flatten(cwd)>), so moving the directory
// without renaming the slug orphans the conversation. mover renames the slug
// dir(s), rebases the cwd recorded inside each transcript, and follows the same
// path through the Desktop session metadata and settings additionalDirectories —
// the same surface clauderig already rewrites on cross-machine restore.
package mover

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/internal/clauderig/project"
)

// Plan is the full set of changes a move entails: the directory move itself plus
// every history artifact that points under the source. It is computed read-only
// by BuildPlan; nothing on disk changes until Apply.
type Plan struct {
	Src      string // absolute, cleaned source directory
	Dst      string // absolute, cleaned destination directory
	MoveDir  bool   // rename Src→Dst on disk (false when Src is already gone — relink only)
	Projects []SlugMove
	Desktop  []string // Desktop session JSON files with a cwd under Src
	Settings string   // settings.json path to rewrite ("" when it has nothing under Src)

	// LiveBlockers are cwds of running Claude sessions that sit under Src. A
	// non-empty list means the move would rename a slug dir a live session is
	// writing to — Apply refuses, and the command reports it.
	LiveBlockers []string
}

// SlugMove renames one ~/.claude/projects slug dir and rebases the cwd its
// transcripts record. A project whose cwd equals Src moves wholesale; a project
// in a subdirectory of Src (a session opened deeper in the tree) is rebased to
// the matching subdirectory of Dst.
type SlugMove struct {
	OldSlug, NewSlug string
	OldCwd, NewCwd   string
	Collision        bool // NewSlug dir already exists — Apply refuses the whole plan
}

// HasCollision reports whether any slug rename would clobber an existing dir.
func (p *Plan) HasCollision() bool {
	for _, m := range p.Projects {
		if m.Collision {
			return true
		}
	}
	return false
}

// Empty reports whether the plan would touch no history at all (no slug dirs, no
// Desktop files, no settings). The directory move may still be worth doing.
func (p *Plan) Empty() bool {
	return len(p.Projects) == 0 && len(p.Desktop) == 0 && p.Settings == ""
}

// Resolve cleans and validates a src/dst pair for a move. src must be an existing
// directory OR already gone (a relink after a manual move); dst must not exist,
// and its parent must. The returned moveDir says whether Apply should perform the
// filesystem rename (false in the relink-only case).
func Resolve(src, dst string) (absSrc, absDst string, moveDir bool, err error) {
	absSrc, err = filepath.Abs(filepath.Clean(src))
	if err != nil {
		return "", "", false, err
	}
	absDst, err = filepath.Abs(filepath.Clean(dst))
	if err != nil {
		return "", "", false, err
	}
	if absSrc == absDst {
		return "", "", false, fmt.Errorf("source and destination are the same: %s", absSrc)
	}

	srcInfo, srcErr := os.Stat(absSrc)
	if srcErr != nil && !os.IsNotExist(srcErr) {
		return "", "", false, fmt.Errorf("stat source %s: %w", absSrc, srcErr)
	}
	dstInfo, dstErr := os.Stat(absDst)
	if dstErr != nil && !os.IsNotExist(dstErr) {
		return "", "", false, fmt.Errorf("stat destination %s: %w", absDst, dstErr)
	}
	srcExists, dstExists := srcErr == nil, dstErr == nil

	switch {
	case srcExists && !srcInfo.IsDir():
		return "", "", false, fmt.Errorf("source is not a directory: %s", absSrc)
	case dstExists && !dstInfo.IsDir():
		return "", "", false, fmt.Errorf("destination exists and is not a directory: %s", absDst)
	case srcExists && dstExists:
		return "", "", false, fmt.Errorf("both source and destination exist: %s and %s", absSrc, absDst)
	case srcExists && !dstExists:
		// Normal move: src present, dst free. Its parent must exist.
		if _, perr := os.Stat(filepath.Dir(absDst)); perr != nil {
			return "", "", false, fmt.Errorf("destination parent does not exist: %s", filepath.Dir(absDst))
		}
		moveDir = true
	case !srcExists && dstExists:
		// Relink only: the directory (a dir, checked above) was already moved by
		// hand; just fix the history.
		moveDir = false
	default:
		return "", "", false, fmt.Errorf("source does not exist: %s", absSrc)
	}
	return absSrc, absDst, moveDir, nil
}

// BuildPlan computes every change relinking Src→Dst requires. claudeHome is
// ~/.claude; desktopRoot is the Desktop app's data dir (pass "" to skip Desktop);
// liveCwds are the cwds of running Claude sessions (for the live-session guard).
func BuildPlan(absSrc, absDst string, moveDir bool, claudeHome, desktopRoot string, liveCwds []string) (*Plan, error) {
	p := &Plan{Src: absSrc, Dst: absDst, MoveDir: moveDir}

	for _, cwd := range liveCwds {
		if _, under := rebase(cwd, absSrc, absDst); under {
			p.LiveBlockers = append(p.LiveBlockers, cwd)
		}
	}
	sort.Strings(p.LiveBlockers)

	if err := p.findProjects(filepath.Join(claudeHome, "projects")); err != nil {
		return nil, err
	}
	if desktopRoot != "" {
		if err := p.findDesktop(filepath.Join(desktopRoot, "claude-code-sessions")); err != nil {
			return nil, err
		}
	}
	if settings := filepath.Join(claudeHome, "settings.json"); fileReferencesSrc(settings, absSrc) {
		p.Settings = settings
	}
	return p, nil
}

// findProjects records a SlugMove for every project slug dir whose cwd is Src or
// sits under it. Unreadable/empty slug dirs are skipped (nothing to relink).
func (p *Plan) findProjects(projectsDir string) error {
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(projectsDir, e.Name())
		cwd, ok, err := project.CwdFromProjectDir(dir)
		if err != nil || !ok {
			continue
		}
		newCwd, under := rebase(cwd, p.Src, p.Dst)
		if !under {
			continue
		}
		newSlug := project.Flatten(newCwd)
		mv := SlugMove{OldSlug: e.Name(), NewSlug: newSlug, OldCwd: cwd, NewCwd: newCwd}
		if newSlug != e.Name() {
			if _, statErr := os.Stat(filepath.Join(projectsDir, newSlug)); statErr == nil {
				mv.Collision = true
			}
		}
		p.Projects = append(p.Projects, mv)
	}
	sort.Slice(p.Projects, func(i, j int) bool { return p.Projects[i].OldSlug < p.Projects[j].OldSlug })
	return nil
}

// findDesktop records every Desktop session JSON file that references a path
// under Src (its cwd/originCwd/planPath move with the directory).
func (p *Plan) findDesktop(sessionsDir string) error {
	err := filepath.WalkDir(sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.SkipDir
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		if fileReferencesSrc(path, p.Src) {
			p.Desktop = append(p.Desktop, path)
		}
		return nil
	})
	if os.IsNotExist(err) {
		return nil
	}
	sort.Strings(p.Desktop)
	return err
}

// rebase maps a path rooted at src onto dst: src itself → dst, and src/x → dst/x.
// It returns the path unchanged with under=false when path is not under src.
func rebase(path, src, dst string) (string, bool) {
	if path == src {
		return dst, true
	}
	prefix := src + string(os.PathSeparator)
	if strings.HasPrefix(path, prefix) {
		return dst + string(os.PathSeparator) + path[len(prefix):], true
	}
	return path, false
}
