package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type stackEdgeFixture struct {
	checkout, seed string
	server         *gitServer
	base, track    map[string]string
}

func newStackEdgeFixture(t *testing.T) stackEdgeFixture {
	t.Helper()
	t.Setenv("RIG_STACK_CACHE", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	f := stackEdgeFixture{checkout: t.TempDir(), base: map[string]string{}, track: map[string]string{}}
	f.server = newGitServer(t, t.TempDir())
	for _, name := range []string{"lib", "app"} {
		f.server.seed(t, "acme/"+name, name)
		fork := f.server.bare(t, "you/"+name)
		f.base[name] = strings.TrimSpace(mustGitStack(t, f.server.path("acme/"+name), "rev-parse", "main"))
		work := t.TempDir()
		mustGitStack(t, work, "clone", "-q", f.server.path("acme/"+name), work)
		mustGitStack(t, work, "config", "user.name", "test")
		mustGitStack(t, work, "config", "user.email", "t@t")
		if err := os.WriteFile(filepath.Join(work, "src", name+".txt"), []byte("carried fix\n"), 0600); err != nil {
			t.Fatal(err)
		}
		mustGitStack(t, work, "add", ".")
		mustGitStack(t, work, "commit", "-qm", "integration snapshot")
		f.track[name] = strings.TrimSpace(mustGitStack(t, work, "rev-parse", "HEAD"))
		mustGitStack(t, work, "push", "-q", fork, "HEAD:refs/heads/stack/integration")
	}
	writeStackManifest(t, f.checkout, fmt.Sprintf(`{
		"repos": {
			"lib": {"upstream": %q, "fork": %q, "trackBranch": "stack/integration", "publishPrefix": "You."},
			"app": {"upstream": %q, "fork": %q, "trackBranch": "stack/integration"}
		},
		"edge": {"lib": {"feed": "https://feed.invalid/index.json"}},
		"lastSync": {"lib": %q, "app": %q},
		"lastPropose": {"lib": "old-proposal", "app": "stack/integration"}
	}`, f.server.spec("acme/lib"), f.server.spec("you/lib"), f.server.spec("acme/app"), f.server.spec("you/app"), f.base["lib"], f.base["app"]))
	mustGitStack(t, f.checkout, "init", "-q")
	mustGitStack(t, f.checkout, "config", "user.name", "test")
	mustGitStack(t, f.checkout, "config", "user.email", "t@t")
	mustGitStack(t, f.checkout, "add", ".")
	mustGitStack(t, f.checkout, "commit", "-qm", "manifest")
	f.seed = filepath.Join(t.TempDir(), "seed.git")
	mustGitStack(t, f.checkout, "clone", "--bare", "-q", f.checkout, f.seed)
	return f
}

func requireStackEdgeEngine(t *testing.T) {
	t.Helper()
	if os.Getenv("RIG_STACK_E2E") == "" {
		t.Skip("set RIG_STACK_E2E=1")
	}
	bin, err := stackJoshProxyBin(stackJoshVersion)
	if err != nil || stackJoshInstalled(bin) != nil {
		t.Fatal("install pinned josh before running E2E")
	}
}

func (f stackEdgeFixture) options(seed bool) stackEdgeReconstructOptions {
	o := stackEdgeReconstructOptions{Checkout: f.checkout, Manifest: "rig.stack.jsonc", Members: []string{"lib"}, AllowedHosts: f.server.addr}
	if seed {
		o.Manifest, o.Seed = "", f.seed
	}
	return o
}

func assertStackEdgeFile(t *testing.T, root, path, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil || strings.ReplaceAll(string(data), "\r\n", "\n") != strings.ReplaceAll(want, "\r\n", "\n") {
		t.Fatalf("%s = %q, want %q: %v", path, data, want, err)
	}
}

func TestStackEdgeReconstructTrackAndCursor(t *testing.T) {
	requireStackEdgeEngine(t)
	for _, seed := range []bool{false, true} {
		t.Run(fmt.Sprintf("seed-%t", seed), func(t *testing.T) {
			f := newStackEdgeFixture(t)
			// Both upstreams advance; default edge builds the old integration
			// snapshot, and the sibling must stay at its recorded cursor.
			for _, name := range []string{"lib", "app"} {
				f.server.commit(t, "acme/"+name, "src/new.txt", "upstream tip\n", "advance")
			}
			if err := os.MkdirAll(filepath.Join(f.checkout, "lib", "src"), 0700); err != nil {
				t.Fatal(err)
			}
			local := filepath.Join(f.checkout, "lib", "src", "lib.txt")
			if err := os.WriteFile(local, []byte("committed local content\n"), 0600); err != nil {
				t.Fatal(err)
			}
			mustGitStack(t, f.checkout, "add", ".")
			mustGitStack(t, f.checkout, "commit", "-qm", "local integration differs from fork")
			if err := os.WriteFile(local, []byte("dirty local content\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(f.checkout, "rig.stack.jsonc"), []byte("invalid working manifest"), 0600); err != nil {
				t.Fatal(err)
			}
			remotes := []string{f.seed, f.server.path("acme/lib"), f.server.path("you/lib"), f.server.path("acme/app"), f.server.path("you/app")}
			before := map[string]string{}
			for _, remote := range remotes {
				before[remote] = mustGitStack(t, remote, "for-each-ref")
			}
			result, cleanup, err := stackEdgeReconstruct(t.Context(), f.options(seed))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			canonical, err := filepath.EvalSymlinks(result.Root)
			if err != nil || result.Root != canonical {
				t.Fatalf("reconstruction root must be canonical for NuGet: %s, %v", result.Root, err)
			}
			assertStackEdgeFile(t, result.Root, "lib/src/lib.txt", "carried fix\n")
			assertStackEdgeFile(t, result.Root, "app/src/app.txt", "app v1\n")
			for _, name := range []string{"app", "lib"} {
				if _, err := os.Stat(filepath.Join(result.Root, name, "src", "new.txt")); !os.IsNotExist(err) {
					t.Fatalf("%s advanced to tip: %v", name, err)
				}
			}
			want := stackEdgeProvenance{TrackCommit: f.track["lib"], TrackParent: f.base["lib"], UpstreamCommit: f.base["lib"]}
			if got := result.Provenance["lib"]; got != want {
				t.Fatalf("edge provenance = %+v, want %+v", got, want)
			}
			if got := result.Provenance["app"]; got != (stackEdgeProvenance{UpstreamCommit: f.base["app"]}) {
				t.Fatalf("sibling provenance = %+v", got)
			}
			m, _, err := loadStackManifest(result.Root)
			if err != nil || m.Repos["lib"].TrackBranch != "stack/integration" || m.LastPropose["lib"] != "old-proposal" || m.Repos["app"].TrackBranch != "" || m.LastPropose["app"] != "" {
				t.Fatalf("fork-selection fields were not preserved only for the edge: %+v, %v", m, err)
			}
			if got := mustGitStack(t, result.Root, "remote"); got != "" {
				t.Fatalf("fused repository has remotes: %s", got)
			}
			if _, err := stackCIGit(t.Context(), result.Root, "push", f.server.path("you/lib"), "HEAD:refs/heads/should-not-exist"); err == nil || !strings.Contains(err.Error(), "Fused history must never be pushed") {
				t.Fatalf("push guard: %v", err)
			}
			for _, remote := range remotes {
				if got := mustGitStack(t, remote, "for-each-ref"); got != before[remote] {
					t.Fatalf("reconstruction changed remote %s", remote)
				}
			}
			assertStackEdgeFile(t, f.checkout, "lib/src/lib.txt", "dirty local content\n")
			assertStackEdgeFile(t, f.checkout, "rig.stack.jsonc", "invalid working manifest")
			cleanup()
			if _, err := os.Stat(filepath.Dir(result.Root)); !os.IsNotExist(err) {
				t.Fatalf("cleanup left temp repository: %v", err)
			}
		})
	}
}

func TestStackEdgeReconstructRefusals(t *testing.T) {
	for _, failure := range []string{"no-track", "missing-track", "unknown-member", "no-members", "blocked-host", "no-cursor"} {
		t.Run(failure, func(t *testing.T) {
			f := newStackEdgeFixture(t)
			opts := f.options(false)
			want := ""
			switch failure {
			case "no-track", "no-cursor":
				m, _, err := loadStackManifest(f.checkout)
				if err != nil {
					t.Fatal(err)
				}
				if failure == "no-track" {
					m.Repos["lib"].TrackBranch = ""
					want = "needs a trackBranch"
				} else {
					delete(m.LastSync, "app")
					want = "no full manifest cursor"
				}
				if err := stackReconstructWrite(f.checkout, m); err != nil {
					t.Fatal(err)
				}
				mustGitStack(t, f.checkout, "add", ".")
				mustGitStack(t, f.checkout, "commit", "-qm", "invalid reconstruction source")
			case "missing-track":
				mustGitStack(t, f.server.path("you/lib"), "update-ref", "-d", "refs/heads/stack/integration")
				want = `trackBranch "stack/integration" is not on`
			case "unknown-member":
				opts.Members = []string{"absent"}
				want = "unknown edge member"
			case "no-members":
				opts.Members = nil
				want = "at least one edge member"
			case "blocked-host":
				opts.AllowedHosts = "github.com"
				want = "not in --allowed-hosts"
			}
			temp := t.TempDir()
			for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
				t.Setenv(key, temp)
			}
			result, cleanup, err := stackEdgeReconstruct(t.Context(), opts)
			defer cleanup()
			if err == nil || !strings.Contains(err.Error(), want) || result != nil {
				t.Fatalf("want refusal %q without repository, got %+v, %v", want, result, err)
			}
			if entries, err := os.ReadDir(temp); err != nil || len(entries) != 0 {
				t.Fatalf("refusal left reconstruction files: %v, %v", entries, err)
			}
		})
	}
}

func TestStackEdgeReconstructMultipleMembers(t *testing.T) {
	requireStackEdgeEngine(t)
	f := newStackEdgeFixture(t)
	m, _, err := loadStackManifest(f.checkout)
	if err != nil {
		t.Fatal(err)
	}
	m.Repos["app"].PublishPrefix = "App."
	m.Edge["app"] = &stackEdge{Feed: "https://feed.invalid/index.json"}
	if err := stackReconstructWrite(f.checkout, m); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, f.checkout, "add", ".")
	mustGitStack(t, f.checkout, "commit", "-qm", "configure second edge member")
	mustGitStack(t, f.checkout, "push", "-q", f.seed, "HEAD")
	opts := f.options(true)
	opts.Members = []string{"lib", "app"}
	result, cleanup, err := stackEdgeReconstruct(t.Context(), opts)
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range opts.Members {
		assertStackEdgeFile(t, result.Root, name+"/src/"+name+".txt", "carried fix\n")
		want := stackEdgeProvenance{TrackCommit: f.track[name], TrackParent: f.base[name], UpstreamCommit: f.base[name]}
		if got := result.Provenance[name]; got != want {
			t.Fatalf("%s provenance = %+v, want %+v", name, got, want)
		}
	}
}

func TestStackEdgeReconstructTip(t *testing.T) {
	requireStackEdgeEngine(t)
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprintf("conflict-%t", conflict), func(t *testing.T) {
			f := newStackEdgeFixture(t)
			path := "src/new.txt"
			if conflict {
				path = "src/lib.txt"
			}
			f.server.commit(t, "acme/lib", path, "upstream tip\n", "upstream advances")
			f.server.commit(t, "acme/app", "src/new.txt", "sibling tip\n", "sibling advances")
			tip := strings.TrimSpace(mustGitStack(t, f.server.path("acme/lib"), "rev-parse", "main"))
			opts := f.options(false)
			opts.UpstreamTip = true
			result, cleanup, err := stackEdgeReconstruct(t.Context(), opts)
			defer cleanup()
			if conflict {
				var inconclusive *stackEdgeInconclusiveError
				if !errors.As(err, &inconclusive) || result != nil || !strings.Contains(err.Error(), "pull, resolve on the integration line, `rig stack propose lib --track-only`") {
					t.Fatalf("conflict must be inconclusive with remedy, got %+v, %v", result, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			assertStackEdgeFile(t, result.Root, "lib/src/lib.txt", "carried fix\n")
			assertStackEdgeFile(t, result.Root, "lib/src/new.txt", "upstream tip\n")
			assertStackEdgeFile(t, result.Root, "app/src/app.txt", "app v1\n")
			if _, err := os.Stat(filepath.Join(result.Root, "app", "src", "new.txt")); !os.IsNotExist(err) {
				t.Fatalf("tip mode advanced sibling: %v", err)
			}
			want := stackEdgeProvenance{TrackCommit: f.track["lib"], TrackParent: f.base["lib"], UpstreamCommit: tip, UpstreamTip: tip}
			if got := result.Provenance["lib"]; got != want {
				t.Fatalf("tip provenance = %+v, want %+v", got, want)
			}
			m, _, err := loadStackManifest(result.Root)
			if err != nil {
				t.Fatal(err)
			}
			if m.cursor("lib") != tip || m.cursor("app") != f.base["app"] {
				t.Fatalf("tip manifest cursors: lib=%s, app=%s", m.cursor("lib"), m.cursor("app"))
			}
			if got := mustGitStack(t, result.Root, "status", "--porcelain"); got != "" {
				t.Fatalf("tip reconstruction left uncommitted changes: %s", got)
			}
		})
	}
}

func TestStackReconstructCIClearsTrackBranch(t *testing.T) {
	for _, tip := range []bool{false, true} {
		m := &stackManifest{Repos: map[string]*stackRepo{
			"app": {TrackBranch: "stack/integration", UpstreamCommit: strings.Repeat("a", 40)},
			"lib": {TrackBranch: "stack/integration", UpstreamBranch: "develop"},
		}, LastSync: map[string]string{"app": strings.Repeat("a", 40), "lib": strings.Repeat("b", 40)}, LastPropose: map[string]string{"app": "proposal", "lib": "proposal"}}
		if err := stackReconstructCursors(m, "app", tip, nil); err != nil {
			t.Fatal(err)
		}
		if m.Repos["app"].TrackBranch != "" || m.Repos["lib"].TrackBranch != "" || m.LastPropose != nil {
			t.Fatal("CI retained fork selection")
		}
		if tip {
			if m.Repos["app"].UpstreamCommit != "" || len(m.LastSync) != 0 {
				t.Fatal("CI tip mode retained cursors")
			}
		} else if m.Repos["lib"].UpstreamCommit != strings.Repeat("b", 40) || m.Repos["lib"].UpstreamBranch != "" {
			t.Fatal("CI sibling was not normalized to cursor")
		}
	}
}

func TestStackEdgeReconstructCommittedManifestRequired(t *testing.T) {
	root := t.TempDir()
	mustGitStack(t, root, "init", "-q")
	writeStackManifest(t, root, `{"repos":{"lib":{"upstream":"github.com/acme/lib","fork":"github.com/you/lib","trackBranch":"integration"}}}`)
	result, cleanup, err := stackEdgeReconstruct(context.Background(), stackEdgeReconstructOptions{Checkout: root, Manifest: "rig.stack.jsonc", Members: []string{"lib"}, AllowedHosts: "github.com"})
	cleanup()
	if err == nil || !strings.Contains(err.Error(), "manifest must be committed") || result != nil {
		t.Fatalf("uncommitted manifest accepted: %+v, %v", result, err)
	}
}
