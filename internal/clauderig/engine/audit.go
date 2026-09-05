package engine

import (
	"fmt"
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
	if _, err := transcript.Enabled(root); err != nil {
		return nil, err
	}
	var findings []redact.Finding
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if os.IsNotExist(e) && p == root {
			return nil
		}
		if e != nil {
			return e
		}
		if d.IsDir() {
			if p == filepath.Join(root, ".git") {
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
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		f, e := transcript.Open(p)
		if e != nil {
			return e
		}
		finding, e := redact.ScanReader(filepath.ToSlash(rel), f)
		f.Close()
		if e != nil {
			return e
		}
		if finding != nil {
			findings = append(findings, *finding)
		}
		return nil
	})
	return findings, err
}
func CheckPublish(root string) error {
	findings, err := Audit(root)
	if err != nil {
		return err
	}
	if len(findings) > 0 {
		return fmt.Errorf("secret tripwire: refusing publication: %s (%s); %d affected file(s)", findings[0].Path, findings[0].Kind, len(findings))
	}
	return nil
}
