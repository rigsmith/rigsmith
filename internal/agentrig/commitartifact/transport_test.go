package commitartifact

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestGitTransportPublishesAndConfirms(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			req, remote, local, parent := publicationFixture(t, format)
			options := GitTransportOptions{Remote: remote.repo.dir, Branch: "main"}
			tr, err := NewGitTransport(options)
			if err != nil {
				t.Fatal(err)
			}
			if destination, branch := tr.Destination(); destination != options.Remote || branch != "main" {
				t.Fatal("destination changed")
			}
			if err := remote.repo.importRef(t.Context(), local.dir, parent, "refs/heads/main", parent); err != nil {
				t.Fatal(err)
			}
			remoteHead := newPublicationCommit(t, remote.repo, parent, "remote.txt", "newer remote")
			mustRun(t, remote.repo, "", "update-ref", "refs/heads/main", remoteHead)
			localHead := newPublicationCommit(t, local, parent, "local.txt", "newer local")
			req.LocalDir, req.LocalCommit, req.Remote = local.dir, localHead, tr
			result, err := Publish(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			for _, sha := range []string{result.CaptureCommit, localHead, remoteHead} {
				if ok, err := remote.repo.ancestor(t.Context(), sha, result.RemoteCommit); err != nil || !ok {
					t.Fatalf("lost history: %s %v", sha, err)
				}
			}
			again, err := Publish(t.Context(), req)
			if err != nil || again != result {
				t.Fatalf("replay: %+v %v", again, err)
			}
		})
	}
}

func TestGitTransportAbsenceErrorsAndRefIsolation(t *testing.T) {
	req, remote, local, parent := publicationFixture(t, "sha1")
	tr, err := NewGitTransport(GitTransportOptions{Remote: remote.repo.dir, Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	private, err := initRepo(t.Context(), filepath.Join(t.TempDir(), "private"), parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := private.importRef(t.Context(), local.dir, parent, RefName, parent); err != nil {
		t.Fatal(err)
	}
	mustRun(t, private, "", "update-ref", remoteRefName, parent)
	before, err := os.ReadFile(filepath.Join(private.dir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	sha, err := tr.Fetch(t.Context(), private.dir, remoteRefName)
	if err != nil || sha != "" {
		t.Fatalf("absent: %s %v", sha, err)
	}
	if refs := mustRun(t, private, "", "for-each-ref", "--format=%(refname)", remoteRefName); refs != "" {
		t.Fatal("stale remote ref retained")
	}
	if kept := mustRun(t, private, "", "rev-parse", RefName); kept != parent {
		t.Fatal("capture ref changed")
	}
	after, _ := os.ReadFile(filepath.Join(private.dir, "config"))
	if string(before) != string(after) {
		t.Fatal("private config changed")
	}
	if _, err := os.Stat(filepath.Join(private.dir, "FETCH_HEAD")); !os.IsNotExist(err) {
		t.Fatal("FETCH_HEAD written")
	}
	if _, err := tr.Fetch(t.Context(), private.dir, RefName); !errors.Is(err, ErrInvalid) {
		t.Fatal("protected ref accepted", err)
	}
	if err := tr.Push(t.Context(), remote.repo.dir, parent); !errors.Is(err, ErrInvalid) {
		t.Fatal("remote repository accepted as workspace", err)
	}
	// A successful initial push, then a stale/non-fast-forward push must not rewind.
	req.Remote = tr
	result, err := Publish(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Push(t.Context(), private.dir, parent); !errors.Is(err, ErrTransport) {
		t.Fatal("rewind accepted", err)
	}
	if got := mustRun(t, remote.repo, "", "rev-parse", "refs/heads/main"); got != result.RemoteCommit {
		t.Fatal("remote rewound")
	}
	if err := os.RemoveAll(remote.repo.dir); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Fetch(t.Context(), private.dir, remoteRefName); !errors.Is(err, ErrTransport) {
		t.Fatal("missing repository treated as absent branch", err)
	}
}

func TestGitTransportRejectsUnsafeOptions(t *testing.T) {
	for _, remote := range []string{"", "origin", "../repo", "ssh://git@example.com/repo", "git@example.com:repo", "http://example.com/repo", "https://example.com/repo", "http://127.0.0.1/repo", "file:///repo", "https://user:secret@example.com/repo", "https://example.com/repo?token=secret", "https://example.com/repo#fragment", "https://example.com/repo?", "https://example.com/repo#", "ext::sh command", "https://example.com/\nrepo"} {
		if _, err := NewGitTransport(GitTransportOptions{Remote: remote, Branch: "main"}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted remote %q: %v", remote, err)
		}
	}
	for _, branch := range []string{"", "-main", "main:other", "main..other", "x.lock", "x//y", "x/../y", "x@{y}", "main*"} {
		if _, err := NewGitTransport(GitTransportOptions{Remote: t.TempDir(), Branch: branch}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted branch %q: %v", branch, err)
		}
	}
}
