package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStackEdgeInitTemporaryPackDotnetE2E(t *testing.T) {
	requireStackEdgeEngine(t)
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	// One tiny package exercises reconstruction and the real pack, leaving all
	// ordinary setup/doctor tests independent of dotnet and network access.
	srv := newGitServer(t, t.TempDir())
	srv.seed(t, "acme/lib", "lib")
	fork := srv.bare(t, "you/lib")
	upstream := srv.path("acme/lib")
	work := t.TempDir()
	mustGitStack(t, work, "clone", "-q", upstream, work)
	csproj(t, work, "Lib", "Lib")
	path := filepath.Join(work, "Lib", "Lib.csproj")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("<Version>"), []byte("<TargetFramework>net10.0</TargetFramework><Version>"), 1)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, work, "add", ".")
	mustGitStack(t, work, "-c", "user.name=test", "-c", "user.email=t@t", "commit", "-qm", "small package")
	mustGitStack(t, work, "push", "-q", upstream, "HEAD:main")
	mustGitStack(t, work, "push", "-q", fork, "HEAD:refs/heads/stack/integration")
	tip := strings.TrimSpace(mustGitStack(t, work, "rev-parse", "HEAD"))
	m := edgeTestManifest()
	m.Repos["lib"].Upstream = srv.spec("acme/lib")
	m.Repos["lib"].Fork = srv.spec("you/lib")
	m.LastSync = map[string]string{"lib": tip}
	manifest, _ := json.Marshal(m)
	root := inTempStackspace(t, string(manifest))
	mustGitStack(t, root, "add", ".")
	mustGitStack(t, root, "-c", "user.name=test", "-c", "user.email=t@t", "commit", "-qm", "manifest")
	// A caller's existing output must survive even when supplied as an option.
	output := filepath.Join(root, "dist")
	if err := os.MkdirAll(output, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, "keep.nupkg"), []byte("user output"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := mustGitStack(t, root, "status", "--porcelain")
	feed := edgeTestFeed(t)
	var out bytes.Buffer
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	t.Setenv("TMP", temp)
	t.Setenv("TEMP", temp)
	err = stackEdgeSetupVerifyPackWith(ctx, &out, root, "lib", stackEdgeReconstructOptions{Checkout: root, Members: []string{"lib"}, AllowedHosts: srv.addr}, stackEdgePackOptions{PublicFeed: feed.URL, Client: feed.Client(), OutputDir: output, Number: 99})
	if err != nil {
		t.Fatalf("temporary pack: %v\n%s", err, out.String())
	}
	for _, want := range []string{"runs project code", "can take a while", "number 0", "publishable: false", " bytes"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %s", want, out.String())
		}
	}
	assertStackEdgeFile(t, root, "dist/keep.nupkg", "user output")
	if before != mustGitStack(t, root, "status", "--porcelain") {
		t.Fatal("pack touched the checkout")
	}
	entries, err := os.ReadDir(temp)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "rig-edge-setup-pack-") || strings.HasPrefix(entry.Name(), "rig-stack-edge-") {
			t.Errorf("temporary pack leaked %s", entry.Name())
		}
	}
}
