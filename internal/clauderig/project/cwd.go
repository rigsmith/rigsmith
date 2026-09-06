package project

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
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
	f, err := transcript.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	return CwdFrom(f)
}

// CwdFrom is CwdFromTranscript over an already-open stream, for a transcript that
// isn't a file on disk — a blob read out of git history, say.
func CwdFrom(r io.Reader) (cwd string, ok bool, err error) {
	// bufio.Reader (not Scanner) so a very long assistant line can't blow a token
	// cap — but read it BOUNDED. ReadString allocates the whole record, so a
	// transcript with one enormous newline-free line set this process's memory
	// ceiling, and `desktop open --session <text>` reads every live transcript.
	br := bufio.NewReader(r)
	for i := 0; i < maxHeaderLines; i++ {
		line, rerr := readBoundedLine(br)
		if len(line) > 0 {
			var tl transcriptLine
			if json.Unmarshal([]byte(strings.TrimSpace(string(line))), &tl) == nil && tl.Cwd != "" && !tl.IsSidechain {
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

// maxTranscriptLineBytes bounds one record. The header lines carrying cwd are
// small; anything larger is an assistant payload this never needs, and a
// truncated record simply fails to parse and is skipped.
const maxTranscriptLineBytes = 1 << 20 // 1 MiB

// readBoundedLine returns at most maxTranscriptLineBytes of the next line and
// discards the remainder, so an unbroken multi-gigabyte record costs a fixed
// buffer rather than its own size. ReadLine never allocates past its internal
// buffer, which is what makes the discard free.
func readBoundedLine(br *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, more, err := br.ReadLine()
		if room := maxTranscriptLineBytes - len(buf); room > 0 && len(chunk) > 0 {
			if len(chunk) > room {
				chunk = chunk[:room]
			}
			buf = append(buf, chunk...)
		}
		if !more || err != nil {
			return buf, err
		}
	}
}
