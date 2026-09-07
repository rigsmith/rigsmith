package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/rigsmith/rigsmith/internal/clauderig/redact"
	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
)

// Audit checks all bytes eligible for publication, including files restored
// from another machine and unchanged files staged by older clauderig versions.
// Chunk files are also checked through their logical transcript, including
// cross-chunk token boundaries. Errors fail closed. No credentials are logged.
func Audit(root string) ([]redact.Finding, error) {
	return audit(root, transcript.Open)
}

// AuditContext is Audit with cancellation between files and streaming reads.
func AuditContext(ctx context.Context, root string) ([]redact.Finding, error) {
	return auditContext(ctx, root, transcript.Open)
}

func audit(root string, open func(string) (transcript.File, error)) ([]redact.Finding, error) {
	return auditContext(context.Background(), root, open)
}

func auditContext(ctx context.Context, root string, open func(string) (transcript.File, error)) ([]redact.Finding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := transcript.Enabled(root); err != nil {
		return nil, err
	}
	var findings []redact.Finding
	referenced := make(map[string]bool)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if os.IsNotExist(e) && p == root {
			return nil
		}
		if e != nil {
			return e
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink in staging: %s", p)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("refusing non-regular staging file: %s", p)
		}
		// WalkDir visits an owner before its sibling .chunks directory. Check
		// file types above even for parts already verified by the logical read.
		if referenced[p] {
			return nil
		}
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		f, e := open(p)
		if e != nil {
			return e
		}
		if parts, packed := transcript.StoredParts(f); packed {
			// Audit the physical index too: unknown fields and duplicate JSON
			// keys can contain bytes that disappear from the decoded structure.
			raw, err := os.Open(p)
			if err != nil {
				f.Close()
				return err
			}
			finding, err := redact.ScanReader(filepath.ToSlash(rel), auditReader{ctx, raw})
			raw.Close()
			if err != nil {
				f.Close()
				return err
			}
			if finding != nil {
				findings = append(findings, *finding)
			}
			for _, part := range parts {
				referenced[filepath.Join(p+transcript.Suffix, part.Hash+".part")] = true
			}
		}
		finding, e := redact.ScanReader(filepath.ToSlash(rel), auditReader{ctx, f})
		f.Close()
		if e != nil {
			return e
		}
		if finding != nil {
			findings = append(findings, *finding)
		}
		return nil
	})
	if err == nil {
		err = ctx.Err()
	}
	return findings, err
}
func CheckPublish(root string) error {
	return CheckPublishContext(context.Background(), root)
}

// ErrSecretTripwire identifies rejected bytes without parsing diagnostic text.
var ErrSecretTripwire = errors.New("secret tripwire")

// CheckPublishContext preserves the publication tripwire while allowing a worker
// to stop a whole-tree audit without waiting for every transcript to be scanned.
func CheckPublishContext(ctx context.Context, root string) error {
	findings, err := AuditContext(ctx, root)
	if err != nil {
		return err
	}
	if len(findings) > 0 {
		return fmt.Errorf("%w: refusing publication: %s (%s); %d affected file(s)", ErrSecretTripwire, findings[0].Path, findings[0].Kind, len(findings))
	}
	return nil
}

type auditReader struct {
	ctx context.Context
	r   io.Reader
}

func (r auditReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}
