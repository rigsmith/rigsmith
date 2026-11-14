package node

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/core/plugin"
)

// pnpmWorkspaceFixture is a two-package pnpm workspace: @acme/theme depends on
// @acme/core through workspace:* (and peers on it through workspace:^), as
// Zoo UI's theme package does on its core.
func pnpmWorkspaceFixture(t *testing.T) (repo string, theme plugin.Package) {
	t.Helper()
	repo = filepath.Join(t.TempDir(), "repo")
	writeFiles(t, repo, map[string]string{
		"package.json":                `{"name":"root","private":true}`,
		"pnpm-workspace.yaml":         "packages:\n  - 'packages/*'\n",
		"packages/core/package.json":  `{"name":"@acme/core","version":"0.1.0"}`,
		"packages/theme/package.json": `{"name":"@acme/theme","version":"0.1.0","dependencies":{"@acme/core":"workspace:*"},"peerDependencies":{"@acme/core":"workspace:^"}}`,
		"packages/theme/index.js":     "export {}\n",
		"packages/core/index.js":      "export {}\n",
	})
	return repo, plugin.Package{
		Name:         "@acme/theme",
		Version:      "0.1.0",
		Dir:          "packages/theme",
		Dependencies: []plugin.Dependency{{Name: "@acme/core", Kind: plugin.DepNormal, Range: "workspace:*"}},
	}
}

// tarballManifest reads package/package.json out of an npm tarball.
func tarballManifest(t *testing.T, path string) map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			t.Fatalf("%s has no package/package.json", path)
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Name != "package/package.json" {
			continue
		}
		var m map[string]any
		if err := json.NewDecoder(tr).Decode(&m); err != nil {
			t.Fatal(err)
		}
		return m
	}
}

// stubPnpmPack stands in for pnpm: it writes a tarball whose manifest has the
// workspace: ranges resolved, and records the directory it packed.
func stubPnpmPack(t *testing.T) *[]string {
	t.Helper()
	var dirs []string
	was := pnpmPack
	pnpmPack = func(_ context.Context, dir, dest string) (string, string, error) {
		dirs = append(dirs, dir)
		return "", "", writeTarball(filepath.Join(dest, "acme-theme-0.1.0.tgz"), `{"name":"@acme/theme","version":"0.1.0","dependencies":{"@acme/core":"0.1.0"}}`)
	}
	t.Cleanup(func() { pnpmPack = was })
	return &dirs
}

func writeTarball(path, manifest string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "package/package.json", Mode: 0o644, Size: int64(len(manifest))}); err != nil {
		return err
	}
	if _, err := tw.Write([]byte(manifest)); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// stubPublish records npm publish's arguments and, since the tarball lives in a
// temp directory removed when Publish returns, the manifest of the tarball it
// was handed.
func stubPublish(t *testing.T) (args *[]string, manifest *map[string]any) {
	t.Helper()
	var got []string
	var m map[string]any
	was := npmPublish
	npmPublish = func(_ context.Context, _ string, _ []string, a ...string) (string, string, error) {
		got = a
		if len(a) > 1 && strings.HasSuffix(a[1], ".tgz") {
			m = tarballManifest(t, a[1])
		}
		return "", "", nil
	}
	t.Cleanup(func() { npmPublish = was })
	return &got, &m
}

// noProvenanceEnv makes the run look like it isn't in GitHub Actions, so
// provenance stays out of the arguments under test.
func noProvenanceEnv(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
}

// A pnpm workspace publishes the tarball pnpm packed — workspace:* resolved —
// through npm publish's tarball argument, not npm's own pack of the directory.
func TestPublishPacksWithPnpmInAPnpmWorkspace(t *testing.T) {
	noProvenanceEnv(t)
	for _, marker := range []string{"pnpm-workspace.yaml", "pnpm-lock.yaml"} {
		t.Run(marker, func(t *testing.T) {
			repo, theme := pnpmWorkspaceFixture(t)
			if marker == "pnpm-lock.yaml" {
				os.Remove(filepath.Join(repo, "pnpm-workspace.yaml"))
				writeFiles(t, repo, map[string]string{"pnpm-lock.yaml": "lockfileVersion: '9.0'\n"})
			}
			stubNpmView(t, "", "npm error code E404", errors.New("exit status 1"))
			packed := stubPnpmPack(t)
			args, manifest := stubPublish(t)

			resp, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{RepoRoot: repo, Package: theme, Access: "public"})
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{filepath.Join(repo, "packages/theme")}; !slices.Equal(*packed, want) {
				t.Errorf("pnpm packed %v, want %v", *packed, want)
			}
			if len(*args) < 2 || !strings.HasSuffix((*args)[1], "acme-theme-0.1.0.tgz") {
				t.Fatalf("npm publish args = %v, want the packed tarball second", *args)
			}
			if want := []string{"--access", "public"}; !slices.Equal((*args)[2:], want) {
				t.Errorf("npm publish args after the tarball = %v, want %v", (*args)[2:], want)
			}
			deps, _ := (*manifest)["dependencies"].(map[string]any)
			if deps["@acme/core"] != "0.1.0" {
				t.Errorf("published manifest dependencies = %v, want @acme/core at 0.1.0", deps)
			}
			if !strings.Contains(resp.Message, "packed with pnpm") {
				t.Errorf("message = %q, want it to say pnpm packed", resp.Message)
			}
		})
	}
}

// A file `shiprig pack` built is published as it is, pnpm workspace or not.
func TestPublishSendsAPackedArtifactUnrepacked(t *testing.T) {
	noProvenanceEnv(t)
	repo, theme := pnpmWorkspaceFixture(t)
	stubNpmView(t, "", "npm error code E404", errors.New("exit status 1"))
	packed := stubPnpmPack(t)
	args, _ := stubPublish(t)
	artifact := filepath.Join(t.TempDir(), "acme-theme-0.1.0.tgz")
	if err := writeTarball(artifact, `{"name":"@acme/theme","version":"0.1.0"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{RepoRoot: repo, Package: theme, ArtifactPath: artifact}); err != nil {
		t.Fatal(err)
	}
	if len(*packed) != 0 {
		t.Errorf("pnpm packed %v, want nothing packed", *packed)
	}
	if len(*args) < 2 || (*args)[1] != artifact {
		t.Errorf("npm publish args = %v, want %s", *args, artifact)
	}
}

// Outside a pnpm workspace nothing rewrites workspace: ranges, so the publish is
// refused — in a dry run too — rather than shipping a manifest no registry can
// resolve.
func TestPublishRefusesWorkspaceProtocolOutsidePnpm(t *testing.T) {
	noProvenanceEnv(t)
	repo, theme := pnpmWorkspaceFixture(t)
	os.Remove(filepath.Join(repo, "pnpm-workspace.yaml"))
	stubNpmView(t, "", "npm error code E404", errors.New("exit status 1"))
	stubPnpmPack(t)
	args, _ := stubPublish(t)
	for _, dry := range []bool{true, false} {
		_, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{RepoRoot: repo, Package: theme, DryRun: dry})
		if err == nil || !strings.Contains(err.Error(), "workspace:*") {
			t.Errorf("dry run %v: err = %v, want a refusal naming the workspace: range", dry, err)
		}
	}
	if *args != nil {
		t.Errorf("npm publish ran with %v", *args)
	}
}

func TestPublishDryRunSaysPnpmPacks(t *testing.T) {
	repo, theme := pnpmWorkspaceFixture(t)
	stubNpmView(t, "", "npm error code E404", errors.New("exit status 1"))
	resp, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{RepoRoot: repo, Package: theme, DryRun: true})
	if err != nil || resp.Message != "dry-run: would pnpm pack and npm publish @acme/theme@0.1.0" {
		t.Errorf("resp = %+v, err = %v", resp, err)
	}
}

// With real pnpm (skipped without it): the workspace is installed, as a release
// job's build has done, and the tarball `shiprig pack` builds — the same one
// Publish sends — carries the real version where the manifest says workspace:.
func TestPnpmPackRewritesWorkspaceProtocol(t *testing.T) {
	if _, err := exec.LookPath("pnpm"); err != nil {
		t.Skip("pnpm not installed")
	}
	// Node caches compiled code in TMPDIR otherwise, outliving the test.
	t.Setenv("NODE_DISABLE_COMPILE_CACHE", "1")
	repo, theme := pnpmWorkspaceFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Workspace packages only, so nothing is fetched.
	install := exec.CommandContext(ctx, "pnpm", "install", "--offline")
	install.Dir = repo
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("pnpm install: %v\n%s", err, out)
	}

	out := t.TempDir()
	resp, err := (&Adapter{}).Artifacts(ctx, plugin.ArtifactsRequest{RepoRoot: repo, Package: theme, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Artifacts) != 1 {
		t.Fatalf("artifacts = %+v, want one tarball", resp.Artifacts)
	}
	m := tarballManifest(t, resp.Artifacts[0].Path)
	deps, _ := m["dependencies"].(map[string]any)
	peers, _ := m["peerDependencies"].(map[string]any)
	if deps["@acme/core"] != "0.1.0" || peers["@acme/core"] != "^0.1.0" {
		t.Errorf("packed manifest: dependencies %v, peerDependencies %v; want 0.1.0 and ^0.1.0", deps, peers)
	}
	// Only the tarball is left in the output directory.
	entries, _ := os.ReadDir(out)
	if len(entries) != 1 {
		t.Errorf("output dir holds %d entries, want just the tarball", len(entries))
	}
}

// Outside pnpm the refusal doesn't depend on how the package is shipped: a
// file `shiprig pack` built is refused at publish, and pack refuses before npm
// writes the ranges into a tarball.
func TestWorkspaceProtocolRefusedOnThePackPathOutsidePnpm(t *testing.T) {
	noProvenanceEnv(t)
	repo, theme := pnpmWorkspaceFixture(t)
	os.Remove(filepath.Join(repo, "pnpm-workspace.yaml"))
	stubNpmView(t, "", "npm error code E404", errors.New("exit status 1"))
	args, _ := stubPublish(t)

	artifact := filepath.Join(t.TempDir(), "acme-theme-0.1.0.tgz")
	if err := writeTarball(artifact, `{"name":"@acme/theme","version":"0.1.0","dependencies":{"@acme/core":"workspace:*"}}`); err != nil {
		t.Fatal(err)
	}
	_, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{RepoRoot: repo, Package: theme, ArtifactPath: artifact})
	if err == nil || !strings.Contains(err.Error(), "workspace:*") {
		t.Errorf("publish of a packed artifact: err = %v, want the workspace: refusal", err)
	}
	if *args != nil {
		t.Errorf("npm publish ran with %v", *args)
	}

	out := t.TempDir()
	_, err = (&Adapter{}).Artifacts(context.Background(), plugin.ArtifactsRequest{RepoRoot: repo, Package: theme, OutputDir: out})
	if err == nil || !strings.Contains(err.Error(), "npm pack @acme/theme@0.1.0") {
		t.Errorf("pack: err = %v, want the workspace: refusal before npm pack", err)
	}
}

// optionalDependencies are discovered as in-repo edges, so one written with
// workspace: is refused like a dependency.
func TestOptionalWorkspaceDependencyIsDiscoveredAndRefused(t *testing.T) {
	noProvenanceEnv(t)
	repo := filepath.Join(t.TempDir(), "repo")
	writeFiles(t, repo, map[string]string{
		"package.json":                `{"name":"root","private":true,"workspaces":["packages/*"]}`,
		"packages/core/package.json":  `{"name":"@acme/core","version":"0.1.0"}`,
		"packages/theme/package.json": `{"name":"@acme/theme","version":"0.1.0","optionalDependencies":{"@acme/core":"workspace:*"}}`,
	})
	resp, err := (&Adapter{}).Discover(context.Background(), plugin.DiscoverRequest{RepoRoot: repo, SourcePath: "."})
	if err != nil {
		t.Fatal(err)
	}
	var theme plugin.Package
	for _, p := range resp.Packages {
		if p.Name == "@acme/theme" {
			theme = p
		}
	}
	if len(theme.Dependencies) != 1 || theme.Dependencies[0].Name != "@acme/core" || theme.Dependencies[0].Range != "workspace:*" {
		t.Fatalf("theme dependencies = %+v, want the optional @acme/core edge", theme.Dependencies)
	}
	stubNpmView(t, "", "npm error code E404", errors.New("exit status 1"))
	_, err = (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{RepoRoot: repo, Package: theme, DryRun: true})
	if err == nil || !strings.Contains(err.Error(), "workspace:*") {
		t.Errorf("err = %v, want the workspace: refusal", err)
	}
}

// stubScripts stands in for pnpm's script runner and the ignore-scripts
// lookup, recording each step (pack and upload included) in order.
func stubScripts(t *testing.T, ignored bool) *[]string {
	t.Helper()
	var steps []string
	wasRun, wasIgnored, wasPack, wasPublish := runPackageScript, scriptsIgnored, pnpmPack, npmPublish
	runPackageScript = func(_ context.Context, _, name string) error { steps = append(steps, "script:"+name); return nil }
	scriptsIgnored = func(context.Context, string) bool { return ignored }
	pnpmPack = func(ctx context.Context, dir, dest string) (string, string, error) {
		steps = append(steps, "pnpm pack")
		return wasPack(ctx, dir, dest)
	}
	npmPublish = func(context.Context, string, []string, ...string) (string, string, error) {
		steps = append(steps, "npm publish")
		return "", "", nil
	}
	t.Cleanup(func() {
		runPackageScript, scriptsIgnored, pnpmPack, npmPublish = wasRun, wasIgnored, wasPack, wasPublish
	})
	return &steps
}

// A pnpm-packed publish runs the publish scripts a directory publish would, in
// npm's order: prepublishOnly before the pack (pnpm pack runs its own prepack,
// prepare and postpack), publish and postpublish after the upload. A script
// the package doesn't define isn't run, and ignore-scripts runs none.
func TestPnpmPublishRunsTheLifecycleScripts(t *testing.T) {
	noProvenanceEnv(t)
	repo, theme := pnpmWorkspaceFixture(t)
	writeFiles(t, repo, map[string]string{"packages/theme/package.json": `{"name":"@acme/theme","version":"0.1.0","scripts":{"prepublishOnly":"tsc","prepack":"x","postpublish":"notify"},"dependencies":{"@acme/core":"workspace:*"}}`})
	stubNpmView(t, "", "npm error code E404", errors.New("exit status 1"))
	stubPnpmPack(t)

	steps := stubScripts(t, false)
	if _, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{RepoRoot: repo, Package: theme}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"script:prepublishOnly", "pnpm pack", "npm publish", "script:postpublish"}; !slices.Equal(*steps, want) {
		t.Errorf("steps = %v, want %v", *steps, want)
	}

	steps = stubScripts(t, true)
	if _, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{RepoRoot: repo, Package: theme}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"pnpm pack", "npm publish"}; !slices.Equal(*steps, want) {
		t.Errorf("with ignore-scripts: steps = %v, want %v", *steps, want)
	}
}
