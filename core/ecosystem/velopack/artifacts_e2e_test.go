package velopack

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

// TestArtifactsE2E drives the real Artifacts() against an actual Velopack project
// using the installed `dotnet` and `vpk` toolchains. It is skipped unless
// VELOPACK_E2E_DIR points at a project directory containing a .csproj and a
// velopack.json. It builds in snapshot (unsigned) mode so no certificates are
// needed. Env knobs: VELOPACK_E2E_CSPROJ (default <name>.csproj via the dir name
// is not assumed — set it), VELOPACK_E2E_NAME, VELOPACK_E2E_VERSION.
//
//	VELOPACK_E2E_DIR=/path/to/vptest VELOPACK_E2E_CSPROJ=vptest.csproj \
//	  go test ./core/ecosystem/velopack/ -run TestArtifactsE2E -v -timeout 20m
func TestArtifactsE2E(t *testing.T) {
	dir := os.Getenv("VELOPACK_E2E_DIR")
	if dir == "" {
		t.Skip("set VELOPACK_E2E_DIR to a Velopack project dir to run this integration test")
	}
	csproj := getenv("VELOPACK_E2E_CSPROJ", "vptest.csproj")
	name := getenv("VELOPACK_E2E_NAME", "vptest")
	version := getenv("VELOPACK_E2E_VERSION", "1.0.0")

	resp, err := New().Artifacts(context.Background(), plugin.ArtifactsRequest{
		RepoRoot:  dir,
		Package:   plugin.Package{Name: name, Version: version, Dir: ".", ManifestPath: csproj},
		OutputDir: filepath.Join(dir, "dist"),
		Snapshot:  true, // unsigned: no Developer ID / Azure creds needed
	})
	if err != nil {
		t.Fatalf("Artifacts: %v", err)
	}
	t.Logf("built=%v skipped=%v: %s", resp.Built, resp.Skipped, resp.Message)
	if !resp.Built {
		t.Fatalf("expected Built=true")
	}
	var attached, feedIndex, nupkg int
	for _, a := range resp.Artifacts {
		base := filepath.Base(a.Path)
		t.Logf("  attach=%-5v kind=%-8s %s", a.Attach, a.Kind, base)
		if a.Attach {
			attached++
		}
		if strings.HasPrefix(base, "releases.") && strings.HasSuffix(base, ".json") {
			feedIndex++
		}
		if strings.HasSuffix(base, ".nupkg") {
			nupkg++
		}
	}
	// The runtime feed the updater needs must exist and be attached.
	if feedIndex == 0 {
		t.Error("no releases.<channel>.json feed index produced")
	}
	if nupkg == 0 {
		t.Error("no .nupkg payload produced")
	}
	if attached == 0 {
		t.Error("no artifacts marked Attach:true")
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
