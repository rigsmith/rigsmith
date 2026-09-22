package cli

import (
	"encoding/json"
	"os"
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

// gitTag appends one git-tag event. The file is opened for append on every
// event, as canon's append-mode stream, so a caller can hand the same file to
// several commands and a run that stops partway still reports what it tagged.
func (e *tagEvents) gitTag(tag, packageName string) error {
	line, err := json.Marshal(struct {
		Type        string `json:"type"`
		Tag         string `json:"tag"`
		PackageName string `json:"packageName"`
	}{"git-tag", tag, packageName})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(e.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
