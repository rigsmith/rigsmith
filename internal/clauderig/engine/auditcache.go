package engine

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// auditVersion invalidates every cached verdict at once. Bump it whenever the
// scanner learns a new credential shape: a file found clean by the old rules
// has not been read by the new ones, and a cache that outlived its rules is
// worse than no cache.
const auditVersion = "1"

// auditCache remembers which staged files were found clean, so a publish reads
// only what has changed since. Without it the audit re-read the whole tree on
// every sync — nearly four minutes here to conclude that three files had moved.
//
// A file is cached only when it is CLEAN. A finding is never remembered: it has
// to be reported again on every run until it is dealt with, which is the whole
// point of refusing.
//
// Keyed on size and mtime, the same evidence the incremental sync trusts to
// decide a staged file is current. A file whose bytes change without either
// moving is invisible to both, and staging is written by this process alone.
//
// Lives beside the staging repo, never inside it: it describes what THIS
// machine has read, and everything in the tree is committed and shared.
type auditCache struct {
	path  string
	mu    sync.Mutex
	clean map[string]auditEntry
	found map[string]auditEntry // clean this run, to be written out
}

type auditEntry struct {
	size int64
	mod  int64
}

func newAuditCache(root string) *auditCache {
	c := &auditCache{
		clean: map[string]auditEntry{},
		found: map[string]auditEntry{},
	}
	if root == "" {
		return c
	}
	c.path = filepath.Join(filepath.Dir(root), ".audit-cache")
	f, err := os.Open(c.path)
	if err != nil {
		return c
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64<<10), 1<<20)
	if !s.Scan() || s.Text() != "clauderig-audit "+auditVersion {
		return c // absent, unreadable or stale reads as "nothing is known"
	}
	for s.Scan() {
		size, mod, rel, ok := parseAuditLine(s.Text())
		if ok {
			c.clean[rel] = auditEntry{size: size, mod: mod}
		}
	}
	return c
}

func parseAuditLine(line string) (size, mod int64, rel string, ok bool) {
	a, rest, found := strings.Cut(line, " ")
	if !found {
		return 0, 0, "", false
	}
	b, rel, found := strings.Cut(rest, " ")
	if !found || rel == "" {
		return 0, 0, "", false
	}
	size, err := strconv.ParseInt(a, 10, 64)
	if err != nil {
		return 0, 0, "", false
	}
	mod, err = strconv.ParseInt(b, 10, 64)
	if err != nil {
		return 0, 0, "", false
	}
	return size, mod, rel, true
}

// wasClean reports a file already read and found clean at exactly this size and
// mtime, and carries the verdict forward so it survives into the next run.
func (c *auditCache) wasClean(rel string, e auditEntry) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if got, ok := c.clean[rel]; !ok || got != e {
		return false
	}
	c.found[rel] = e
	return true
}

// note records a clean verdict reached this run.
func (c *auditCache) note(rel string, e auditEntry) {
	if strings.ContainsAny(rel, "\n\r") {
		return // unrepresentable in the file; it just gets read again
	}
	c.mu.Lock()
	c.found[rel] = e
	c.mu.Unlock()
}

// save writes the verdicts this run stands behind. Only called when the audit
// completed: a walk that stopped early has no opinion about what it never
// reached, and writing then would cache its ignorance.
//
// Best-effort. A cache that cannot be written costs the next run a full read,
// which is the harmless direction.
func (c *auditCache) save() {
	if c.path == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "clauderig-audit %s\n", auditVersion)
	for rel, e := range c.found {
		fmt.Fprintf(&b, "%d %d %s\n", e.size, e.mod, rel)
	}
	tmp, err := os.CreateTemp(filepath.Dir(c.path), ".audit-cache-*")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	_ = os.Rename(tmp.Name(), c.path)
}
