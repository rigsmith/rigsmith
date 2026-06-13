package gitutil

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// Commit is a single commit read from the log, with the files it touched. It is
// the structured input commit-based versioning synthesizes changesets from
// (see core/commitsource).
type Commit struct {
	// Hash is the full commit SHA.
	Hash string
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

var logFormat = strings.Join([]string{"%H", "%s", "%b"}, logFieldSep)

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

	args := []string{"log", "--name-only", "--no-renames", "--pretty=format:" + logRecordSep + logFormat + logFieldSep}
	if strings.TrimSpace(ref) != "" {
		args = append(args, ref+"..HEAD")
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
		// rec = hash <FS> subject <FS> body <FS> \n file1 \n file2 …
		fields := strings.SplitN(rec, logFieldSep, 4)
		if len(fields) < 4 {
			continue
		}
		c := Commit{
			Hash:    strings.TrimSpace(fields[0]),
			Subject: strings.TrimSpace(fields[1]),
			Body:    strings.TrimSpace(fields[2]),
		}
		for _, line := range strings.Split(fields[3], "\n") {
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
