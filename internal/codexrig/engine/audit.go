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
	"github.com/rigsmith/rigsmith/internal/codexrig/rolloutstore"
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
	files, others, err := listStaged(staging)
	if err != nil {
		return nil, err
	}
	cache := newAuditCache(staging)

	var mu sync.Mutex
	var findings []redact.Finding

	// Everything that is not a content file is judged first, cheaply. A
	// non-regular entry is refused outright: it is not data this can scan,
	// and following it would read outside the tree. A part is fine only when
	// an index in the tree vouches for it — that owner's logical read is how
	// its bytes get scanned — and refused when nothing does.
	referenced := referencedParts(staging, files)
	for _, rel := range others {
		if rolloutstore.IsPartPath(rel) {
			if referenced[rel] {
				continue
			}
			findings = append(findings, redact.Finding{Path: rel, Kind: "chunk part no index references", File: true})
			continue
		}
		findings = append(findings, redact.Finding{Path: rel, Kind: "not a regular file", File: true})
	}

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
			// Through the store, so a rollout kept in parts is scanned as the
			// conversation it is. Scanning the index instead would read a few
			// hundred bytes of hashes and clear a file nobody looked at.
			f, err := rolloutstore.Open(abs)
			if err != nil {
				mu.Lock()
				findings = append(findings, redact.Finding{Path: rel, Kind: redact.KindUnreadable, File: true})
				mu.Unlock()
				return nil
			}
			finding, serr := redact.ScanReader(rel, f)
			_ = f.Close()
			// The physical file too, when it is an index: Decode keeps the
			// fields it knows and drops the rest, so a credential in an
			// unknown field would exist in the committed bytes and in nothing
			// the logical read ever showed the scanner.
			if serr == nil && finding == nil {
				if isIdx, ierr := rolloutstore.IsIndexFile(abs); ierr == nil && isIdx {
					raw, rerr := os.Open(abs)
					if rerr != nil {
						serr = rerr
					} else {
						finding, serr = redact.ScanReader(rel, raw)
						_ = raw.Close()
					}
				}
			}
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
	files, _, err := listStaged(staging)
	return files, err
}

// listStaged walks the staging tree and sorts what it finds into two lists:
// files — regular files that are content, for restore to write out — and
// others, everything else: symlinks and other non-regular entries, and chunk
// parts. The audit needs the second list as much as the first. When
// listStagedFiles alone existed and simply dropped those entries, a symlink
// in a cloned tree passed the audit by never being shown to it, and a part
// no index referenced was neither scanned nor refused.
func listStaged(staging string) (files, others []string, err error) {
	err = filepath.WalkDir(staging, func(p string, d fs.DirEntry, err error) error {
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
		if !d.Type().IsRegular() || rolloutstore.IsPartPath(rel) {
			others = append(others, rel)
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(files)
	sort.Strings(others)
	return files, others, nil
}

// referencedParts is every part path some chunk index in the tree vouches for.
// A part outside this set is bytes no index describes: the audit never reads
// it through an owner, so it must be refused rather than published unscanned.
func referencedParts(staging string, files []string) map[string]bool {
	out := map[string]bool{}
	for _, rel := range files {
		abs := filepath.Join(staging, filepath.FromSlash(rel))
		isIdx, err := rolloutstore.IsIndexFile(abs)
		if err != nil || !isIdx {
			continue
		}
		raw, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		idx, err := rolloutstore.Decode(raw)
		if err != nil {
			continue
		}
		for _, part := range idx.Parts {
			out[rolloutstore.PartPath(rel, part.Hash)] = true
		}
	}
	return out
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
