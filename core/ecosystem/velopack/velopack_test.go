package velopack

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

func TestInfoOverlaysDotnet(t *testing.T) {
	info := New().Info()
	if info.ID != "velopack" {
		t.Errorf("ID = %q, want velopack", info.ID)
	}
	if len(info.Overlays) != 1 || info.Overlays[0] != "dotnet" {
		t.Errorf("Overlays = %v, want [dotnet]", info.Overlays)
	}
	// Publish is intentionally not advertised — a Velopack app ships via forge release.
	for _, c := range info.Capabilities {
		if c == plugin.MethodPublish {
			t.Error("velopack must not advertise Publish")
		}
	}
}

// writeFile creates path (with parents) holding content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const csprojWithVelopack = `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <Version>1.2.0</Version>
    <PackageId>Halyards.Desktop</PackageId>
  </PropertyGroup>
  <ItemGroup>
    <PackageReference Include="Velopack" Version="1.2.0" />
  </ItemGroup>
</Project>`

const csprojPlainLib = `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <Version>3.0.0</Version>
    <PackageId>Acme.Lib</PackageId>
  </PropertyGroup>
</Project>`

// TestDiscoverClaimsOnlyVelopackConfiguredProjects pins that discovery returns a
// project only when a velopack.json sits next to its .csproj — the marker that
// makes it Velopack-owned (the plain library is left to the dotnet adapter).
func TestDiscoverClaimsOnlyVelopackConfiguredProjects(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "app", "Halyards.Desktop.csproj"), csprojWithVelopack)
	writeFile(t, filepath.Join(root, "app", "velopack.json"), `{"packId":"Halyards","channels":["win-x64"]}`)
	writeFile(t, filepath.Join(root, "lib", "Acme.Lib.csproj"), csprojPlainLib)

	resp, err := New().Discover(context.Background(), plugin.DiscoverRequest{RepoRoot: root, SourcePath: "."})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Packages) != 1 {
		t.Fatalf("discovered %d packages, want 1: %+v", len(resp.Packages), resp.Packages)
	}
	got := resp.Packages[0]
	if got.Name != "Halyards.Desktop" {
		t.Errorf("Name = %q, want Halyards.Desktop", got.Name)
	}
	if got.Version != "1.2.0" {
		t.Errorf("Version = %q, want 1.2.0 (delegated to dotnet)", got.Version)
	}
	if got.Dir != "app" {
		t.Errorf("Dir = %q, want app (must match dotnet's, so overlay reconciliation drops the dotnet pkg)", got.Dir)
	}
}

func TestDetect(t *testing.T) {
	root := t.TempDir()
	if ok, _ := New().Detect(context.Background(), root); ok {
		t.Error("empty repo should not detect velopack")
	}
	writeFile(t, filepath.Join(root, "app", "App.csproj"), csprojWithVelopack)
	writeFile(t, filepath.Join(root, "app", "velopack.json"), `{"packId":"App","channels":["win-x64"]}`)
	if ok, err := New().Detect(context.Background(), root); err != nil || !ok {
		t.Errorf("Detect = %v, %v; want true, nil", ok, err)
	}
}

func TestVelopackRefVersion(t *testing.T) {
	cases := []struct{ name, text, want string }{
		{"include then version", `<PackageReference Include="Velopack" Version="1.2.0" />`, "1.2.0"},
		{"version then include", `<PackageReference Version="2.0.1" Include="Velopack" />`, "2.0.1"},
		{"case insensitive id", `<PackageReference Include="velopack" Version="1.0.0" />`, "1.0.0"},
		{"no version attr (central pkg mgmt)", `<PackageReference Include="Velopack" />`, ""},
		{"not referenced", `<PackageReference Include="Something.Else" Version="1.0.0" />`, ""},
	}
	for _, c := range cases {
		if got := velopackRefVersion(c.text); got != c.want {
			t.Errorf("%s: velopackRefVersion = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestConfigDefaultsAndValidate(t *testing.T) {
	cfg := Config{PackId: "Halyards", Channels: []string{"osx-arm64"}}.withDefaults()
	if cfg.PackTitle != "Halyards" {
		t.Errorf("PackTitle default = %q, want Halyards", cfg.PackTitle)
	}
	if cfg.MainExe != "Halyards" {
		t.Errorf("MainExe default = %q, want Halyards", cfg.MainExe)
	}
	if cfg.Output != filepath.Join("dist", "releases") {
		t.Errorf("Output default = %q, want dist/releases", cfg.Output)
	}

	if err := (Config{Channels: []string{"win-x64"}}).validate(); err == nil {
		t.Error("missing packId should be invalid")
	}
	if err := (Config{PackId: "X"}).validate(); err == nil {
		t.Error("missing channels should be invalid")
	}
	if err := (Config{PackId: "X", Channels: []string{"freebsd-x64"}}).validate(); err == nil {
		t.Error("unknown RID should be invalid")
	}
	if err := (Config{PackId: "X", Channels: []string{"osx-arm64", "win-x64"}}).validate(); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
}

func TestMacosWrapDmgDefault(t *testing.T) {
	if !(Macos{}).WrapDmg() {
		t.Error("WrapDmg should default to true")
	}
	no := false
	if (Macos{Dmg: &no}).WrapDmg() {
		t.Error("WrapDmg should honor explicit false")
	}
}

func TestOsOfAndBuildableOn(t *testing.T) {
	if osOf("osx-arm64") != osMac || osOf("win-x64") != osWindows || osOf("linux-x64") != osLinux {
		t.Error("osOf RID mapping wrong")
	}
	if osOf("solaris-x64") != osUnknown {
		t.Error("unknown RID should be osUnknown")
	}
	// macOS channels need a macOS host; others build anywhere.
	if buildableOn(osMac, osWindows) {
		t.Error("macOS channel should not build on a Windows host")
	}
	if !buildableOn(osMac, osMac) {
		t.Error("macOS channel should build on a macOS host")
	}
	if !buildableOn(osWindows, osMac) || !buildableOn(osLinux, osWindows) {
		t.Error("Windows/Linux channels should build on any host")
	}
}

func TestBuildPublishArgs(t *testing.T) {
	got := strings.Join(buildPublishArgs("app/App.csproj", "osx-arm64", "dist/publish/osx-arm64"), " ")
	want := "publish app/App.csproj -c Release -r osx-arm64 --self-contained -p:PublishSingleFile=false -o dist/publish/osx-arm64 --nologo"
	if got != want {
		t.Errorf("publish args =\n  %q\nwant\n  %q", got, want)
	}
}

func fullCfg() Config {
	return Config{
		PackId: "Halyards", PackTitle: "Halyards", PackAuthors: "Acme, Inc.", MainExe: "Halyards",
		Channels: []string{"osx-arm64", "win-x64"},
		Icon:     Icon{Macos: "app/halyards.icns", Windows: "app/halyards.ico"},
		Output:   "dist/releases",
		Macos:    Macos{BundleId: "com.acme.halyards", SignIdentity: "Developer ID Application: Acme", NotaryProfile: "halyards-notary"},
		Windows:  Windows{TrustedSigning: &TrustedSigning{Endpoint: "https://eus.codesigning.azure.net", Account: "Acme", Profile: "Acme"}},
	}
}

func TestBuildPackArgsMacSigned(t *testing.T) {
	got := buildPackArgs(fullCfg(), "osx-arm64", "dist/publish/osx-arm64", "dist/releases", "1.0.0", false, "")
	assertHas(t, got, "--packId", "Halyards")
	assertHas(t, got, "--mainExe", "Halyards") // no .exe on macOS
	assertHas(t, got, "--bundleId", "com.acme.halyards")
	assertHas(t, got, "--signAppIdentity", "Developer ID Application: Acme")
	assertHas(t, got, "--notaryProfile", "halyards-notary")
	assertHas(t, got, "--channel", "osx-arm64")
	assertHas(t, got, "--icon", "app/halyards.icns")
	if contains(got, "--azureTrustedSignFile") {
		t.Error("macOS pack must not carry the Azure signing flag")
	}
}

func TestBuildPackArgsMacSnapshotIsUnsigned(t *testing.T) {
	got := buildPackArgs(fullCfg(), "osx-arm64", "p", "o", "1.0.0", true, "")
	if contains(got, "--signAppIdentity") || contains(got, "--notaryProfile") {
		t.Errorf("snapshot pack must be unsigned, got %v", got)
	}
	// Identity-independent flags still present.
	assertHas(t, got, "--bundleId", "com.acme.halyards")
}

func TestBuildPackArgsWindowsAzure(t *testing.T) {
	got := buildPackArgs(fullCfg(), "win-x64", "dist/publish/win-x64", "dist/releases", "1.0.0", false, "dist/trustedsigning.json")
	assertHas(t, got, "--mainExe", "Halyards.exe") // .exe appended on Windows
	assertHas(t, got, "--icon", "app/halyards.ico")
	assertHas(t, got, "--azureTrustedSignFile", "dist/trustedsigning.json")
	if contains(got, "--bundleId") || contains(got, "--signAppIdentity") {
		t.Error("Windows pack must not carry macOS flags")
	}
}

func TestBuildPackArgsWindowsSnapshotNoAzure(t *testing.T) {
	got := buildPackArgs(fullCfg(), "win-x64", "p", "o", "1.0.0", true, "")
	if contains(got, "--azureTrustedSignFile") {
		t.Errorf("snapshot Windows pack must be unsigned, got %v", got)
	}
}

func TestBuildPackArgsOmitsEmptyOptionalFlags(t *testing.T) {
	min := Config{PackId: "App", PackTitle: "App", MainExe: "App", Channels: []string{"osx-arm64"}, Output: "out"}
	got := buildPackArgs(min, "osx-arm64", "p", "out", "1.0.0", false, "")
	for _, f := range []string{"--packAuthors", "--icon", "--bundleId", "--signAppIdentity", "--notaryProfile"} {
		if contains(got, f) {
			t.Errorf("unset optional flag %s should be omitted, got %v", f, got)
		}
	}
}

func TestMajorMismatch(t *testing.T) {
	if _, _, ok := majorMismatch("1.2.0", "Velopack (vpk) 1.0.1298"); !ok {
		t.Error("same major (1 == 1) should match")
	}
	if _, _, ok := majorMismatch("1.2.0", "vpk 2.0.5"); ok {
		t.Error("different majors (1 vs 2) should mismatch")
	}
	// Unparseable side → not enforced (ok=true) rather than a wrong guess.
	if _, _, ok := majorMismatch("1.2.0", "no version here"); !ok {
		t.Error("unparseable vpk version should not be enforced")
	}
}

func TestExtractVersionAndMajor(t *testing.T) {
	if v := extractVersion("Velopack (vpk) 0.0.1298, running on .NET 8"); v != "0.0.1298" {
		t.Errorf("extractVersion = %q, want 0.0.1298", v)
	}
	if m := majorOf("2.4.6"); m != "2" {
		t.Errorf("majorOf = %q, want 2", m)
	}
	if m := majorOf("no version"); m != "" {
		t.Errorf("majorOf of no-version = %q, want empty", m)
	}
}

// TestCollectReleases classifies a representative dist/releases layout: DMGs and
// the Windows Setup.exe are attachable installers; the nupkg/json/RELEASES feed
// files are returned but not attached (vpk upload owns them).
func TestCollectReleases(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		"Halyards-osx-arm64.dmg",
		"Halyards-1.0.0-osx-arm64-full.nupkg",
		"releases.osx-arm64.json",
		"RELEASES-osx-arm64",
		"assets.osx-arm64.json",
		"Halyards-win-x64-Setup.exe",
		"Halyards-1.0.0-win-x64-full.nupkg",
		"trustedsigning.json", // intermediate, not an artifact
	}
	for _, f := range files {
		writeFile(t, filepath.Join(dir, f), "x")
	}

	arts := collectReleases(dir)
	attached := map[string]bool{}
	all := map[string]string{} // base -> kind
	for _, a := range arts {
		base := filepath.Base(a.Path)
		all[base] = a.Kind
		if a.Attach {
			attached[base] = true
		}
	}

	wantAttached := []string{"Halyards-osx-arm64.dmg", "Halyards-win-x64-Setup.exe"}
	for _, w := range wantAttached {
		if !attached[w] {
			t.Errorf("%s should be attached", w)
		}
	}
	for _, notAttached := range []string{"Halyards-1.0.0-osx-arm64-full.nupkg", "releases.osx-arm64.json", "RELEASES-osx-arm64"} {
		if _, ok := all[notAttached]; !ok {
			t.Errorf("%s should be collected", notAttached)
		}
		if attached[notAttached] {
			t.Errorf("%s should NOT be attached (vpk upload owns the feed)", notAttached)
		}
	}
	if _, ok := all["trustedsigning.json"]; ok {
		t.Error("trustedsigning.json is an intermediate, should not be an artifact")
	}
}

// --- helpers ---

func contains(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// assertHas checks that args contains flag immediately followed by value.
func assertHas(t *testing.T, args []string, flag, value string) {
	t.Helper()
	for i, a := range args {
		if a == flag {
			if i+1 < len(args) && args[i+1] == value {
				return
			}
			t.Errorf("flag %s = %q, want %q", flag, valueAfter(args, i), value)
			return
		}
	}
	t.Errorf("missing flag %s (want value %q) in %v", flag, value, args)
}

func valueAfter(args []string, i int) string {
	if i+1 < len(args) {
		return args[i+1]
	}
	return ""
}
