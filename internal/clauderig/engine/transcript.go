package engine

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/redact"
)

// errPrivateKeyInTranscript stops the scrub for a file carrying a PEM block the
// text rules cannot remove whole, so the caller can fall back to refusing it
// rather than staging a mangled copy.
var errPrivateKeyInTranscript = errors.New("transcript contains a private key block")

// isTranscript reports whether rel is a conversation rather than config. Only
// these are scrubbed: a secret in settings.json is there because Claude Code
// needs it, while a secret in a transcript is there because somebody pasted it.
func isTranscript(rel string) bool {
	return strings.HasPrefix(rel, "projects/") && strings.HasSuffix(rel, ".jsonl")
}

// conversationText reports whether a staged file is text belonging to a
// conversation, and so gets scrubbed when redactTranscripts is on.
//
// Wider than the transcript itself, because a pasted credential lands wherever
// the conversation put it: in the transcript, in a tool result written beside
// it, in a note under memory/. Scrubbing only .jsonl left four files on one
// real machine holding bearer tokens — the setting was on, the tripwire refused
// them anyway, and the sync stayed blocked with nothing left to try.
//
// .json is excluded deliberately: structured files go through the field-level
// redactor, which knows where a value ends. Images and other binaries are
// excluded because a byte-level rewrite of one is not a redaction, it is
// damage.
func conversationText(rel string) bool {
	return strings.HasPrefix(rel, "projects/")
}

// scrubbable reports whether a staged file should be scrubbed: it belongs to a
// conversation, and its content is text.
//
// Judged on content rather than on the extension. An allowlist gets both ends
// wrong: tool output written to a .log, or to a file with no extension at all,
// is text that would keep a credential and keep the sync refused; and a PNG
// somebody named .md would be handed to the rewriter, which would edit bytes
// inside an image. The head of the file answers the question directly.
// isJSONRecord reports a transcript line that is one JSON object, which is what
// every line of a conversation transcript is. It bounds what a rewrite can
// reach: a credential inside it is a string value, and cannot continue onto the
// next line.
func isJSONRecord(line []byte) bool {
	return bytes.HasPrefix(bytes.TrimLeft(line, " \t"), []byte("{"))
}

func scrubbable(rel, src string) bool {
	if !conversationText(rel) {
		return false
	}
	f, err := os.Open(src)
	if err != nil {
		return false // unreadable here means the copy will fail anyway
	}
	defer f.Close()
	var head [8000]byte
	n, rerr := f.Read(head[:])
	if rerr != nil && n == 0 {
		return false
	}
	return !redact.LooksBinary(head[:n])
}

// redactTranscript streams src to dst, replacing credential-shaped tokens, and
// reports what it took out.
//
// Streamed line by line rather than read whole: transcripts run to hundreds of
// megabytes. The final streaming audit independently verifies the result.
// JSONL makes this natural, one record per line.
//
// The staged file keeps the source's mtime so the incremental skip still
// recognises it next time, and is written via a temp file so an interrupted sync
// cannot leave a half-scrubbed transcript in staging.
// errBinaryContent means the file turned out to be binary partway through, so
// no rewrite of it can be safe. Not a failure: the caller copies the file as it
// stands, which is what would have happened had the head given it away.
var errBinaryContent = errors.New("binary content: not scrubbable")

func redactTranscript(dst, src string, mtime time.Time) (hits []redact.TextHit, err error) {
	in, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer func() {
		if in != nil {
			in.Close()
		}
	}()

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
			// The head said text, but only the head was read. A file can be
			// clean for 8 KB and binary after it, and rewriting a byte sequence
			// inside the binary part is damage: the bytes are not a credential,
			// they are pixels. Abandon the rewrite and let the caller copy the
			// file through untouched.
			if redact.LooksBinary(line) {
				return nil, errBinaryContent
			}
			// Inside a JSON record the key is a string value, and the text rule
			// stops at the closing quote — so the whole block goes, footer or
			// no footer. That matters: a key quoted in a conversation is
			// usually truncated and has no footer at all, which is why testing
			// for one rejected the very files this was meant to clear.
			//
			// Anything else is raw text, where the body runs on into lines this
			// loop copies through untouched and the scanner (which matches only
			// the header) would not notice. Still refused.
			if redact.HasPrivateKey(line) && !isJSONRecord(line) {
				return nil, errPrivateKeyInTranscript
			}
			out, found, changed := redact.RedactText(line)
			if changed {
				hits = append(hits, found...)
				line = out
			}
			// Belt and braces on the case just allowed through: if a marker
			// survived the rewrite, the rule did not span what it looked like
			// it spanned, and the rest of the key may still be here.
			if redact.HasPrivateKey(line) {
				return nil, errPrivateKeyInTranscript
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
	// Release the source before the rename. The orphan sweep scrubs a staged
	// file in place, so src and dst are the same path there — and Windows will
	// not rename over a file that is still open, where POSIX does not care.
	if err = in.Close(); err != nil {
		return nil, err
	}
	in = nil
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
