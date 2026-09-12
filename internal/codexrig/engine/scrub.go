package engine

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/redact"
)

// Scrubbing rewrites the STAGED copy of a rollout to take credential-shaped
// tokens out of the conversation text. The live ~/.codex file is never touched:
// codexrig backs a machine up, it does not edit it, so the secret stays where
// the user left it.
//
// It is off by default, and that is a considered position rather than caution
// for its own sake. Rewriting the middle of a conversation is a thing a backup
// tool should do only because it was asked to — the result is no longer the
// bytes Codex wrote, and a rollout that has been edited is one whose replay
// nobody can vouch for. The publication scan runs either way, so a recognised
// credential is refused whether or not the scrubber is on; what the setting buys
// is the ability to carry a rollout that would otherwise be refused forever.

var (
	// errPrivateKey means the rollout holds PEM key material. That is not
	// something to rewrite around — a key inside a conversation is a key, and
	// the file is refused rather than partially cleaned.
	errPrivateKey = errors.New("rollout contains private key material")
	// errBinary means the file looked like text at the head and was not. The
	// caller falls back to a verbatim copy so nothing is corrupted, and the
	// whole-tree audit still reads it.
	errBinary = errors.New("binary content")
)

// scrubLineBytes caps a single line. A rollout line is one JSON record and is
// normally small; a file with one enormous newline-free line would otherwise set
// the process's memory ceiling.
const scrubLineBytes = 8 << 20

// scrubInto writes a scrubbed copy of src to dst, line at a time so a large
// rollout is never held whole, and stamps it with the source's mtime so the
// incremental skip recognises it next run.
func scrubInto(dst, src string, mod time.Time) ([]redact.TextHit, error) {
	in, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer in.Close()

	// Sniff before rewriting. A file that is binary from the start is caught
	// here; one that turns binary later is caught mid-stream below, and in both
	// cases the caller copies it verbatim instead.
	head := make([]byte, 8000)
	n, _ := io.ReadFull(in, head)
	if redact.LooksBinary(head[:n]) {
		return nil, errBinary
	}
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".scrub-*")
	if err != nil {
		return nil, err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()

	var hits []redact.TextHit
	r := bufio.NewReaderSize(in, 64<<10)
	w := bufio.NewWriterSize(tmp, 64<<10)
	for {
		line, rerr := readBoundedLine(r)
		if len(line) > 0 {
			if redact.HasPrivateKey(line) {
				tmp.Close()
				return nil, errPrivateKey
			}
			if redact.LooksBinary(line) {
				tmp.Close()
				return nil, errBinary
			}
			out, lineHits, _ := redact.RedactText(line)
			hits = append(hits, lineHits...)
			if _, werr := w.Write(out); werr != nil {
				tmp.Close()
				return nil, werr
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				break
			}
			tmp.Close()
			return nil, rerr
		}
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(name, dst); err != nil {
		return nil, err
	}
	if err := os.Chtimes(dst, mod, mod); err != nil {
		return nil, err
	}
	return hits, nil
}

// readBoundedLine returns one line INCLUDING its newline, capped. Returning the
// newline matters: a scrub that dropped it would silently join two JSON records
// into one unparseable line.
func readBoundedLine(r *bufio.Reader) ([]byte, error) {
	var out []byte
	for {
		chunk, err := r.ReadSlice('\n')
		out = append(out, chunk...)
		if err == nil {
			return out, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			if len(out) > scrubLineBytes {
				// Past the cap the line is not a JSON record any more. Stop
				// reading it rather than growing without bound.
				return out, io.EOF
			}
			continue
		}
		return out, err
	}
}
