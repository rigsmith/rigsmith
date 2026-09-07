package commitartifact

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Everything gh can read is synthetic. The stored token is present so gh never
// falls back to an OS keychain. No command contacts GitHub or a real account.
func configuredGitHome(t *testing.T) string {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(key)
		if strings.HasPrefix(upper, "GIT_") || strings.HasPrefix(upper, "GH_") || strings.HasPrefix(upper, "SSH_") || upper == "GITHUB_TOKEN" || upper == "GITHUB_ENTERPRISE_TOKEN" {
			t.Setenv(key, "")
		}
	}
	home := t.TempDir()
	for key, value := range map[string]string{"HOME": home, "USERPROFILE": home, "XDG_CONFIG_HOME": home, "GH_CONFIG_DIR": filepath.Join(home, "gh"), "GH_PROMPT_DISABLED": "1", "GH_NO_UPDATE_NOTIFIER": "1", "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_SYSTEM": os.DevNull, "GIT_CONFIG_GLOBAL": filepath.Join(home, "gitconfig"), "HTTP_PROXY": "", "HTTPS_PROXY": "", "ALL_PROXY": "", "NO_PROXY": "*"} {
		t.Setenv(key, value)
	}
	if err := os.MkdirAll(os.Getenv("GH_CONFIG_DIR"), 0700); err != nil {
		t.Fatal(err)
	}
	return home
}

func configuredGit(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture git: %v\n%s", err, out)
	}
	return string(out)
}

func ghPublicationFixture(t *testing.T, remote gitRepo) (GitTransportOptions, *atomic.Int32, string) {
	t.Helper()
	configuredGitHome(t)
	if _, err := exec.LookPath("gh"); err != nil {
		t.Fatal("real gh is required for the configured Git integration fixture", err)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	backend := &cgi.Handler{Path: git, Args: []string{"http-backend"}, Dir: filepath.Dir(remote.dir), Env: []string{"GIT_PROJECT_ROOT=" + filepath.Dir(remote.dir), "GIT_HTTP_EXPORT_ALL=1", "REMOTE_USER=fixture"}}
	var authenticated atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || (user != "fixture" && user != "x-access-token") || password != "synthetic-gh-publication-token" {
			w.Header().Set("WWW-Authenticate", `Basic realm="fixture"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		authenticated.Add(1)
		backend.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	u, _ := url.Parse(server.URL)
	hosts := filepath.Join(os.Getenv("GH_CONFIG_DIR"), "hosts.yml")
	data := "\"" + u.Host + "\":\n    user: fixture\n    oauth_token: synthetic-gh-publication-token\n    git_protocol: https\n"
	if err := os.WriteFile(hosts, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	setup := exec.CommandContext(t.Context(), "gh", "auth", "setup-git", "--hostname", u.Host)
	setup.Dir = t.TempDir()
	if out, err := setup.CombinedOutput(); err != nil {
		t.Fatalf("fixture gh setup-git: %v\n%s", err, out)
	}
	ca := filepath.Join(t.TempDir(), "fixture.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	configuredGit(t, "config", "--global", "http."+server.URL+".sslCAInfo", filepath.ToSlash(ca))
	configuredGit(t, "config", "--global", "http.schannelUseSSLCAInfo", "true")
	return GitTransportOptions{Remote: server.URL + "/" + filepath.Base(remote.dir), Branch: "main"}, &authenticated, hosts
}

func TestConfiguredGitPublishesUsingGH(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			req, remote, local, parent := publicationFixture(t, format)
			options, authenticated, hosts := ghPublicationFixture(t, remote.repo)
			// An explicit bound push URL must override the user's push-only rewrite.
			configuredGit(t, "config", "--global", "url.https://invalid.example/.pushInsteadOf", options.Remote)
			beforeHosts, err := os.ReadFile(hosts)
			if err != nil {
				t.Fatal(err)
			}
			beforeConfig, err := os.ReadFile(os.Getenv("GIT_CONFIG_GLOBAL"))
			if err != nil {
				t.Fatal(err)
			}
			tr, err := NewConfiguredGitTransport(options)
			if err != nil {
				t.Fatal(err)
			}
			if dest, branch := tr.Destination(); dest != options.Remote || branch != "main" {
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
			for _, sha := range []string{result.CaptureCommit, remoteHead, localHead} {
				if ok, err := remote.repo.ancestor(t.Context(), sha, result.RemoteCommit); err != nil || !ok {
					t.Fatal("lost history", err)
				}
			}
			again, err := Publish(t.Context(), req)
			if err != nil || again != result {
				t.Fatal("replay changed publication", err)
			}
			if authenticated.Load() == 0 {
				t.Fatal("Git did not authenticate through gh")
			}
			afterHosts, _ := os.ReadFile(hosts)
			afterConfig, _ := os.ReadFile(os.Getenv("GIT_CONFIG_GLOBAL"))
			if string(beforeHosts) != string(afterHosts) || string(beforeConfig) != string(afterConfig) {
				t.Fatal("publication changed login or Git configuration")
			}
		})
	}
}

func TestConfiguredGitRefusesAuthFailureAndRewrites(t *testing.T) {
	_, remote, _, parent := publicationFixture(t, "sha1")
	options, authenticated, hosts := ghPublicationFixture(t, remote.repo)
	private, err := initRepo(t.Context(), filepath.Join(t.TempDir(), "private"), parent)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(private.dir, "config"))
	tr, err := NewConfiguredGitTransport(options)
	if err != nil {
		t.Fatal(err)
	}
	if sha, err := tr.Fetch(t.Context(), private.dir, remoteRefName); err != nil || sha != "" {
		t.Fatal("authenticated empty remote", err)
	}
	// Environment credentials keep gh's normal precedence over a stored login.
	t.Setenv("GH_ENTERPRISE_TOKEN", "wrong-synthetic-token")
	if _, err := tr.Fetch(t.Context(), private.dir, remoteRefName); !errors.Is(err, ErrTransport) {
		t.Fatal("wrong credential treated as absent", err)
	}
	t.Setenv("GH_ENTERPRISE_TOKEN", "")
	// A different nonempty stored token also fails without touching a keychain.
	data, _ := os.ReadFile(hosts)
	if err := os.WriteFile(hosts, []byte(strings.ReplaceAll(string(data), "synthetic-gh-publication-token", "wrong-stored-token")), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Fetch(t.Context(), private.dir, remoteRefName); !errors.Is(err, ErrTransport) {
		t.Fatal("stored auth failure treated as absent", err)
	}
	if err := os.WriteFile(hosts, data, 0600); err != nil {
		t.Fatal(err)
	}
	previous := authenticated.Load()
	configuredGit(t, "config", "--global", "url.https://invalid.example/.insteadOf", options.Remote)
	if _, err := tr.Fetch(t.Context(), private.dir, remoteRefName); !errors.Is(err, ErrInvalid) {
		t.Fatal("URL rewrite accepted", err)
	}
	if authenticated.Load() != previous {
		t.Fatal("rewrite contacted original remote")
	}
	configuredGit(t, "config", "--global", "--unset-all", "url.https://invalid.example/.insteadOf")
	configuredGit(t, "config", "--global", "remote."+configuredRemote+".pushurl", "https://invalid.example/")
	if err := tr.checkRepo(t.Context(), private.dir); !errors.Is(err, ErrInvalid) {
		t.Fatal("additional push URL accepted", err)
	}
	configuredGit(t, "config", "--global", "--unset-all", "remote."+configuredRemote+".pushurl")
	u, _ := url.Parse(options.Remote)
	configuredGit(t, "config", "--global", "--unset-all", "credential.https://"+u.Host+".helper")
	if _, err := tr.Fetch(t.Context(), private.dir, remoteRefName); !errors.Is(err, ErrTransport) {
		t.Fatal("missing helper treated as absent", err)
	}
	after, _ := os.ReadFile(filepath.Join(private.dir, "config"))
	if string(before) != string(after) {
		t.Fatal("private configuration changed")
	}
}

func TestConfiguredGitIgnoresRepositoryOverrides(t *testing.T) {
	_, remote, local, parent := publicationFixture(t, "sha1")
	configuredGitHome(t)
	private, err := initRepo(t.Context(), filepath.Join(t.TempDir(), "private"), parent)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := NewConfiguredGitTransport(GitTransportOptions{Remote: remote.repo.dir, Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	configuredGit(t, "config", "--global", "remote."+configuredRemote+".fetch", "+refs/heads/*:refs/surprise/*")
	if err := remote.repo.importRef(t.Context(), local.dir, parent, "refs/heads/main", parent); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"GIT_DIR", "GIT_COMMON_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE", "GIT_SHALLOW_FILE"} {
		t.Setenv(key, filepath.Join(t.TempDir(), "wrong"))
	}
	if sha, err := tr.Fetch(t.Context(), private.dir, remoteRefName); err != nil || sha != parent {
		t.Fatal("repository override escaped workspace", err)
	}
	if refs := mustRun(t, private, "", "for-each-ref", "--format=%(refname)", "refs/surprise/"); refs != "" {
		t.Fatal("configured tracking refs changed", refs)
	}
}

func TestConfiguredGitRejectsInvalidOptions(t *testing.T) {
	for _, remote := range []string{"origin", "../repo", "http://example.com/repo", "ssh://git@example.com/repo", "ext::command", "https://u:secret@example.com/repo", "https://example.com/repo?token=x", "https://example.com/repo#", "https://example.com/\nrepo"} {
		if _, err := NewConfiguredGitTransport(GitTransportOptions{Remote: remote, Branch: "main"}); !errors.Is(err, ErrInvalid) {
			t.Fatal("accepted destination", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	tr, err := NewConfiguredGitTransport(GitTransportOptions{Remote: t.TempDir(), Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := tr.Fetch(ctx, t.TempDir(), remoteRefName); !errors.Is(err, context.Canceled) {
		t.Fatal("lost cancellation", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancelled operation blocked")
	}
}
