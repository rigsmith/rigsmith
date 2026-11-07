package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/jsonc"
)

func TestStackReconstructOwnCommittedManifest(t *testing.T) {
	for _, path := range []string{"rig.stack.jsonc", "rig.stack.json", ".rig.json"} {
		t.Run(path, func(t *testing.T) {
			root := t.TempDir()
			mustGitStack(t, root, "init", "-q")
			data := `{"repos":{"lib":{"upstream":"github.com/acme/Lib"}}}`
			if path == ".rig.json" {
				data = `{"stack":` + data + `}`
			}
			if err := os.WriteFile(filepath.Join(root, path), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			mustGitStack(t, root, "add", ".")
			mustGitStack(t, root, "-c", "user.name=test", "-c", "user.email=t@t", "commit", "-qm", "manifest")
			// Neither dirty configuration nor an untracked competing manifest
			// may change the committed source selected for reconstruction.
			for _, file := range []string{path, "rig.stack.jsonc"} {
				if err := os.WriteFile(filepath.Join(root, file), []byte("invalid working tree"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, manifest := range []string{"", path} {
				got, _, err := stackReconstructManifest(t.Context(), root, t.TempDir(), manifest, "", "github.com")
				if err != nil {
					t.Fatal(err)
				}
				var m stackManifest
				if err := jsonc.Unmarshal(got, &m); err != nil || m.Repos["lib"] == nil || m.Repos["lib"].Upstream != "github.com/acme/Lib" {
					t.Fatalf("committed manifest = %s, %v", got, err)
				}
			}
		})
	}
}

func TestStackReconstructOwnManifestRefusals(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "ambiguous"}[ambiguous], func(t *testing.T) {
			root := t.TempDir()
			mustGitStack(t, root, "init", "-q")
			if ambiguous {
				for _, path := range []string{"rig.stack.jsonc", "rig.stack.json"} {
					if err := os.WriteFile(filepath.Join(root, path), []byte(`{"repos":{}}`), 0600); err != nil {
						t.Fatal(err)
					}
				}
				mustGitStack(t, root, "add", ".")
			}
			mustGitStack(t, root, "-c", "user.name=test", "-c", "user.email=t@t", "commit", "--allow-empty", "-qm", "source")
			data, _, err := stackReconstructManifest(t.Context(), root, t.TempDir(), "", "", "github.com")
			want := "no committed stack manifest"
			if ambiguous {
				want = "ambiguous"
			}
			if err == nil || !strings.Contains(err.Error(), want) || len(data) != 0 {
				t.Fatalf("want %s refusal, got %s, %v", want, data, err)
			}
		})
	}
}

func TestStackEdgePackTipConflict(t *testing.T) {
	requireStackEdgeEngine(t)
	f := newStackEdgeFixture(t)
	f.server.commit(t, "acme/lib", "src/lib.txt", "conflicting upstream\n", "upstream advances")
	chdir(t, f.checkout)
	dir := filepath.Join(f.checkout, "out")
	if err := os.MkdirAll(filepath.Join(dir, "old"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"stale.nupkg", "old/stale.snupkg", "edge-pack.json", "old/checksums.txt"} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(path)), []byte("stale output"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	tip := strings.TrimSpace(mustGitStack(t, f.server.path("acme/lib"), "rev-parse", "main"))
	dry := newStackEdgePackCmd()
	dry.SetArgs([]string{"lib", "--upstream-tip", "--allowed-hosts", f.server.addr, "--out", dir, "-n"})
	var out strings.Builder
	dry.SetOut(&out)
	dry.SetErr(&out)
	if err := dry.Execute(); err != nil || !strings.Contains(out.String(), "inconclusive") || !strings.Contains(out.String(), f.track["lib"]) || !strings.Contains(out.String(), tip) || !strings.Contains(out.String(), "rig stack propose lib --track-only") {
		t.Fatalf("dry-run conflict outcome: %v\n%s", err, out.String())
	}
	for _, path := range []string{"stale.nupkg", "old/stale.snupkg", "edge-pack.json", "old/checksums.txt"} {
		assertStackEdgeFile(t, dir, path, "stale output")
	}
	if files := stackPackWalk(dir); len(files) != 4 {
		t.Fatalf("dry-run changed existing output: %v", files)
	}
	out.Reset()
	c := newStackEdgePackCmd()
	c.SetArgs([]string{"lib", "--upstream-tip", "--allowed-hosts", f.server.addr, "--out", dir})
	c.SetOut(&out)
	c.SetErr(&out)
	if err := c.Execute(); err != nil {
		t.Fatalf("conflict was a build failure: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "inconclusive") || !strings.Contains(out.String(), "pull, resolve on the integration line, `rig stack propose lib --track-only`") {
		t.Fatalf("missing inconclusive remedy: %s", out.String())
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "edge-pack.json" {
		t.Fatalf("inconclusive output must contain only edge-pack.json: %v, %v", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "edge-pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report stackEdgePackResult
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	want := stackEdgeProvenance{TrackCommit: f.track["lib"], TrackParent: f.base["lib"], UpstreamCommit: f.base["lib"], UpstreamTip: tip}
	if report.Status != "inconclusive" || report.Member != "lib" || report.Commit != f.track["lib"] || report.Provenance["lib"] != want || report.Publishable || len(report.Artifacts) != 0 || !strings.Contains(report.Remedy, "rig stack propose lib --track-only") {
		t.Fatalf("inconclusive report: %s", data)
	}
	if !strings.Contains(out.String(), report.Member+": "+report.Status) || !strings.Contains(out.String(), report.Remedy) || !strings.Contains(out.String(), report.Commit) || !strings.Contains(out.String(), tip) {
		t.Fatalf("stdout does not match report: %s", out.String())
	}
	if got := mustGitStack(t, f.checkout, "diff", "HEAD"); got != "" {
		t.Fatalf("inconclusive reconstruction changed checkout: %s", got)
	}
	// The conflicting upstream does not prevent an ordinary track build.
	result, cleanup, err := stackEdgeReconstruct(t.Context(), f.options(false))
	defer cleanup()
	if err != nil {
		t.Fatalf("ordinary track mode should still reconstruct: %+v, %v", result, err)
	}
	if result.Provenance["lib"].TrackCommit != f.track["lib"] {
		t.Fatalf("wrong track commit: %+v", result.Provenance)
	}
}

func TestStackEdgeClearOutputRefusesSources(t *testing.T) {
	root := inTempStackspace(t, `{"repos":{}}`)
	mustGitStack(t, root, "add", ".")
	for _, path := range []string{root, filepath.Dir(root), filepath.Join(root, ".git")} {
		if err := stackEdgeClearOutput(t.Context(), root, path); err == nil {
			t.Fatalf("cleared source path %s", path)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "source"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source", "keep.txt"), []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, root, "add", ".")
	if err := stackEdgeClearOutput(t.Context(), root, filepath.Join(root, "source")); err == nil || !strings.Contains(err.Error(), "tracked source") {
		t.Fatalf("cleared tracked source: %v", err)
	}
	assertStackEdgeFile(t, root, "source/keep.txt", "source")
}
