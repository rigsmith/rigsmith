package peek

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
	"github.com/rigsmith/rigsmith/internal/clauderig/project"
)

// ErrExists means the transcript is already on this machine. Materialise never
// overwrites: the local copy may be a session that is still running, or one that
// has moved on since the remote snapshot, and clobbering either loses turns
// nobody asked to lose. Restore learned this the hard way — see the live-session
// guard in engine/restore.go.
var ErrExists = errors.New("that session already exists on this machine")

// Materialized describes a completed copy.
type Materialized struct {
	// Path is the absolute local transcript that now exists.
	Path string
	// Slug is the local project directory it landed in — rewritten for this
	// machine's paths when the manifest knows the project, so the session shows
	// up under the right folder rather than the source machine's.
	Slug string
	// Rewrote reports whether the slug was translated for this machine.
	Rewrote bool
	Bytes   int
}

// Materialize copies one session out of ref into the local CLI root.
//
// Strictly additive: it creates the project directory if needed and writes the
// transcript only when nothing is there. Everything else about the machine is
// left alone — no config merge, no manifest update, no registry touch. This is
// the "bring that conversation over" operation, not a restore.
//
// projectsDir is the local ~/.claude/projects. man may be nil, in which case the
// source slug is used verbatim.
func Materialize(ctx context.Context, repo *gitrepo.Repo, ref string, s Session,
	projectsDir string, man *manifest.Manifest, res *pathmap.Resolver) (Materialized, error) {

	blob, err := Read(ctx, repo, ref, s)
	if err != nil {
		return Materialized{}, err
	}

	slug, rewrote := localSlug(s.Slug, man, res)
	dir := filepath.Join(projectsDir, slug)
	dst := filepath.Join(dir, s.ID+".jsonl")

	if _, err := os.Stat(dst); err == nil {
		return Materialized{Path: dst, Slug: slug, Rewrote: rewrote}, ErrExists
	} else if !os.IsNotExist(err) {
		return Materialized{}, err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Materialized{}, err
	}
	// O_EXCL so a session that appears between the stat and the write still
	// wins — the additive guarantee shouldn't hinge on a race.
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return Materialized{Path: dst, Slug: slug, Rewrote: rewrote}, ErrExists
		}
		return Materialized{}, err
	}
	n, werr := f.Write(blob)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return Materialized{}, fmt.Errorf("write %s: %w", dst, werr)
	}

	return Materialized{Path: dst, Slug: slug, Rewrote: rewrote, Bytes: n}, nil
}

// localSlug translates a source project directory into this machine's layout
// using the synced manifest, the same way restore does — otherwise a session
// from a machine with a different home lands under a folder that means nothing
// here. Falls back to the source slug when the project isn't in the manifest or
// can't be resolved for this machine, which is the "bring it over anyway" rule.
func localSlug(srcSlug string, man *manifest.Manifest, res *pathmap.Resolver) (string, bool) {
	if man == nil || res == nil {
		return srcSlug, false
	}
	p, ok := man.Projects[srcSlug]
	if !ok || p.Template == "" {
		return srcSlug, false
	}
	newSlug, _, st := project.RewriteFromTemplate(p.Template, res)
	if st != pathmap.StatusResolved || newSlug == "" || newSlug == srcSlug {
		return srcSlug, false
	}
	return newSlug, true
}
