package engine

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/rigsmith/rigsmith/internal/agentrig/redact"
	"golang.org/x/sync/errgroup"
)

// Audit reads every file in the staging tree and reports anything that looks
// like a credential.
//
// It is not the same check as the per-file scan on the way in, and having both
// is deliberate. The inbound scan sees the bytes of files this run touched; the
// audit sees the bytes that are about to be PUBLISHED, including everything an
// older codexrig staged before a rule existed, everything a merge brought in
// from another machine, and everything a redactor missed. A tool whose promise is
// "no secret leaves this machine" has to check the thing it is actually sending.
//
// It fails closed: a file that cannot be read is a finding, not a pass.
func Audit(staging string) ([]redact.Finding, error) {
	files, err := listStagedFiles(staging)
	if err != nil {
		return nil, err
	}
	cache := newAuditCache(staging)

	var mu sync.Mutex
	var findings []redact.Finding

	g := new(errgroup.Group)
	g.SetLimit(runtime.NumCPU())
	for _, rel := range files {
		rel := rel
		g.Go(func() error {
			abs := filepath.Join(staging, filepath.FromSlash(rel))
			st, err := os.Lstat(abs)
			if err != nil {
				mu.Lock()
				findings = append(findings, redact.Finding{Path: rel, Kind: redact.KindUnreadable, File: true})
				mu.Unlock()
				return nil
			}
			// A symlink or a device node in a backup tree is not data we can
			// scan, and following it would read outside the tree.
			if !st.Mode().IsRegular() {
				mu.Lock()
				findings = append(findings, redact.Finding{Path: rel, Kind: "not a regular file", File: true})
				mu.Unlock()
				return nil
			}
			entry := auditEntry{size: st.Size(), mod: st.ModTime().UnixNano()}
			if cache.wasClean(rel, entry) {
				return nil
			}
			f, err := os.Open(abs)
			if err != nil {
				mu.Lock()
				findings = append(findings, redact.Finding{Path: rel, Kind: redact.KindUnreadable, File: true})
				mu.Unlock()
				return nil
			}
			finding, serr := redact.ScanReader(rel, f)
			_ = f.Close()
			switch {
			case serr != nil:
				mu.Lock()
				findings = append(findings, redact.Finding{Path: rel, Kind: redact.KindUnreadable, File: true})
				mu.Unlock()
			case finding != nil:
				mu.Lock()
				findings = append(findings, *finding)
				mu.Unlock()
			default:
				cache.note(rel, entry)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].Path < findings[j].Path })
	if len(findings) == 0 {
		// Only a run that found nothing anywhere may write the cache. A partial
		// verdict would let the next run skip a file this one never cleared.
		cache.save()
	}
	return findings, nil
}

// CheckPublish is the audit as a gate: it runs immediately before a commit or a
// push, so a merge that brought in another machine's work cannot slip a
// credential past the scan that ran before it.
func CheckPublish(staging string) error {
	findings, err := Audit(staging)
	if err != nil {
		return err
	}
	if len(findings) == 0 {
		return nil
	}
	return tripwireError(findings)
}

func listStagedFiles(staging string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(staging, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		rel, rerr := filepath.Rel(staging, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			// git's own object store is not our data, and walking it would
			// double the work for nothing.
			if rel == ".git" || strings.HasPrefix(rel, ".git/") {
				return fs.SkipDir
			}
			return nil
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// --- the audit cache -----------------------------------------------------
//
// Without it every sync reads the whole staged tree twice — once on the way in
// and once here — to conclude that three files moved. The cache remembers the
// exact (size, mtime) of bytes that were READ AND FOUND CLEAN, so a file is
// skipped only when nothing about it has changed since a run that cleared it.
// It lives beside the staging repo, not inside it: it is this machine's record of
// its own reads, and it must never be shared or committed.

const auditCacheName = ".audit-cache"
const auditCacheHeader = "codexrig-audit 1"

type auditEntry struct {
	size int64
	mod  int64
}

type auditCache struct {
	path string
	mu   sync.Mutex
	prev map[string]auditEntry
	now  map[string]auditEntry
}

func newAuditCache(staging string) *auditCache {
	c := &auditCache{
		prev: map[string]auditEntry{},
		now:  map[string]auditEntry{},
	}
	if staging == "" {
		return c
	}
	c.path = filepath.Join(filepath.Dir(staging), auditCacheName)
	b, err := os.ReadFile(c.path)
	if err != nil {
		return c
	}
	lines := strings.Split(string(b), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != auditCacheHeader {
		// A cache from another version is discarded rather than guessed at.
		return c
	}
	for _, line := range lines[1:] {
		// "<size> <mtime-ns> <rel>" — rel last, because a path may contain a
		// space and the numbers may not.
		parts := strings.SplitN(strings.TrimRight(line, "\r"), " ", 3)
		if len(parts) != 3 {
			continue
		}
		size, err1 := strconv.ParseInt(parts[0], 10, 64)
		mod, err2 := strconv.ParseInt(parts[1], 10, 64)
		if err1 != nil || err2 != nil || parts[2] == "" {
			continue
		}
		c.prev[parts[2]] = auditEntry{size: size, mod: mod}
	}
	return c
}

func (c *auditCache) wasClean(rel string, e auditEntry) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	got, ok := c.prev[rel]
	if ok && got == e {
		c.now[rel] = e
		return true
	}
	return false
}

func (c *auditCache) note(rel string, e auditEntry) {
	c.mu.Lock()
	c.now[rel] = e
	c.mu.Unlock()
}

func (c *auditCache) save() {
	if c.path == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	rels := make([]string, 0, len(c.now))
	for rel := range c.now {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	var b strings.Builder
	b.WriteString(auditCacheHeader)
	b.WriteByte('\n')
	for _, rel := range rels {
		e := c.now[rel]
		b.WriteString(strconv.FormatInt(e.size, 10))
		b.WriteByte(' ')
		b.WriteString(strconv.FormatInt(e.mod, 10))
		b.WriteByte(' ')
		b.WriteString(rel)
		b.WriteByte('\n')
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, c.path)
}

// wasCleanFor lets the sync pass consult the same verdicts without writing any:
// the audit owns the cache, and a writer here could clear a file the audit never
// read.
func (c *auditCache) wasCleanFor(rel string, e auditEntry) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	got, ok := c.prev[rel]
	return ok && got == e
}
