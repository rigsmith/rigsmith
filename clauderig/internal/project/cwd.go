package project

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/core/pathmap"
)

// maxHeaderLines bounds how far into a transcript we scan for the cwd. The cwd
// sits in the session header (measured: within ~800 bytes / the first few lines
// for 188/197 real transcripts), so this never reads the multi-MB body; it is a
// backstop against a pathological cwd-less file.
const maxHeaderLines = 5000

// transcriptLine is the slice of a Claude Code transcript record we care about.
type transcriptLine struct {
	Cwd         string `json:"cwd"`
	IsSidechain bool   `json:"isSidechain"`
}

// CwdFromTranscript returns the session working directory recorded in a Claude
// Code transcript, scanning only the header region (it stops at the first match,
// so the common case reads a few hundred bytes — not the whole file). Sidechain
// (sub-agent) records are skipped so we get the session's own cwd. ok is false
// when no cwd is found within the header bound.
func CwdFromTranscript(path string) (cwd string, ok bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()

	// bufio.Reader (not Scanner) so a very long assistant line can't blow a token
	// cap; we read whole lines and decode each as JSON.
	br := bufio.NewReader(f)
	for i := 0; i < maxHeaderLines; i++ {
		line, rerr := br.ReadString('\n')
		if len(line) > 0 {
			var tl transcriptLine
			if json.Unmarshal([]byte(strings.TrimSpace(line)), &tl) == nil && tl.Cwd != "" && !tl.IsSidechain {
				return tl.Cwd, true, nil
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			return "", false, rerr
		}
	}
	return "", false, nil
}

// CwdFromProjectDir reads the cwd for a ~/.claude/projects/<slug> directory from
// the first transcript that yields one. All transcripts in a slug dir share the
// same cwd (it is the dir's identity), so one read suffices.
func CwdFromProjectDir(dir string) (cwd string, ok bool, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		c, found, rerr := CwdFromTranscript(filepath.Join(dir, e.Name()))
		if rerr != nil {
			return "", false, rerr
		}
		if found {
			return c, true, nil
		}
	}
	return "", false, nil
}

// RewriteFromTemplate resolves a portable cwd template (as stored in the manifest)
// for the target machine and returns the target slug and cwd. When the template
// can't resolve, it falls back to the un-tokenized template flattened as-is, with
// the resolver's status — keeping the "restore anyway" rule.
func RewriteFromTemplate(template string, target *pathmap.Resolver) (newSlug, newCwd string, status pathmap.Status) {
	res := target.Resolve(template)
	if !res.IsResolved() {
		return Flatten(template), template, res.Status
	}
	return Flatten(res.Path), res.Path, pathmap.StatusResolved
}
