package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

const edgeCIKey = "private-edge-key-do-not-print"

type edgeCITestFeed struct {
	*httptest.Server
	mu                     sync.Mutex
	versions               []string
	pushStatus, readStatus int
	requests               []string
	pushes                 [][]byte
}

func newEdgeCITestFeed(t *testing.T) *edgeCITestFeed {
	t.Helper()
	f := &edgeCITestFeed{pushStatus: http.StatusCreated}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/public/") {
			if r.Header.Get("X-NuGet-ApiKey") != "" {
				t.Error("push credential sent to public release lookup")
			}
			fmt.Fprint(w, `{"versions":["1.2.3"]}`)
			return
		}
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		if r.Header.Get("X-NuGet-ApiKey") != edgeCIKey {
			t.Error("missing credential on private feed request")
		}
		if r.Method == http.MethodGet {
			user, password, ok := r.BasicAuth()
			if !ok || user != "rig" || password != edgeCIKey {
				t.Error("missing authenticated feed read")
			}
		}
		if strings.Contains(r.URL.String(), edgeCIKey) {
			t.Error("credential in URL")
		}
		if f.readStatus != 0 && r.Method == http.MethodGet {
			w.WriteHeader(f.readStatus)
			fmt.Fprint(w, edgeCIKey)
			return
		}
		switch {
		case r.URL.Path == "/index.json" && r.Method == http.MethodGet:
			fmt.Fprintf(w, `{"resources":[{"@id":%q,"@type":"PackageBaseAddress/3.0.0"},{"@id":%q,"@type":"PackagePublish/2.0.0"}]}`, f.URL+"/flat", f.URL+"/push")
		case strings.HasPrefix(r.URL.Path, "/flat/") && r.Method == http.MethodGet:
			if f.versions == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"versions": f.versions})
		case r.URL.Path == "/push" && r.Method == http.MethodPut:
			reader, err := r.MultipartReader()
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			part, err := reader.NextPart()
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			if part.FormName() != "package" || !strings.HasSuffix(part.FileName(), ".nupkg") {
				t.Error("invalid multipart upload")
			}
			b, err := io.ReadAll(part)
			if err != nil {
				t.Error(err)
			}
			f.pushes = append(f.pushes, b)
			if _, err = reader.NextPart(); err != io.EOF {
				t.Error("extra or malformed multipart part", err)
			}
			w.WriteHeader(f.pushStatus)
			fmt.Fprint(w, edgeCIKey)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

type edgeCITestFixture struct {
	o                       stackEdgeCIOptions
	r                       stackEdgeCIReport
	g                       *gitServer
	f                       *edgeCITestFeed
	checkout, track, parent string
}

func newEdgeCITestFixture(t *testing.T) *edgeCITestFixture {
	t.Helper()
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("EDGE_FEED_KEY", edgeCIKey)
	f := &edgeCITestFixture{g: newGitServer(t, t.TempDir()), f: newEdgeCITestFeed(t), checkout: t.TempDir()}
	f.g.seed(t, "acme/lib", "lib")
	f.g.commit(t, "acme/lib", "Lib.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><Version>1.2.3</Version><TargetFramework>net10.0</TargetFramework><IsPackable>true</IsPackable></PropertyGroup></Project>`, "project")
	f.parent = strings.TrimSpace(mustGitStack(t, f.g.path("acme/lib"), "rev-parse", "main"))
	f.g.bare(t, "you/lib")
	work := t.TempDir()
	mustGitStack(t, work, "clone", "-q", f.g.path("acme/lib"), work)
	mustGitStack(t, work, "config", "user.name", "test")
	mustGitStack(t, work, "config", "user.email", "test@localhost")
	if err := os.WriteFile(filepath.Join(work, "fix.txt"), []byte("carried\n"), 0600); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, work, "add", ".")
	mustGitStack(t, work, "commit", "-qm", "carried fix")
	f.track = strings.TrimSpace(mustGitStack(t, work, "rev-parse", "HEAD"))
	mustGitStack(t, work, "push", "-q", f.g.path("you/lib"), "HEAD:refs/heads/stack/integration")
	writeStackManifest(t, f.checkout, fmt.Sprintf(`{"repos":{"lib":{"upstream":%q,"fork":%q,"trackBranch":"stack/integration","publishPrefix":"You."}},"edge":{"lib":{"feed":%q,"pushKeySecret":"EDGE_FEED_KEY"}},"lastSync":{"lib":%q}}`, f.g.spec("acme/lib"), f.g.spec("you/lib"), f.f.URL+"/index.json", f.parent))
	mustGitStack(t, f.checkout, "init", "-q")
	mustGitStack(t, f.checkout, "add", ".")
	mustGitStack(t, f.checkout, "-c", "user.name=test", "-c", "user.email=test@localhost", "commit", "-qm", "seed")
	base := strings.TrimSpace(mustGitStack(t, f.checkout, "rev-parse", "HEAD"))
	seed := filepath.Join(t.TempDir(), "seed.git")
	mustGitStack(t, f.checkout, "clone", "-q", "--bare", f.checkout, seed)
	f.o = stackEdgeCIOptions{Seed: seed, Member: "lib", Number: 7, Publish: filepath.Join(t.TempDir(), "edge.json"), Out: t.TempDir(), Allowed: f.g.addr, Client: f.f.Client(), PublicFeed: f.f.URL + "/public"}
	version := "1.2.4-edge.7"
	want := stackEdgeVerifyRequest{Version: version, MaxSize: 100_000_000, MemberIDs: map[string]string{"Lib": "You.Lib"}, Packages: []stackEdgeExpectedPackage{{OriginalID: "Lib", ID: "You.Lib", AssemblyBefore: "Lib", AssemblyAfter: "Lib"}}}
	f.r = stackEdgeCIReport{Schema: 1, SeedBase: base, Member: "lib", Feed: f.f.URL + "/index.json", Label: "edge", Number: 7, Status: "packed", Pack: stackEdgePackResult{Status: "packed", Member: "lib", Version: version, RepositoryURL: "https://" + f.g.spec("you/lib"), Commit: f.track, Publishable: true, Provenance: map[string]stackEdgeProvenance{"lib": {TrackCommit: f.track, TrackParent: f.parent, UpstreamCommit: f.parent}}, Expected: want}}
	f.packageFile(t, "You.Lib", version, "", "Lib")
	return f
}

func (f *edgeCITestFixture) packageFile(t *testing.T, id, version, deps, assembly string) {
	t.Helper()
	edgeTestNupkg(t, f.o.Out, "package.nupkg", id, version, deps, assembly)
	stat, err := os.Stat(filepath.Join(f.o.Out, "package.nupkg"))
	if err != nil {
		t.Fatal(err)
	}
	f.r.Pack.Artifacts = []stackEdgeVerifiedPackage{{File: "package.nupkg", ID: id, Size: uint64(stat.Size())}}
	f.r.SHA256, err = stackEdgeCIHashes(f.o.Out, f.r.Pack.Artifacts)
	if err != nil {
		t.Fatal(err)
	}
}

func (f *edgeCITestFixture) run(t *testing.T) (string, error) {
	t.Helper()
	if err := stackEdgeCIWriteReport(f.o.Publish, f.r); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := stackEdgeCI(t.Context(), &out, f.o)
	if strings.Contains(out.String(), edgeCIKey) || err != nil && strings.Contains(err.Error(), edgeCIKey) {
		t.Fatal("credential leaked")
	}
	return out.String(), err
}

func TestStackEdgeCIPublish(t *testing.T) {
	f := newEdgeCITestFixture(t)
	before := mustGitStack(t, f.g.path("you/lib"), "for-each-ref")
	out, err := f.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Published 1") || len(f.f.pushes) != 1 {
		t.Fatalf("out=%q pushes=%d", out, len(f.f.pushes))
	}
	data, err := os.ReadFile(filepath.Join(f.o.Out, "package.nupkg"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, f.f.pushes[0]) {
		t.Fatal("push did not contain the exact package")
	}
	if !reflect.DeepEqual(f.f.requests, []string{"GET /index.json", "GET /flat/you.lib/index.json", "PUT /push"}) {
		t.Fatal(f.f.requests)
	}
	if after := mustGitStack(t, f.g.path("you/lib"), "for-each-ref"); after != before {
		t.Fatal("publish changed fork refs")
	}
}

func TestStackEdgeCIPublishRefusals(t *testing.T) {
	for _, name := range []string{"ancestry", "sha", "verification", "inconclusive", "tip", "zero", "reset", "409", "500", "read-500", "extra-id", "original-id", "version", "member-dependency", "missing-id", "changed-feed", "changed-label", "seed-moved", "here", "claim-assembly", "claim-third-party"} {
		t.Run(name, func(t *testing.T) {
			f := newEdgeCITestFixture(t)
			want := ""
			switch name {
			case "ancestry":
				// A fetchable fork-network commit on a different branch must not
				// count as a member of the integration branch.
				f.g.commit(t, "acme/lib", "unrelated.txt", "other\n", "not built")
				sha := strings.TrimSpace(mustGitStack(t, f.g.path("acme/lib"), "rev-parse", "main"))
				mustGitStack(t, f.g.path("acme/lib"), "push", "-q", f.g.path("you/lib"), sha+":refs/heads/unrelated")
				p := f.r.Pack.Provenance["lib"]
				p.TrackCommit = sha
				f.r.Pack.Provenance["lib"] = p
				f.r.Pack.Commit = sha
				want = "not on the fork"
			case "sha":
				// Change only a ZIP timestamp: metadata, size and verifier verdict
				// remain identical, so only the hash protects the original bytes.
				path := filepath.Join(f.o.Out, "package.nupkg")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				data[10] ^= 1
				if err = os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				want = "sha256"
			case "verification":
				f.packageFile(t, "Lib", f.r.Pack.Version, "", "Lib")
				want = "not a republished"
			case "inconclusive":
				f.r.Status = "inconclusive"
				want = "only packed"
			case "tip":
				f.r.Tip = true
				p := f.r.Pack.Provenance["lib"]
				p.UpstreamTip = f.parent
				f.r.Pack.Provenance["lib"] = p
				want = "--allow-tip-publish"
			case "zero":
				f.r.Number = 0
				want = "nonzero"
			case "reset":
				f.f.versions = []string{"1.2.4-edge.6", "1.2.4-edge.10", "2.0.0-edge.1"}
				want = "increase"
			case "409":
				f.f.pushStatus = 409
				want = "1.2.4-edge.7 already exists"
			case "500":
				f.f.pushStatus = 500
				want = "HTTP 500"
			case "read-500":
				f.f.readStatus = 500
				want = "HTTP 500"
			case "extra-id":
				f.r.Pack.Expected.MemberIDs["Extra"] = "You.Extra"
				f.r.Pack.Expected.Packages = []stackEdgeExpectedPackage{{OriginalID: "Extra", ID: "You.Extra", AssemblyBefore: "Extra", AssemblyAfter: "Extra"}}
				f.packageFile(t, "You.Extra", f.r.Pack.Version, "", "Extra")
				want = "untrusted or duplicate republished id"
			case "original-id":
				f.r.Pack.Expected.MemberIDs["Lib"] = "Lib"
				f.r.Pack.Expected.Packages[0].ID = "Lib"
				f.packageFile(t, "Lib", f.r.Pack.Version, "", "Lib")
				want = "untrusted or duplicate republished id"
			case "version":
				f.r.Pack.Version = "2.0.0-edge.7"
				f.r.Pack.Expected.Version = f.r.Pack.Version
				f.packageFile(t, "You.Lib", f.r.Pack.Version, "", "Lib")
				want = "computed version"
			case "member-dependency":
				f.r.Pack.Expected.Packages[0].Dependencies = []stackEdgeDependency{{ID: "Lib", Version: "1.2.3"}}
				f.packageFile(t, "You.Lib", f.r.Pack.Version, `<dependency id="Lib" version="1.2.3"/>`, "Lib")
				want = "member dependency"
			case "missing-id":
				f.r.Pack.Expected.Packages = nil
				want = "non-empty subset"
			case "changed-feed":
				f.r.Feed = "https://attacker.invalid/index.json"
				want = "trusted seed"
			case "changed-label":
				f.r.Label = "other"
				want = "trusted seed"
			case "seed-moved":
				f.r.SeedBase = strings.Repeat("a", 40)
				want = "trusted seed"
			case "here":
				f.r.Pack.Here = true
				want = "only packed"
			case "claim-assembly":
				f.r.Pack.Expected.Packages[0].AssemblyAfter = "Changed"
				want = "AssemblyName changed"
			case "claim-third-party":
				f.r.Pack.Expected.Packages[0].Dependencies = []stackEdgeDependency{{ID: "ThirdParty", Version: "[3.0.0]"}}
				want = "from discovery is missing"
			}
			_, err := f.run(t)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("want %q, got %v", want, err)
			}
			if len(f.f.pushes) != 0 && name != "409" && name != "500" {
				t.Fatal("refused report reached push")
			}
			if len(f.f.pushes) > 1 {
				t.Fatal("unexpected push retry")
			}
		})
	}
}

func TestStackEdgeCIPublishAllowsTipAndAncestor(t *testing.T) {
	f := newEdgeCITestFixture(t)
	f.r.Tip = true
	f.o.AllowTip = true
	p := f.r.Pack.Provenance["lib"]
	p.UpstreamTip = f.parent
	f.r.Pack.Provenance["lib"] = p
	// Move trackBranch forward without removing the report's commit.
	work := t.TempDir()
	mustGitStack(t, work, "clone", "-q", "--branch", "stack/integration", f.g.path("you/lib"), work)
	mustGitStack(t, work, "-c", "user.name=test", "-c", "user.email=test@localhost", "commit", "--allow-empty", "-qm", "later track")
	mustGitStack(t, work, "push", "-q", "origin", "HEAD")
	if _, err := f.run(t); err != nil {
		t.Fatal(err)
	}
}

func TestStackEdgeCIPublishBuildClaims(t *testing.T) {
	f := newEdgeCITestFixture(t)
	f.r.Pack.Expected.Packages[0].AssemblyBefore = "Evaluated"
	f.r.Pack.Expected.Packages[0].AssemblyAfter = "Evaluated"
	f.r.Pack.Expected.Packages[0].Dependencies = []stackEdgeDependency{{ID: "ThirdParty", Version: "[3.0.0]"}}
	f.packageFile(t, "You.Lib", f.r.Pack.Version, `<dependency id="ThirdParty" version="[3.0.0]"/>`, "Evaluated")
	if _, err := f.run(t); err != nil {
		t.Fatal(err)
	}
}

func TestStackEdgeCIReportRefusesCredential(t *testing.T) {
	f := newEdgeCITestFixture(t)
	f.o.Publish = ""
	f.o.Report = filepath.Join(t.TempDir(), "edge.json")
	var out bytes.Buffer
	err := stackEdgeCI(t.Context(), &out, f.o)
	if err == nil || !strings.Contains(err.Error(), "must not receive") || strings.Contains(err.Error(), edgeCIKey) || strings.Contains(out.String(), edgeCIKey) {
		t.Fatalf("out=%q err=%v", out.String(), err)
	}
	if _, err = os.Stat(f.o.Report); !os.IsNotExist(err) {
		t.Fatal("credential-bearing job wrote report")
	}
}

func TestStackEdgeCIFlags(t *testing.T) {
	for _, args := range [][]string{{}, {"--seed", "acme/stack@main", "--member", "lib"}, {"--seed", "s", "--member", "lib", "--report", "r", "--publish", "p"}, {"--seed", "s", "--member", "lib", "--report", "r"}, {"--seed", "s", "--member", "lib", "--publish", "p"}, {"--seed", "s", "--member", "lib", "--publish", "p", "--number", "7", "--build", "ignored"}} {
		c := newStackEdgeCICmd()
		c.SilenceUsage = true
		c.SilenceErrors = true
		c.SetOut(io.Discard)
		c.SetErr(io.Discard)
		c.SetArgs(args)
		if err := c.Execute(); err == nil {
			t.Fatalf("accepted %q", args)
		}
	}
}

func TestStackEdgeCIMenu(t *testing.T) {
	var labels []string
	for _, entry := range stackEdgeMenuEntries() {
		labels = append(labels, entry.Label)
		if entry.Cmd == nil || entry.Cmd.RunE == nil || entry.Cmd.Args != nil && entry.Cmd.Args(entry.Cmd, nil) != nil {
			t.Fatalf("menu entry %q must run without positional arguments", entry.Label)
		}
	}
	if !reflect.DeepEqual(labels, []string{"init", "pack", "status", "adopt"}) {
		t.Fatalf("interactive edge actions = %q; CI must be absent", labels)
	}
	edge := newStackEdgeCmd()
	ci, _, err := edge.Find([]string{"ci"})
	if err != nil || ci == edge || ci.Hidden {
		t.Fatal("CI must remain discoverable from the command line")
	}
}

func (f *edgeCITestFixture) adoptedMoves(t *testing.T) []stackEdgeAdoptedMove {
	t.Helper()
	m, _, err := loadStackManifest(f.checkout)
	if err != nil {
		t.Fatal(err)
	}
	pinned, head := strings.Repeat("a", 40), strings.Repeat("b", 40)
	pr := "https://" + m.Repos["lib"].Upstream + "/pull/2"
	m.Edge["lib"].Adopted = []stackEdgeAdopted{{PR: pr, Commit: pinned}, {PR: strings.TrimSuffix(pr, "2") + "1", Commit: pinned}}
	data, _ := json.Marshal(m)
	writeStackManifest(t, f.checkout, string(data))
	mustGitStack(t, f.checkout, "add", "rig.stack.jsonc")
	mustGitStack(t, f.checkout, "-c", "user.name=test", "-c", "user.email=test@localhost", "commit", "-qm", "adopted pins")
	mustGitStack(t, f.checkout, "push", f.o.Seed, "HEAD")
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("movement check wrote: %s", r.Method)
		}
		if strings.HasSuffix(r.URL.Path, "/pulls/1") {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path != "/repos/acme/lib/pulls/2" {
			t.Errorf("unexpected request %s", r.URL)
		}
		fmt.Fprintf(w, `{"head":{"sha":%q}}`, head)
	}))
	t.Cleanup(api.Close)
	t.Setenv("GITHUB_API_URL", api.URL)
	return []stackEdgeAdoptedMove{{PR: pr, Pinned: pinned, Head: head}}
}

func TestStackEdgeCIReportAdoptedMoved(t *testing.T) {
	f := newEdgeCITestFixture(t)
	want := f.adoptedMoves(t)
	t.Setenv("EDGE_FEED_KEY", "")
	f.g.commit(t, "acme/lib", "fix.txt", "upstream conflict\n", "advance upstream")
	f.o.Publish, f.o.Report, f.o.Tip = "", filepath.Join(t.TempDir(), "edge.json"), true
	var out bytes.Buffer
	if err := stackEdgeCI(t.Context(), &out, f.o); err != nil {
		t.Fatal(err)
	}
	r, err := stackEdgeCIReadReport(f.o.Report)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "inconclusive" || len(r.AdoptedMoved) != 1 || r.AdoptedMoved[0].PR != want[0].PR || r.AdoptedMoved[0].Pinned != want[0].Pinned || r.AdoptedMoved[0].Head != want[0].Head || r.AdoptedMoved[0].Warning == "" {
		t.Fatalf("report moves: %+v", r)
	}
	if !strings.Contains(out.String(), r.AdoptedMoved[0].Warning) || !strings.Contains(out.String(), "could not check every adopted PR") {
		t.Fatalf("warnings missing: %s", &out)
	}
	data, _ := os.ReadFile(f.o.Report)
	if !strings.Contains(string(data), `"adoptedMoved"`) || !strings.Contains(string(data), `"pinned"`) {
		t.Fatal("report fields missing")
	}
}

func TestStackEdgeCIPublishAdoptedMovedDoesNotBlock(t *testing.T) {
	f := newEdgeCITestFixture(t)
	f.r.AdoptedMoved = []stackEdgeAdoptedMove{{PR: "https://github.com/acme/Lib/pull/2", Pinned: strings.Repeat("a", 40), Head: strings.Repeat("b", 40), Warning: "adopted PR moved"}}
	if _, err := f.run(t); err != nil {
		t.Fatal(err)
	}
	if len(f.f.pushes) != 1 {
		t.Fatal("warning blocked publish")
	}
}
