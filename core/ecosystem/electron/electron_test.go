package electron

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDetectMatrix covers the ways a package.json signals an Electron app, and a
// plain library that must not be claimed.
func TestDetectMatrix(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string // path under dir -> content
		want  bool
	}{
		{"electron dep", map[string]string{"package.json": `{"name":"a","version":"1.0.0","devDependencies":{"electron":"^30"}}`}, true},
		{"build key", map[string]string{"package.json": `{"name":"a","version":"1.0.0","build":{"appId":"com.x"}}`}, true},
		{"forge config in pkg", map[string]string{"package.json": `{"name":"a","version":"1.0.0","config":{"forge":{}}}`}, true},
		{"forge config file", map[string]string{"package.json": `{"name":"a","version":"1.0.0"}`, "forge.config.js": "module.exports = {}"}, true},
		{"builder config file", map[string]string{"package.json": `{"name":"a","version":"1.0.0"}`, "electron-builder.yml": "appId: com.x"}, true},
		{"plain library", map[string]string{"package.json": `{"name":"a","version":"1.0.0","dependencies":{"left-pad":"^1"}}`}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, content := range tc.files {
				writeFile(t, filepath.Join(dir, rel), content)
			}
			pj := readPkg(filepath.Join(dir, "package.json"))
			if got := isElectronApp(dir, pj); got != tc.want {
				t.Errorf("isElectronApp = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDiscoverOnlyElectronPackages: in a workspace with an app and a library, only
// the Electron app is discovered by this adapter.
func TestDiscoverOnlyElectronPackages(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "apps", "desktop", "package.json"), `{"name":"desktop","version":"1.4.0","private":true,"devDependencies":{"electron":"^30"}}`)
	writeFile(t, filepath.Join(root, "packages", "lib", "package.json"), `{"name":"lib","version":"0.9.0"}`)

	resp, err := New().Discover(context.Background(), plugin.DiscoverRequest{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Packages) != 1 {
		t.Fatalf("expected only the electron app, got %+v", resp.Packages)
	}
	p := resp.Packages[0]
	if p.Name != "desktop" || p.Version != "1.4.0" || p.Dir != filepath.Join("apps", "desktop") {
		t.Errorf("package = %+v, want desktop@1.4.0 in apps/desktop", p)
	}
	if !p.Private {
		t.Errorf("private flag not carried: %+v", p)
	}
}

// TestSetVersion stamps package.json's version, preserving the rest.
func TestSetVersion(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "package.json")
	writeFile(t, manifest, `{
  "name": "desktop",
  "version": "1.4.0",
  "private": true
}`)

	err := New().SetVersion(context.Background(), plugin.SetVersionRequest{
		RepoRoot:   root,
		Package:    plugin.Package{ManifestPath: "package.json"},
		NewVersion: "1.5.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(manifest)
	if !strings.Contains(string(got), `"version": "1.5.0"`) {
		t.Errorf("version not updated: %s", got)
	}
	if !strings.Contains(string(got), `"name": "desktop"`) {
		t.Errorf("name clobbered: %s", got)
	}
}

// TestPublishNoOp: an Electron app is never npm-published by default.
func TestPublishNoOp(t *testing.T) {
	resp, err := New().Publish(context.Background(), plugin.PublishRequest{
		RepoRoot: t.TempDir(),
		Package:  plugin.Package{Name: "desktop", Version: "1.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Published || !resp.Skipped {
		t.Errorf("publish = %+v, want a skip", resp)
	}
}

// TestArtifactsDryRunBuilderSelection: dry-run names the builder it would run —
// electron-forge when a forge config is present, electron-builder otherwise.
func TestArtifactsDryRunBuilderSelection(t *testing.T) {
	t.Run("electron-builder default", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "app", "package.json"), `{"name":"d","version":"1.0.0","devDependencies":{"electron":"^30"}}`)
		resp, err := New().Artifacts(context.Background(), plugin.ArtifactsRequest{
			RepoRoot: root, DryRun: true, Package: plugin.Package{Name: "d", Version: "1.0.0", Dir: "app"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(resp.Message, "electron-builder build d@1.0.0") {
			t.Errorf("message = %q, want electron-builder", resp.Message)
		}
	})
	t.Run("electron-forge when configured", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "app", "package.json"), `{"name":"d","version":"1.0.0","config":{"forge":{}}}`)
		resp, err := New().Artifacts(context.Background(), plugin.ArtifactsRequest{
			RepoRoot: root, DryRun: true, Package: plugin.Package{Name: "d", Version: "1.0.0", Dir: "app"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(resp.Message, "electron-forge build d@1.0.0") {
			t.Errorf("message = %q, want electron-forge", resp.Message)
		}
	})
}

// TestArtifactsDryRunSigned: a dry-run with signing credentials reports it.
func TestArtifactsDryRunSigned(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "app", "package.json"), `{"name":"d","version":"1.0.0","devDependencies":{"electron":"^30"}}`)
	resp, err := New().Artifacts(context.Background(), plugin.ArtifactsRequest{
		RepoRoot: root, DryRun: true,
		Package: plugin.Package{Name: "d", Version: "1.0.0", Dir: "app"},
		Signing: &plugin.SigningCreds{Env: map[string]string{"CSC_LINK": "base64..."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Message, "(signed)") {
		t.Errorf("signed dry-run should note (signed); got %q", resp.Message)
	}
}

// TestMergeSigningEnv appends sorted KEY=VALUE entries, leaving base alone when
// there is nothing to sign with.
func TestMergeSigningEnv(t *testing.T) {
	base := []string{"PATH=/x"}
	if got := mergeSigningEnv(base, nil); len(got) != 1 {
		t.Errorf("nil signing should return base unchanged, got %v", got)
	}
	got := mergeSigningEnv(base, &plugin.SigningCreds{Env: map[string]string{"CSC_KEY_PASSWORD": "p", "CSC_LINK": "l"}})
	want := []string{"PATH=/x", "CSC_KEY_PASSWORD=p", "CSC_LINK=l"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("mergeSigningEnv = %v, want %v", got, want)
	}
}

// TestInfoCapabilities locks the contract: Artifacts yes, Publish no, overlays node.
func TestInfoCapabilities(t *testing.T) {
	info := New().Info()
	caps := map[string]bool{}
	for _, c := range info.Capabilities {
		caps[c] = true
	}
	if !caps[plugin.MethodArtifacts] {
		t.Error("Electron should advertise MethodArtifacts")
	}
	if caps[plugin.MethodPublish] {
		t.Error("Electron should NOT advertise MethodPublish")
	}
	if len(info.Overlays) != 1 || info.Overlays[0] != "node" {
		t.Errorf("Overlays = %v, want [node]", info.Overlays)
	}
}

// TestCollectInstallers gathers installers + updater manifests, ignoring junk.
func TestCollectInstallers(t *testing.T) {
	out := t.TempDir()
	writeFile(t, filepath.Join(out, "Desktop-1.0.0.dmg"), "x")
	writeFile(t, filepath.Join(out, "Desktop Setup 1.0.0.exe"), "x")
	writeFile(t, filepath.Join(out, "Desktop-1.0.0.AppImage"), "x")
	writeFile(t, filepath.Join(out, "desktop_1.0.0_amd64.snap"), "x")
	writeFile(t, filepath.Join(out, "Desktop-1.0.0.dmg.blockmap"), "x")
	writeFile(t, filepath.Join(out, "latest-mac.yml"), "version: 1.0.0")
	writeFile(t, filepath.Join(out, "builder-debug.yml"), "noise")
	writeFile(t, filepath.Join(out, "readme.txt"), "noise")

	got := map[string]bool{}
	for _, a := range collectInstallers(out) {
		if !a.Attach {
			t.Errorf("%s should be Attach:true", a.Path)
		}
		got[filepath.Base(a.Path)] = true
	}
	for _, want := range []string{"Desktop-1.0.0.dmg", "Desktop Setup 1.0.0.exe", "Desktop-1.0.0.AppImage", "desktop_1.0.0_amd64.snap", "Desktop-1.0.0.dmg.blockmap", "latest-mac.yml"} {
		if !got[want] {
			t.Errorf("expected %q among installers; got %v", want, got)
		}
	}
	if got["readme.txt"] || got["builder-debug.yml"] {
		t.Errorf("junk collected: %v", got)
	}
}

// A build that must not sign (--dry-build, --unsigned) uses electron-builder's
// documented off-switch: every CSC_* certificate variable unset,
// CSC_IDENTITY_AUTO_DISCOVERY=false, and notarization off.
func TestUnsignedBuildUsesElectronBuildersOffSwitch(t *testing.T) {
	for _, req := range []plugin.ArtifactsRequest{{Snapshot: true}, {Unsigned: true}} {
		got := strings.Join(builderArgs(req, 27), " ")
		if got != "electron-builder --publish never -c.mac.notarize=false -c.mac.sign.identity=null" {
			t.Errorf("snapshot=%v unsigned=%v: args = %q", req.Snapshot, req.Unsigned, got)
		}
	}
	if args := builderArgs(plugin.ArtifactsRequest{}, 27); strings.Join(args, " ") != "electron-builder --publish never" {
		t.Errorf("a signed build's args changed: %v", args)
	}

	env := unsignedBuilderEnv([]string{
		"PATH=/usr/bin", "CSC_LINK=/tmp/cert.p12", "CSC_KEY_PASSWORD=pw", "WIN_CSC_LINK=x",
		"CSC_NAME=Dev", "CSC_IDENTITY_AUTO_DISCOVERY=true", "APPLE_ID=you@example.com",
	})
	want := "PATH=/usr/bin|APPLE_ID=you@example.com|CSC_IDENTITY_AUTO_DISCOVERY=false"
	if got := strings.Join(env, "|"); got != want {
		t.Fatalf("unsignedBuilderEnv = %s, want %s", got, want)
	}
}

// The macOS identity override follows the installed electron-builder: v27
// rejects the old mac.identity key, and before v27 mac.sign means something
// else, so an unknown version gets neither.
func TestIdentityOverrideFollowsTheInstalledBuilder(t *testing.T) {
	for major, want := range map[int]string{28: "-c.mac.sign.identity=null", 27: "-c.mac.sign.identity=null", 26: "-c.mac.identity=null", 24: "-c.mac.identity=null", 0: ""} {
		if got := identityOverride(major); got != want {
			t.Errorf("identityOverride(%d) = %q, want %q", major, got, want)
		}
	}
	if got := strings.Join(builderArgs(plugin.ArtifactsRequest{Unsigned: true}, 0), " "); got != "electron-builder --publish never -c.mac.notarize=false" {
		t.Errorf("unknown version: args = %q", got)
	}

	// Found in the app's own node_modules, or hoisted to the workspace root.
	root := t.TempDir()
	app := filepath.Join(root, "apps", "desk")
	writeBuilder := func(dir, version string) {
		t.Helper()
		p := filepath.Join(dir, "node_modules", "electron-builder")
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "package.json"), []byte(`{"version":"`+version+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := installedBuilderMajor(app, root); got != 0 {
		t.Fatalf("nothing installed, got major %d", got)
	}
	writeBuilder(root, "26.0.12")
	if got := installedBuilderMajor(app, root); got != 26 {
		t.Fatalf("hoisted v26 read as %d", got)
	}
	writeBuilder(app, "27.1.0")
	if got := installedBuilderMajor(app, root); got != 27 {
		t.Fatalf("the app's own v27 read as %d", got)
	}
}
