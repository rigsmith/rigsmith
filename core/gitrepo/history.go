package gitrepo

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Deletion is a path git no longer tracks, and the commit that removed it. The
// content is still reachable at Commit^:Path — a deleted file in a git repo is
// gone from the tree, not from history.
type Deletion struct {
	Path   string
	Commit string
}

// Deletions lists files removed under pathspec, newest deletion first, one entry
// per path (a path deleted, re-added and deleted again reports only its latest
// removal — that is the copy worth recovering).
//
// NUL-delimited (-z) deliberately: without it git QUOTES any path holding
// non-ASCII or special characters ("cli/projects/-Caf\303\251/s.jsonl"), and
// that display string is not a path anything can open — so a session in an
// accented project directory could never be recovered, and would be counted
// unreadable instead. Under -z the commit headers arrive glued to the front of
// the following name, which is what the inner loop peels off.
func (r *Repo) Deletions(ctx context.Context, pathspec string) ([]Deletion, error) {
	out, err := runGit(ctx, r.Dir, "log", "--all", "--diff-filter=D",
		"--pretty=format:commit %H", "--name-only", "-z", "--", pathspec)
	if err != nil {
		return nil, err
	}
	var (
		dels []Deletion
		seen = map[string]bool{}
		cur  string
	)
	for _, chunk := range strings.Split(out, "\x00") {
		for {
			i := strings.IndexByte(chunk, '\n')
			if i < 0 {
				break
			}
			if line := strings.TrimSpace(chunk[:i]); strings.HasPrefix(line, "commit ") {
				cur = strings.TrimPrefix(line, "commit ")
			}
			chunk = chunk[i+1:]
		}
		if chunk == "" || cur == "" || seen[chunk] {
			continue
		}
		seen[chunk] = true
		dels = append(dels, Deletion{Path: chunk, Commit: cur})
	}
	return dels, nil
}

// LastCommitTime is the author time of the newest commit at or before rev that
// touched path — for a deleted transcript, when it last changed, which is the
// closest thing history holds to when the session ended.
func (r *Repo) LastCommitTime(ctx context.Context, rev, path string) (time.Time, error) {
	out, err := runGit(ctx, r.Dir, "log", "-1", "--format=%ct", rev, "--", path)
	if err != nil {
		return time.Time{}, err
	}
	s := strings.TrimSpace(out)
	if s == "" {
		return time.Time{}, nil
	}
	secs, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("gitrepo: unreadable commit time %q: %w", s, err)
	}
	return time.Unix(secs, 0).UTC(), nil
}

// ShowPrefix reads at most max bytes of the blob at rev:path.
//
// Streamed and cut short deliberately: these are transcripts, and reading whole
// ones to find a title in their first few lines would pull tens of megabytes
// through memory per file. The early close makes git's own write fail, which is
// expected and not an error here.
func (r *Repo) ShowPrefix(ctx context.Context, rev, path string, max int) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", "show", rev+":"+path)
	cmd.Dir = r.Dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	buf := make([]byte, max)
	n, rerr := io.ReadFull(bufio.NewReader(stdout), buf)
	if rerr != nil && rerr != io.ErrUnexpectedEOF && rerr != io.EOF {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, rerr
	}
	// Nothing more is wanted; killing beats draining a multi-MB blob.
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if n == 0 && stderr.Len() > 0 {
		return nil, fmt.Errorf("git show %s:%s: %s", rev, path, strings.TrimSpace(stderr.String()))
	}
	return buf[:n], nil
}
