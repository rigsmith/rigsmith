package commitartifact

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func httpTransportFixture(t *testing.T, remote gitRepo) (GitTransportOptions, *atomic.Int32) {
	t.Helper()
	executable, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	backend := &cgi.Handler{Path: executable, Args: []string{"http-backend"}, Dir: filepath.Dir(remote.dir), Env: []string{"GIT_PROJECT_ROOT=" + filepath.Dir(remote.dir), "GIT_HTTP_EXPORT_ALL=1", "REMOTE_USER=fixture"}}
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		user, password, ok := r.BasicAuth()
		if !ok || user != "fixture" || password != "synthetic transport password" {
			w.Header().Set("WWW-Authenticate", `Basic realm="fixture"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		backend.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	return GitTransportOptions{Remote: server.URL + "/" + filepath.Base(remote.dir), Branch: "main", Credential: &HTTPCredential{Username: "fixture", Password: "synthetic transport password"}, CAFile: ca}, &requests
}

func TestGitTransportPublishesAndConfirms(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		for _, protocol := range []string{"file", "https"} {
			t.Run(format+"/"+protocol, func(t *testing.T) {
				req, remote, local, parent := publicationFixture(t, format)
				options := GitTransportOptions{Remote: remote.repo.dir, Branch: "main"}
				if protocol == "https" {
					options, _ = httpTransportFixture(t, remote.repo)
				}
				tr, err := NewGitTransport(options)
				if err != nil {
					t.Fatal(err)
				}
				if destination, branch := tr.Destination(); destination != options.Remote || branch != "main" {
					t.Fatal("destination changed")
				}
				// Both remote and local have newer committed history than the retained seed.
				if err := remote.repo.importRef(t.Context(), local.dir, parent, "refs/heads/main", parent); err != nil {
					t.Fatal(err)
				}
				remoteHead := newPublicationCommit(t, remote.repo, parent, "remote.txt", "newer remote")
				mustRun(t, remote.repo, "", "update-ref", "refs/heads/main", remoteHead)
				localHead := newPublicationCommit(t, local, parent, "local.txt", "newer local")
				req.LocalDir, req.LocalCommit = local.dir, localHead
				req.Remote = tr
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

func TestGitTransportCredentialsAreExplicitAndIsolated(t *testing.T) {
	_, remote, _, parent := publicationFixture(t, "sha1")
	options, requests := httpTransportFixture(t, remote.repo)
	private, err := initRepo(t.Context(), filepath.Join(t.TempDir(), "private"), parent)
	if err != nil {
		t.Fatal(err)
	}
	// Host Git settings and curl's netrc must not provide credentials or redirect
	// the operation. Poisoned values would fail or authenticate an anonymous call.
	home := t.TempDir()
	putPublicationFile(t, home, ".netrc", "machine 127.0.0.1 login fixture password \"synthetic transport password\"\n")
	putPublicationFile(t, home, ".gitconfig", "[url \"https://invalid.example/\"]\n insteadOf = https://127.0.0.1\n[credential]\n helper = !exit 91\n")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "http.proxy")
	t.Setenv("GIT_CONFIG_VALUE_0", "http://127.0.0.1:1")
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "wrong"))
	t.Setenv("GIT_ASKPASS", "nonexistent-askpass")
	for _, mode := range []string{"anonymous", "wrong", "correct"} {
		request := options
		switch mode {
		case "anonymous":
			request.Credential = nil
		case "wrong":
			request.Credential = &HTTPCredential{Username: "fixture", Password: "wrong synthetic password"}
		}
		tr, err := NewGitTransport(request)
		if err != nil {
			t.Fatal(err)
		}
		if mode == "correct" {
			request.Credential.Password = "changed after construction"
		}
		sha, err := tr.Fetch(t.Context(), private.dir, remoteRefName)
		if mode == "correct" {
			if err != nil || sha != "" {
				t.Fatalf("correct credential: %s %v", sha, err)
			}
		} else if !errors.Is(err, ErrTransport) {
			t.Fatalf("%s used ambient credentials: %s %v", mode, sha, err)
		}
		if err != nil && (strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), options.Remote)) {
			t.Fatal("sensitive transport diagnostics", err)
		}
	}
	if requests.Load() == 0 {
		t.Fatal("HTTP transport not exercised")
	}
}

func TestGitTransportRejectsRedirectsAndUntrustedTLS(t *testing.T) {
	_, _, _, parent := publicationFixture(t, "sha1")
	private, err := initRepo(t.Context(), filepath.Join(t.TempDir(), "private"), parent)
	if err != nil {
		t.Fatal(err)
	}
	var leaked atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1); http.Error(w, "unexpected", 500) }))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	tr, err := NewGitTransport(GitTransportOptions{Remote: redirect.URL + "/repo", Branch: "main", Credential: &HTTPCredential{Username: "fixture", Password: "synthetic redirect password"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Fetch(t.Context(), private.dir, remoteRefName); !errors.Is(err, ErrTransport) {
		t.Fatal("redirect accepted", err)
	}
	if leaked.Load() != 0 {
		t.Fatal("redirect destination contacted")
	}
	var untrustedRequests atomic.Int32
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		untrustedRequests.Add(1)
		http.Error(w, "unexpected", 500)
	}))
	defer tlsServer.Close()
	tr, err = NewGitTransport(GitTransportOptions{Remote: tlsServer.URL + "/repo", Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Fetch(t.Context(), private.dir, remoteRefName); !errors.Is(err, ErrTransport) {
		t.Fatal("untrusted TLS accepted", err)
	}
	if untrustedRequests.Load() != 0 {
		t.Fatal("request crossed untrusted TLS connection")
	}
}

func TestGitTransportCancellation(t *testing.T) {
	_, _, _, parent := publicationFixture(t, "sha1")
	private, err := initRepo(t.Context(), filepath.Join(t.TempDir(), "private"), parent)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	tr, err := NewGitTransport(GitTransportOptions{Remote: server.URL + "/repo", Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	go func() {
		select {
		case <-entered:
			cancel()
		case <-ctx.Done():
		}
	}()
	if _, err := tr.Fetch(ctx, private.dir, remoteRefName); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestGitTransportRejectsUnsafeOptions(t *testing.T) {
	for _, remote := range []string{"", "origin", "../repo", "ssh://git@example.com/repo", "git@example.com:repo", "http://example.com/repo", "https://user:secret@example.com/repo", "https://example.com/repo?token=secret", "https://example.com/repo#fragment", "https://example.com/repo?", "https://example.com/repo#", "ext::sh command", "https://example.com/\nrepo"} {
		if _, err := NewGitTransport(GitTransportOptions{Remote: remote, Branch: "main"}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted remote %q: %v", remote, err)
		}
	}
	for _, branch := range []string{"", "-main", "main:other", "main..other", "x.lock", "x//y", "x/../y", "x@{y}", "main*"} {
		if _, err := NewGitTransport(GitTransportOptions{Remote: "https://example.com/repo", Branch: branch}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted branch %q: %v", branch, err)
		}
	}
}
