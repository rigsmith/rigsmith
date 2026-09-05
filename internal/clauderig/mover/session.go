package mover

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/internal/clauderig/project"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
)

// SessionMove is what re-rooting one session did, or would do.
type SessionMove struct {
	ID      string
	OldCwd  string
	NewCwd  string
	OldPath string
	NewPath string
	// Records is how many transcript records had their cwd rewritten.
	Records int
	// Moved is whether the transcript changed project directory. False when the
	// new root flattens to the same slug, which is legal and simply means the
	// file stays where it is.
	Moved bool
}

// ErrSessionNotFound is returned when no transcript on this machine has that id.
var ErrSessionNotFound = errors.New("no transcript with that session id on this machine")

// FindSession locates a session's transcript under projectsDir.
func FindSession(projectsDir, id string) (path, cwd string, err error) {
	// Claude Code files transcripts under a lowercase uuid, and Glob matches
	// case-sensitively even where the filesystem would not. An id pasted from
	// somewhere that upper-cased it has to find the same session.
	id = session.CanonicalID(id)
	matches, err := filepath.Glob(filepath.Join(projectsDir, "*", id+".jsonl"))
	if err != nil {
		return "", "", err
	}
	if len(matches) == 0 {
		return "", "", ErrSessionNotFound
	}
	// A session id is unique, but a restore or a hand-copied tree can leave two
	// files with the same name in different slugs. Refusing beats picking one
	// and rewriting the wrong copy.
	if len(matches) > 1 {
		return "", "", fmt.Errorf("%s is filed in %d places at once — resolve that first: %s",
			id, len(matches), strings.Join(matches, ", "))
	}
	path = matches[0]
	cwd, ok, err := project.CwdFromProjectDir(filepath.Dir(path))
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", "", fmt.Errorf("cannot read the current directory of %s", id)
	}
	return path, cwd, nil
}

// MoveSession re-roots one session onto newCwd: the records that were recorded
// at its current root are rewritten to the new one, and the transcript moves
// into the project directory that root belongs to.
//
// This is NOT [Plan.Apply]. That one exists because a directory MOVED on disk,
// so every path underneath it moved with it and has to be rebased. Here nothing
// moved: the session was simply filed under the directory Claude Code happened
// to start in, and you are saying where it belongs. So only the records sitting
// at the old root are rewritten — records from deeper directories name real
// paths that still exist and still point at the right place.
//
// The caller decides whether this is a good idea. There is no heuristic here on
// purpose: it does the mechanics of a move you have already chosen.
func MoveSession(projectsDir, id, newCwd string, dryRun bool) (SessionMove, error) {
	var mv SessionMove
	if !filepath.IsAbs(newCwd) {
		return mv, fmt.Errorf("give an absolute directory, not %q", newCwd)
	}
	newCwd = filepath.Clean(newCwd)
	// The id names the destination file, so it has to be the canonical one
	// whatever casing the caller typed.
	id = session.CanonicalID(id)

	path, oldCwd, err := FindSession(projectsDir, id)
	if err != nil {
		return mv, err
	}
	mv = SessionMove{ID: id, OldCwd: oldCwd, NewCwd: newCwd, OldPath: path, NewPath: path}
	if oldCwd == newCwd {
		return mv, nil
	}

	newDir := filepath.Join(projectsDir, project.Flatten(newCwd))
	mv.NewPath = filepath.Join(newDir, id+".jsonl")
	mv.Moved = mv.NewPath != mv.OldPath

	// A transcript already sitting at the destination is someone else's — the
	// same session filed twice — and overwriting it would lose a conversation.
	// Stat only reports what was true a moment ago; the create below is what
	// actually holds the line.
	if mv.Moved {
		if dryRun {
			if _, serr := os.Stat(mv.NewPath); serr == nil {
				return mv, fmt.Errorf("%s already exists — that session is already filed under %s", mv.NewPath, newCwd)
			}
		} else if err := os.MkdirAll(newDir, 0o755); err != nil {
			return mv, err
		}
	}

	// Exact matches only, which is what makes this a re-root rather than a
	// rebase: rewriting anything that merely sits UNDER the old root would turn
	// /a/sub into /b/sub for a directory that never moved and is still there.
	//
	// Rewriting straight into the new home rather than in place and renaming
	// after: a failure between those two steps used to leave the transcript
	// under the old slug carrying the new root. Here the source is untouched
	// until a complete copy exists at the destination.
	dest := ""
	if mv.Moved {
		dest = mv.NewPath
	}
	n, err := rewriteExactCwd(path, dest, oldCwd, newCwd, dryRun)
	if err != nil {
		return mv, err
	}
	mv.Records = n

	if dryRun || !mv.Moved {
		return mv, nil
	}
	if err := os.Remove(path); err != nil {
		return mv, err
	}
	return mv, nil
}
