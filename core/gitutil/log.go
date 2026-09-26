package gitutil

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Commit is a single commit read from the log, with the files it touched. It is
// the structured input commit-based versioning synthesizes changesets from
// (see core/commitsource).
type Commit struct {
	// Hash is the full commit SHA.
	Hash string
	// Short is git's unique abbreviation of Hash in this repository: at
	// least 7 characters, longer where 7 would be ambiguous.
	Short string
	// Subject is the first line of the message (the conventional-commit header).
	Subject string
	// Body is everything after the subject (blank-line separated), used to find
	// a `BREAKING CHANGE:` footer.
	Body string
	// Files are the absolute paths of the files the commit changed, resolved
	// against the repo root (mirroring ChangedFilesSince), so the same
	// path-attribution logic applies.
	Files []string
}

// Record/field separators chosen from the ASCII control range so they never
// collide with commit text. %x1e starts each commit record, %x1f delimits
// fields; the file list (from --name-only) follows the final field.
const (
	logRecordSep = "\x1e"
	logFieldSep  = "\x1f"
)

var logFormat = strings.Join([]string{"%H", "%h", "%s", "%b"}, logFieldSep)

// LogSince returns the commits reachable from HEAD but not from ref, newest
// first, each with the files it changed. An empty ref reads the entire history
// (the caller's package has no prior release tag). An invalid ref (or absent
// git/repo) is an error — the caller surfaces it rather than treating it as "no
// commits".
func LogSince(ctx context.Context, dir, ref string) ([]Commit, error) {
	root, err := runGit(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("gitutil: not a git repository: %w", err)
	}
	repoRoot := strings.TrimSpace(root)

	// --abbrev=7: %h is at least 7 characters whatever core.abbrev says,
	// and git lengthens it until it's unique.
	args := []string{"log", "--name-only", "--no-renames", "--abbrev=7", "--pretty=format:" + logRecordSep + logFormat + logFieldSep}
	if strings.TrimSpace(ref) != "" {
		// A tag rendered from a template could start with a dash; after
		// --end-of-options it's a revision, never an option.
		args = append(args, "--end-of-options", ref+"..HEAD")
	}
	out, err := runGit(ctx, dir, args...)
	if err != nil {
		return nil, fmt.Errorf("gitutil: log since %q: %w", ref, err)
	}

	var commits []Commit
	for _, rec := range strings.Split(out, logRecordSep) {
		if strings.TrimSpace(rec) == "" {
			continue
		}
		// rec = hash <FS> short <FS> subject <FS> body <FS> \n file1 \n file2 …
		fields := strings.SplitN(rec, logFieldSep, 5)
		if len(fields) < 5 {
			continue
		}
		c := Commit{
			Hash:    strings.TrimSpace(fields[0]),
			Short:   strings.TrimSpace(fields[1]),
			Subject: strings.TrimSpace(fields[2]),
			Body:    strings.TrimSpace(fields[3]),
		}
		for _, line := range strings.Split(fields[4], "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			c.Files = append(c.Files, filepath.Join(repoRoot, filepath.FromSlash(line)))
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// FileHistory returns the full SHAs of the commits that changed relPath
// (relative to dir), newest first.
func FileHistory(ctx context.Context, dir, relPath string) ([]string, error) {
	out, err := runGit(ctx, dir, "log", "--format=%H", "--", relPath)
	if err != nil {
		return nil, fmt.Errorf("gitutil: history of %s: %w", relPath, err)
	}
	var shas []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			shas = append(shas, line)
		}
	}
	return shas, nil
}

// IsShallow reports whether dir's repository is a shallow clone, whose
// history stops at grafted commits that look parentless.
func IsShallow(ctx context.Context, dir string) bool {
	out, err := runGit(ctx, dir, "rev-parse", "--is-shallow-repository")
	return err == nil && strings.TrimSpace(out) == "true"
}

// Parents returns each commit's parents (none for a root commit), read in one
// git call. The parents are the commits' own, not the ones a path-limited
// log rewrites them to.
func Parents(ctx context.Context, dir string, shas []string) (map[string][]string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-list", "--no-walk", "--parents", "--stdin")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(strings.Join(shas, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gitutil: parents: %w", err)
	}
	parents := make(map[string][]string, len(shas))
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Fields(line); len(f) > 0 {
			parents[f[0]] = f[1:]
		}
	}
	return parents, nil
}

// FileAtRevs reads relPath (relative to dir) at each of revs in one git call.
// A rev where the file doesn't exist is absent from the result; a rev that
// doesn't exist is an error, not an absent file.
func FileAtRevs(ctx context.Context, dir string, revs []string, relPath string) (map[string][]byte, error) {
	prefix, err := runGit(ctx, dir, "rev-parse", "--show-prefix")
	if err != nil {
		return nil, fmt.Errorf("gitutil: not a git repository: %w", err)
	}
	path := strings.TrimSpace(prefix) + filepath.ToSlash(relPath)
	var in strings.Builder
	for _, rev := range revs {
		// A commit check first, so a missing file and a missing commit
		// don't both read as "missing".
		in.WriteString(rev + "^{commit}\n" + rev + ":" + path + "\n")
	}
	cmd := exec.CommandContext(ctx, "git", "cat-file", "--batch")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(in.String())
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gitutil: reading %s: %w", path, err)
	}
	files := make(map[string][]byte, len(revs))
	rest := out
	// next reads one object: its type, or "" when it's missing.
	next := func() (typ string, body []byte, err error) {
		nl := bytes.IndexByte(rest, '\n')
		if nl < 0 {
			return "", nil, fmt.Errorf("gitutil: truncated cat-file output")
		}
		header := string(rest[:nl])
		rest = rest[nl+1:]
		if strings.HasSuffix(header, " missing") || strings.HasSuffix(header, " ambiguous") {
			return "", nil, nil
		}
		f := strings.Fields(header)
		if len(f) != 3 {
			return "", nil, fmt.Errorf("gitutil: unexpected cat-file header %q", header)
		}
		size, err := strconv.Atoi(f[2])
		if err != nil || size+1 > len(rest) {
			return "", nil, fmt.Errorf("gitutil: bad cat-file size in %q", header)
		}
		body, rest = rest[:size], rest[size+1:]
		return f[1], body, nil
	}
	for _, rev := range revs {
		typ, _, err := next()
		if err != nil {
			return nil, err
		}
		if typ != "commit" {
			return nil, fmt.Errorf("gitutil: no commit %s", rev)
		}
		typ, body, err := next()
		if err != nil {
			return nil, err
		}
		// Only a file is a file there: a directory by that name isn't, and
		// an empty file is still one (its content is for the caller to judge).
		if typ == "blob" {
			files[rev] = body
		}
	}
	return files, nil
}
