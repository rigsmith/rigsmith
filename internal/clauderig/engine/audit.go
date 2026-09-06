package engine

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/rigsmith/rigsmith/internal/clauderig/redact"
	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
)

// Audit checks all bytes eligible for publication, including files restored
// from another machine and unchanged files staged by older clauderig versions.
// Chunk files are also checked through their logical transcript, including
// cross-chunk token boundaries. Errors fail closed. No credentials are logged.
//
// Files found clean are remembered by size and mtime (see auditCache), so a
// publish re-reads only what has moved since. The structural checks below —
// symlinks, non-regular files — are NOT cached: they cost a stat, and they are
// how a swapped file type gets caught.
func Audit(root string) ([]redact.Finding, error) {
	return audit(root, transcript.Open)
}

func audit(root string, open func(string) (transcript.File, error)) ([]redact.Finding, error) {
	if _, err := transcript.Enabled(root); err != nil {
		return nil, err
	}
	// The walk is structure only: it is cheap, it must see every file, and the
	// order it visits them in is what tells an owner from its parts.
	var owners, parts []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
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
		if strings.HasSuffix(filepath.Base(filepath.Dir(p)), transcript.Suffix) {
			parts = append(parts, p)
			return nil
		}
		owners = append(owners, p)
		return nil
	})
	if err != nil {
		return nil, err
	}

	cache := newAuditCache(root)
	// Owners first, and only then the parts they did not claim: reading a
	// transcript through its logical form covers its parts, including tokens
	// that straddle a chunk boundary, which reading a part alone cannot.
	referenced := &sync.Map{}
	findings, err := scanFiles(root, owners, open, cache, referenced)
	if err != nil {
		return findings, err
	}
	remaining := parts[:0]
	for _, p := range parts {
		if _, claimed := referenced.Load(p); !claimed {
			remaining = append(remaining, p)
		}
	}
	rest, err := scanFiles(root, remaining, open, cache, referenced)
	findings = append(findings, rest...)
	if err != nil {
		return findings, err
	}
	// Only a completed audit may leave verdicts behind. Findings are never
	// cached: they have to be reported again until they are dealt with.
	if len(findings) == 0 {
		cache.save()
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].Path < findings[j].Path })
	return findings, nil
}

// scanFiles reads the given staged files concurrently. Scanning is CPU-bound —
// regex over gigabytes — so it gets a worker per core rather than the one the
// sequential walk gave it.
func scanFiles(root string, paths []string, open func(string) (transcript.File, error),
	cache *auditCache, referenced *sync.Map) ([]redact.Finding, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	var mu sync.Mutex
	var findings []redact.Finding
	g := new(errgroup.Group)
	g.SetLimit(runtime.NumCPU())
	for _, p := range paths {
		g.Go(func() error {
			found, err := scanStaged(root, p, open, cache, referenced)
			if err != nil {
				return err
			}
			if len(found) > 0 {
				mu.Lock()
				findings = append(findings, found...)
				mu.Unlock()
			}
			return nil
		})
	}
	err := g.Wait()
	return findings, err
}

func scanStaged(root, p string, open func(string) (transcript.File, error),
	cache *auditCache, referenced *sync.Map) ([]redact.Finding, error) {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return nil, err
	}
	rel = filepath.ToSlash(rel)
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	entry := auditEntry{size: info.Size(), mod: info.ModTime().UnixNano()}

	f, err := open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var findings []redact.Finding
	parts, packed := transcript.StoredParts(f)
	if packed {
		for _, part := range parts {
			referenced.Store(filepath.Join(p+transcript.Suffix, part.Hash+".part"), true)
		}
		// Audit the physical index too: unknown fields and duplicate JSON keys
		// can contain bytes that disappear from the decoded structure.
		raw, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		finding, err := redact.ScanReader(rel, raw)
		raw.Close()
		if err != nil {
			return nil, err
		}
		if finding != nil {
			findings = append(findings, *finding)
		}
	} else if cache.wasClean(rel, entry) {
		// Only a file that stands alone can be taken on trust. A packed
		// transcript's index says nothing about the parts it points at, so a
		// cached verdict on the owner would hide a chunk that has been deleted
		// or swapped underneath it — the logical read below is what catches
		// that, and it has to happen every time.
		return nil, nil
	}
	finding, err := redact.ScanReader(rel, f)
	if err != nil {
		return nil, err
	}
	if finding != nil {
		findings = append(findings, *finding)
	}
	if len(findings) == 0 && !packed {
		cache.note(rel, entry)
	}
	return findings, nil
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
