package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/rigsmith/rigsmith/core/gitutil"
)

// tagEvents speaks @changesets v3's output contract: `changeset publish` and
// `changeset git-tag` append one NDJSON line per git tag they create to the
// file named by --output or $CHANGESETS_OUTPUT:
//
//	{"type":"git-tag","tag":"pkg@1.2.0","packageName":"pkg"}
//
// changesets/action (and shiprig-action) set CHANGESETS_OUTPUT, then push
// exactly those tags and create a GitHub release for each. The contract is
// also what makes the caller the owner of the push: with events on, the tags
// are created locally and never pushed here, as `changeset publish` never
// pushes, so no tag is pushed twice. As in canon, a tag that already exists,
// locally or on the remote, is skipped with no event.
type tagEvents struct {
	path string
}

// openTagEvents returns the event sink named by the flag, else by
// $CHANGESETS_OUTPUT; nil when neither is set.
func openTagEvents(flag string) *tagEvents {
	if flag == "" {
		flag = os.Getenv("CHANGESETS_OUTPUT")
	}
	if flag == "" {
		return nil
	}
	return &tagEvents{path: flag}
}

// gitTag appends one git-tag event, all or nothing: a write that fails
// partway is truncated back off, so the file never holds half a line.
// appended reports whether the whole line landed, which can be true even with
// an error (the close failed after the write).
func (e *tagEvents) gitTag(tag, packageName string) (appended bool, err error) {
	line, err := json.Marshal(struct {
		Type        string `json:"type"`
		Tag         string `json:"tag"`
		PackageName string `json:"packageName"`
	}{"git-tag", tag, packageName})
	if err != nil {
		return false, err
	}
	f, err := os.OpenFile(e.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return false, err
	}
	before, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		f.Close()
		return false, err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Truncate(before)
		f.Close()
		return false, err
	}
	return true, f.Close()
}

// create makes the tag and reports it, or reports nothing when the tag turned
// out to exist already: one another process created between the caller's
// check and this call is theirs, so it is neither reported nor, on a failed
// write, deleted.
func (e *tagEvents) create(ctx context.Context, repoRoot, tag, packageName string) (created bool, err error) {
	created, err = gitutil.CreateNewTag(ctx, repoRoot, tag, tag)
	if err != nil || !created {
		return false, err
	}
	return true, e.record(ctx, repoRoot, tag, packageName)
}

// record reports a tag this run just created. When its event could not be
// appended, the tag is deleted again: left in place, a retry would find it
// existing, skip it, and never report it, so the caller would never push or
// release it. When the event did land (only the close failed), the tag stays,
// or a retry would report it twice.
func (e *tagEvents) record(ctx context.Context, repoRoot, tag, packageName string) error {
	appended, err := e.gitTag(tag, packageName)
	if err == nil {
		return nil
	}
	if !appended {
		if derr := gitutil.DeleteTag(ctx, repoRoot, tag); derr != nil {
			return fmt.Errorf("recording tag %s in %s: %w (and removing the tag again failed: %w)", tag, e.path, err, derr)
		}
		return fmt.Errorf("recording tag %s in %s (tag removed, so a retry creates and reports it): %w", tag, e.path, err)
	}
	return fmt.Errorf("recording tag %s in %s (the event was written): %w", tag, e.path, err)
}
