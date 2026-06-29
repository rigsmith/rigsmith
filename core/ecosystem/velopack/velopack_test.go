package velopack

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

func TestInfoOverlaysBases(t *testing.T) {
	info := New().Info()
	if info.ID != "velopack" {
		t.Errorf("ID = %q, want velopack", info.ID)
	}
	// Velopack overlays every base language it can package, not just dotnet.
	want := "dotnet,cargo,node,go"
	if strings.Join(info.Overlays, ",") != want {
		t.Errorf("Overlays = %v, want [%s]", info.Overlays, want)
	}
	// Publish is intentionally not advertised — a Velopack app ships via forge release.
	for _, c := range info.Capabilities {
		if c == plugin.MethodPublish {
			t.Error("velopack must not advertise Publish")
		}
	}
}

// cargoToml is a minimal [package] manifest for the cargo base in tests.
func cargoToml(name, version string) string {
	return "[package]\nname = \"" + name + "\"\nversion = \"" + version + "\"\n"
}

// TestDetectBase pins the sibling-manifest auto-detection that picks a velopack
// app's base when its config doesn't set one (and "" for a base-less dir).
func TestDetectBase(t *testing.T) {
	root := t.TempDir()
	dn := filepath.Join(root, "dn")
	writeFile(t, filepath.Join(dn, "App.csproj"), csprojPlainLib)
	rs := filepath.Join(root, "rs")
	writeFile(t, filepath.Join(rs, "Cargo.toml"), cargoToml("rustapp", "0.3.0"))
	nd := filepath.Join(root, "nd")
	writeFile(t, filepath.Join(nd, "package.json"), `{"name":"el","version":"2.1.0"}`)
	gm := filepath.Join(root, "gm")
	writeFile(t, filepath.Join(gm, "go.mod"), "module example.com/app\n\ngo 1.22\n")
	none := filepath.Join(root, "none")
	writeFile(t, filepath.Join(none, "velopack.json"), "{}")

	for _, c := range []struct{ dir, want string }{
		{dn, baseDotnet}, {rs, baseCargo}, {nd, baseNode}, {gm, baseGo}, {none, ""},
	} {
		if got := detectBase(c.dir); got != c.want {
			t.Errorf("detectBase(%s) = %q, want %q", c.dir, got, c.want)
		}
	}
}

// TestDiscoverAcrossBases verifies velopack claims apps in more than one base
// ecosystem, delegating each package's name/version to its base, and never claims
// a project that lacks a velopack file.
func TestDiscoverAcrossBases(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "dn", "App.csproj"), csprojWithVelopack)
	writeFile(t, filepath.Join(root, "dn", "velopack.json"), `{"packId":"App","channels":["win-x64"]}`)
	writeFile(t, filepath.Join(root, "rs", "Cargo.toml"), cargoToml("rustapp", "0.3.0"))
	writeFile(t, filepath.Join(root, "rs", "velopack.json"), `{"packId":"RustApp","channels":["win-x64"],"build":{"command":"true"}}`)
	writeFile(t, filepath.Join(root, "el", "package.json"), `{"name":"electronapp","version":"2.1.0"}`)
	writeFile(t, filepath.Join(root, "el", "velopack.jsonc"), `{"packId":"Electron","channels":["win-x64"],"build":{"command":"true"}}`)
	// A plain crate without a velopack file stays with the cargo adapter.
	writeFile(t, filepath.Join(root, "plainlib", "Cargo.toml"), cargoToml("plainlib", "9.9.9"))

	resp, err := New().Discover(context.Background(), plugin.DiscoverRequest{RepoRoot: root, SourcePath: "."})
	if err != nil {
		t.Fatal(err)
	}
	ver := map[string]string{}
	name := map[string]string{}
	for _, p := range resp.Packages {
		ver[p.Dir] = p.Version
		name[p.Dir] = p.Name
	}
	if len(resp.Packages) != 3 {
		t.Fatalf("discovered %d packages, want 3: %+v", len(resp.Packages), resp.Packages)
	}
	if ver["dn"] != "1.2.0" {
		t.Errorf("dotnet app version = %q, want 1.2.0", ver["dn"])
	}
	if name["rs"] != "rustapp" || ver["rs"] != "0.3.0" {
		t.Errorf("cargo app = %q@%q, want rustapp@0.3.0 (delegated to cargo)", name["rs"], ver["rs"])
	}
	if name["el"] != "electronapp" || ver["el"] != "2.1.0" {
		t.Errorf("node app = %q@%q, want electronapp@2.1.0 (delegated to node)", name["el"], ver["el"])
	}
	if _, claimed := ver["plainlib"]; claimed {
		t.Error("a crate without a velopack file must not be claimed by velopack")
	}
}

// TestSetVersionDelegatesToBase confirms a bump on a non-dotnet velopack app is
// written into the base manifest (here a Cargo.toml) by the base adapter.
func TestSetVersionDelegatesToBase(t *testing.T) {
	root := t.TempDir()
	cargoPath := filepath.Join(root, "rs", "Cargo.toml")
	writeFile(t, cargoPath, cargoToml("rustapp", "0.3.0"))
	writeFile(t, filepath.Join(root, "rs", "velopack.json"), `{"packId":"RustApp","channels":["win-x64"],"build":{"command":"true"}}`)

	err := New().SetVersion(context.Background(), plugin.SetVersionRequest{
		RepoRoot:   root,
		Package:    plugin.Package{Name: "rustapp", Dir: "rs", ManifestPath: "rs/Cargo.toml", VersionFile: "rs/Cargo.toml", Version: "0.3.0"},
		NewVersion: "0.4.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cargoPath)
	if !strings.Contains(string(b), `version = "0.4.0"`) {
		t.Errorf("delegated SetVersion did not bump Cargo.toml:\n%s", b)
	}
}

// TestRidTargets pins the RID → Go/Rust target mapping a build.command relies on.
func TestRidTargets(t *testing.T) {
	for _, c := range []struct{ rid, goos, goarch, rust string }{
		{"win-x64", "windows", "amd64", "x86_64-pc-windows-msvc"},
		{"win-arm64", "windows", "arm64", "aarch64-pc-windows-msvc"},
		{"osx-arm64", "darwin", "arm64", "aarch64-apple-darwin"},
		{"osx-x64", "darwin", "amd64", "x86_64-apple-darwin"},
		{"linux-x64", "linux", "amd64", "x86_64-unknown-linux-gnu"},
		{"freebsd-x64", "", "", ""}, // unrecognized OS → no targets
	} {
		goos, goarch := ridGo(c.rid)
		if goos != c.goos || goarch != c.goarch {
			t.Errorf("ridGo(%q) = %q/%q, want %q/%q", c.rid, goos, goarch, c.goos, c.goarch)
		}
		if got := ridRustTarget(c.rid); got != c.rust {
			t.Errorf("ridRustTarget(%q) = %q, want %q", c.rid, got, c.rust)
		}
	}
}

// TestRidBuildVars checks the variables exported into a build.command's env.
func TestRidBuildVars(t *testing.T) {
	m := map[string]string{}
	for _, v := range ridBuildVars("win-x64", "1.2.3", "/abs/out") {
		if i := strings.IndexByte(v, '='); i > 0 {
			m[v[:i]] = v[i+1:]
		}
	}
	for k, want := range map[string]string{
		"CHANNEL": "win-x64", "RID": "win-x64", "OUTPUT": "/abs/out", "VERSION": "1.2.3",
		"GOOS": "windows", "GOARCH": "amd64", "RUST_TARGET": "x86_64-pc-windows-msvc",
	} {
		if m[k] != want {
			t.Errorf("ridBuildVars[%s] = %q, want %q", k, m[k], want)
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
	if err := (Config{PackId: "X", Channels: []string{"win-x64"}, Base: "cargo"}).validate(); err != nil {
		t.Errorf("base cargo should be valid: %v", err)
	}
	if err := (Config{PackId: "X", Channels: []string{"win-x64"}, Base: "python"}).validate(); err == nil {
		t.Error("unknown base should be invalid")
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
		Windows: Windows{
			SignTemplate:   "jsign --storetype TRUSTEDSIGNING --keystore https://eus.codesigning.azure.net --storepass TOKEN --alias Acme/Acme {{file}}",
			TrustedSigning: &TrustedSigning{Endpoint: "https://eus.codesigning.azure.net", Account: "Acme", Profile: "Acme"},
		},
	}
}

func TestBuildPackArgsMacSigned(t *testing.T) {
	// macOS channel on a macOS host: native, no directive.
	got := buildPackArgs(fullCfg(), "osx-arm64", "dist/publish/osx-arm64", "dist/releases", "1.0.0", false, osMac, "")
	if got[0] != "pack" {
		t.Errorf("native macOS build must not carry a cross directive, got %q", got[0])
	}
	assertHas(t, got, "--packId", "Halyards")
	assertHas(t, got, "--mainExe", "Halyards") // no .exe on macOS
	assertHas(t, got, "--bundleId", "com.acme.halyards")
	assertHas(t, got, "--signAppIdentity", "Developer ID Application: Acme")
	assertHas(t, got, "--notaryProfile", "halyards-notary")
	assertHas(t, got, "--channel", "osx-arm64")
	assertHas(t, got, "--icon", "app/halyards.icns")
	if contains(got, "--azureTrustedSignFile") || contains(got, "--signTemplate") {
		t.Error("macOS pack must not carry a Windows signing flag")
	}
}

func TestBuildPackArgsMacSnapshotIsUnsigned(t *testing.T) {
	got := buildPackArgs(fullCfg(), "osx-arm64", "p", "o", "1.0.0", true, osMac, "")
	if contains(got, "--signAppIdentity") || contains(got, "--notaryProfile") {
		t.Errorf("snapshot pack must be unsigned, got %v", got)
	}
	// Identity-independent flags still present.
	assertHas(t, got, "--bundleId", "com.acme.halyards")
}

// TestBuildPackArgsWindowsNativeAzure: a Windows channel built ON Windows uses
// vpk's native Azure Trusted Signing and no cross directive.
func TestBuildPackArgsWindowsNativeAzure(t *testing.T) {
	got := buildPackArgs(fullCfg(), "win-x64", "dist/publish/win-x64", "dist/releases", "1.0.0", false, osWindows, "dist/trustedsigning.json")
	if got[0] != "pack" {
		t.Errorf("native Windows build must not carry a cross directive, got %q", got[0])
	}
	assertHas(t, got, "--mainExe", "Halyards.exe") // .exe appended on Windows
	assertHas(t, got, "--icon", "app/halyards.ico")
	assertHas(t, got, "--azureTrustedSignFile", "dist/trustedsigning.json")
	if contains(got, "--signTemplate") {
		t.Error("native Windows must use --azureTrustedSignFile, not --signTemplate")
	}
	if contains(got, "--bundleId") || contains(got, "--signAppIdentity") {
		t.Error("Windows pack must not carry macOS flags")
	}
}

// TestBuildPackArgsWindowsCrossSignTemplate: a Windows channel cross-compiled from
// macOS gets the [win] directive and signs via --signTemplate (jsign) — vpk's
// --azureTrustedSignFile isn't available there.
func TestBuildPackArgsWindowsCrossSignTemplate(t *testing.T) {
	got := buildPackArgs(fullCfg(), "win-x64", "dist/publish/win-x64", "dist/releases", "1.0.0", false, osMac, "")
	if got[0] != "[win]" {
		t.Errorf("cross-compiled Windows build must start with the [win] directive, got %q", got[0])
	}
	assertHas(t, got, "--mainExe", "Halyards.exe")
	assertHas(t, got, "--signTemplate", fullCfg().Windows.SignTemplate)
	assertHas(t, got, "--signExclude", `\.dll$`)
	if contains(got, "--azureTrustedSignFile") {
		t.Error("cross-compiled Windows must not use the Windows-host-only --azureTrustedSignFile")
	}
	// vpk's {{file}} placeholder survives untouched.
	if !contains(got, fullCfg().Windows.SignTemplate) || !strings.Contains(fullCfg().Windows.SignTemplate, "{{file}}") {
		t.Error("--signTemplate must pass through verbatim, including {{file}}")
	}
}

func TestBuildPackArgsWindowsSnapshotNoSigning(t *testing.T) {
	got := buildPackArgs(fullCfg(), "win-x64", "p", "o", "1.0.0", true, osMac, "")
	if contains(got, "--azureTrustedSignFile") || contains(got, "--signTemplate") {
		t.Errorf("snapshot Windows pack must be unsigned, got %v", got)
	}
	// But the cross directive is about target platform, not signing — still present.
	if got[0] != "[win]" {
		t.Errorf("cross snapshot still needs the [win] directive, got %q", got[0])
	}
}

func TestCrossDirective(t *testing.T) {
	cases := []struct {
		ch   string
		host targetOS
		want string
	}{
		{"win-x64", osMac, "[win]"},   // cross from macOS
		{"win-x64", osLinux, "[win]"}, // cross from Linux
		{"win-x64", osWindows, ""},    // native Windows
		{"osx-arm64", osMac, ""},      // native macOS
		{"osx-arm64", osWindows, "[osx]"},
		{"linux-x64", osMac, "[linux]"}, // cross from macOS
		{"linux-x64", osLinux, ""},      // native Linux
	}
	for _, c := range cases {
		if got := crossDirective(c.ch, c.host); got != c.want {
			t.Errorf("crossDirective(%q, %v) = %q, want %q", c.ch, c.host, got, c.want)
		}
	}
}

func TestExpandEnv(t *testing.T) {
	env := []string{"AZURE_CODESIGN_TOKEN=tok123", "FOO=bar", "AZURE_CODESIGN_TOKEN=tok456"} // dup → last wins
	cases := []struct{ in, want string }{
		{"jsign --storepass $AZURE_CODESIGN_TOKEN {{file}}", "jsign --storepass tok456 {{file}}"},
		{"a ${FOO} b", "a bar b"},
		{"no vars here {{file}}", "no vars here {{file}}"}, // {{file}} is vpk's, untouched
		{"$UNSET tail", " tail"},                           // unset → empty
		{"", ""},
	}
	for _, c := range cases {
		if got := expandEnv(c.in, env); got != c.want {
			t.Errorf("expandEnv(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRedactCommandStorepass(t *testing.T) {
	args := []string{"[win]", "pack", "--signTemplate", "jsign --storepass SUPERSECRET --alias a/b {{file}}", "--signExclude", `\.dll$`}
	got := redactCommand(args)
	if strings.Contains(got, "SUPERSECRET") {
		t.Errorf("storepass token leaked in echoed command: %s", got)
	}
	if !strings.Contains(got, "--storepass ***") {
		t.Errorf("storepass not redacted to ***: %s", got)
	}
	if !strings.Contains(got, "{{file}}") {
		t.Errorf("redaction must not touch {{file}}: %s", got)
	}
	// `--storepass=VALUE` form is redacted too.
	if g := redactCommand([]string{"x --storepass=abc123 y"}); strings.Contains(g, "abc123") {
		t.Errorf("--storepass=VALUE not redacted: %s", g)
	}
}

func TestBuildPackArgsOmitsEmptyOptionalFlags(t *testing.T) {
	min := Config{PackId: "App", PackTitle: "App", MainExe: "App", Channels: []string{"osx-arm64"}, Output: "out"}
	got := buildPackArgs(min, "osx-arm64", "p", "out", "1.0.0", false, osMac, "")
	for _, f := range []string{"--packAuthors", "--icon", "--bundleId", "--signAppIdentity", "--notaryProfile"} {
		if contains(got, f) {
			t.Errorf("unset optional flag %s should be omitted, got %v", f, got)
		}
	}
}

func TestVpkVersionFromHelpBanner(t *testing.T) {
	// The real `vpk --help` banner (vpk has no --version flag).
	const help = "Description:\n  Velopack CLI 1.2.0, for distributing applications.\n\nUsage:\n  vpk [command] [options]\n"
	if got := vpkVersion(help); got != "1.2.0" {
		t.Errorf("vpkVersion(banner) = %q, want 1.2.0", got)
	}
	// Fallback to the first dotted version when the banner phrasing changes.
	if got := vpkVersion("some tool 3.4.5 build"); got != "3.4.5" {
		t.Errorf("vpkVersion(fallback) = %q, want 3.4.5", got)
	}
}

func TestSameMajor(t *testing.T) {
	if !sameMajor("1.2.0", "1.2.0") {
		t.Error("1 == 1 should be same major")
	}
	if sameMajor("1.2.0", "2.0.5") {
		t.Error("1 vs 2 should differ")
	}
	// Unparseable side → not enforced (true) rather than a wrong guess.
	if !sameMajor("1.2.0", "no version here") {
		t.Error("unparseable vpk version should not be enforced")
	}
}

func TestExtractVersionAndMajor(t *testing.T) {
	if v := extractVersion("Velopack CLI 1.2.0, for distributing applications."); v != "1.2.0" {
		t.Errorf("extractVersion = %q, want 1.2.0", v)
	}
	if m := majorOf("2.4.6"); m != "2" {
		t.Errorf("majorOf = %q, want 2", m)
	}
	if m := majorOf("no version"); m != "" {
		t.Errorf("majorOf of no-version = %q, want empty", m)
	}
}

// TestCollectReleases classifies a representative dist/releases layout. Installers
// (DMG, Setup.exe) AND the update feed the in-app updater needs — the
// releases.<ch>.json index plus the full/delta .nupkg payloads — are attached to
// the release; the legacy RELEASES-<ch> and the assets.<ch>.json build manifest
// are collected but not attached.
func TestCollectReleases(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		"Halyards-osx-arm64.dmg",
		"Halyards-1.0.0-osx-arm64-full.nupkg",
		"Halyards-1.0.0-osx-arm64-delta.nupkg",
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

	// Installers + the runtime update feed (index + full/delta payloads) all attach.
	wantAttached := []string{
		"Halyards-osx-arm64.dmg",
		"Halyards-win-x64-Setup.exe",
		"releases.osx-arm64.json",
		"Halyards-1.0.0-osx-arm64-full.nupkg",
		"Halyards-1.0.0-osx-arm64-delta.nupkg",
		"Halyards-1.0.0-win-x64-full.nupkg",
	}
	for _, w := range wantAttached {
		if !attached[w] {
			t.Errorf("%s should be attached (installer or update-feed asset)", w)
		}
	}
	// Legacy/build-manifest files are collected but not attached.
	for _, notAttached := range []string{"RELEASES-osx-arm64", "assets.osx-arm64.json"} {
		if _, ok := all[notAttached]; !ok {
			t.Errorf("%s should be collected", notAttached)
		}
		if attached[notAttached] {
			t.Errorf("%s should NOT be attached (unused by the modern updater)", notAttached)
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
