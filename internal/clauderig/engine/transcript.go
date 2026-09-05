package engine

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/redact"
)

// errPrivateKeyInTranscript stops the scrub for a file carrying a PEM block, so
// the caller can fall back to refusing it rather than staging a mangled copy.
var errPrivateKeyInTranscript = errors.New("transcript contains a private key block")

// isTranscript reports whether rel is a conversation rather than config. Only
// these are scrubbed: a secret in settings.json is there because Claude Code
// needs it, while a secret in a transcript is there because somebody pasted it.
func isTranscript(rel string) bool {
	return strings.HasPrefix(rel, "projects/") && strings.HasSuffix(rel, ".jsonl")
}

// redactTranscript streams src to dst, replacing credential-shaped tokens, and
// reports what it took out.
//
// Streamed line by line rather than read whole: transcripts run to hundreds of
// megabytes, which is also why the 64 KB content-scan limit skips them entirely
// today — a pasted key in a real conversation has never been examined at all.
// JSONL makes this natural, one record per line.
//
// The staged file keeps the source's mtime so the incremental skip still
// recognises it next time, and is written via a temp file so an interrupted sync
// cannot leave a half-scrubbed transcript in staging.
func redactTranscript(dst, src string, mtime time.Time) (hits []redact.TextHit, err error) {
	in, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".clauderig-redact-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		if err != nil {
			os.Remove(tmpName)
		}
	}()

	// bufio.Reader, not Scanner: a single transcript line can exceed any token
	// limit Scanner will accept, and a line that fails to scan would be dropped
	// from the staged copy rather than merely unscrubbed.
	r := bufio.NewReaderSize(in, 64<<10)
	w := bufio.NewWriterSize(tmp, 64<<10)
	for {
		line, rerr := r.ReadBytes('\n')
		if len(line) > 0 {
			if redact.HasPrivateKey(line) {
				return nil, errPrivateKeyInTranscript
			}
			out, found, changed := redact.RedactText(line)
			if changed {
				hits = append(hits, found...)
				line = out
			}
			if _, werr := w.Write(line); werr != nil {
				return nil, werr
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			return nil, rerr
		}
	}
	if err = w.Flush(); err != nil {
		return nil, err
	}
	if err = tmp.Close(); err != nil {
		return nil, err
	}
	if err = os.Chtimes(tmpName, mtime, mtime); err != nil {
		return nil, err
	}
	if err = os.Rename(tmpName, dst); err != nil {
		return nil, err
	}
	return hits, nil
}

// kindsOf collapses hits to the distinct rule names, in first-seen order. The
// journal wants "an anthropic key and a JWT", not twelve repetitions of one.
func kindsOf(hits []redact.TextHit) []string {
	seen := map[string]bool{}
	var out []string
	for _, h := range hits {
		if seen[h.Kind] {
			continue
		}
		seen[h.Kind] = true
		out = append(out, h.Kind)
	}
	return out
}
