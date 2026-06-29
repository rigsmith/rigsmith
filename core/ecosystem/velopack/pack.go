package velopack

import (
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/core/plugin"
)

// vpk rejects a packVersion below 0.0.1; 0.0.0 is rigsmith's "nothing released
// yet" manifest sentinel (the version step bumps it from a changeset).
const (
	zeroVersion   = "0.0.0"
	minVpkVersion = "0.0.1"
)

// Artifacts builds the Velopack installers and update feeds for every configured
// channel and returns them so the release step can attach/upload them. For each
// channel (a .NET RID) it produces the directory of built binaries — a dotnet
// `dotnet publish --self-contained`, or the configured build.command for other
// bases — then runs `vpk pack` with the per-OS signing flags, and on macOS wraps
// the notarized .app in a .dmg. Outputs land in the configured output dir (default
// dist/releases).
//
// Snapshot builds everything unsigned (a fast local rehearsal); DryRun reports
// the plan without building. macOS channels are skipped on a non-macOS host
// (signing/notarization/DMG need macOS tooling); Windows/Linux channels build on
// any host.
func (a *Adapter) Artifacts(ctx context.Context, req plugin.ArtifactsRequest) (plugin.ArtifactsResponse, error) {
	pkgDir := filepath.Join(req.RepoRoot, req.Package.Dir)
	cfg, err := loadConfig(pkgDir)
	if err != nil {
		return plugin.ArtifactsResponse{}, err
	}

	ver, err := resolvePackVersion(req.Package.Version, req.Snapshot)
	if err != nil {
		return plugin.ArtifactsResponse{}, err
	}
	releasesRel := cfg.Output
	releasesAbs := filepath.Join(req.RepoRoot, releasesRel)
	host := hostOS()

	// Which channels can build on this host. macOS channels need a macOS host.
	var planned, skipped []string
	for _, ch := range cfg.Channels {
		if buildableOn(osOf(ch), host) {
			planned = append(planned, ch)
		} else {
			skipped = append(skipped, ch)
		}
	}

	if req.DryRun {
		msg := fmt.Sprintf("dry-run: would vpk pack %s@%s for [%s]%s%s",
			cfg.PackId, ver, strings.Join(planned, ", "), snapshotSuffix(req.Snapshot), skipNote(skipped))
		return plugin.ArtifactsResponse{Message: msg}, nil
	}
	if len(planned) == 0 {
		return plugin.ArtifactsResponse{Skipped: true, Message: "no Velopack channels buildable on this host" + skipNote(skipped)}, nil
	}

	// Preflight: the `vpk` CLI must be present and (when the csproj pins Velopack)
	// share its major version, since pack flags shift across majors.
	if err := a.checkVpkMajor(ctx, req); err != nil {
		return plugin.ArtifactsResponse{}, err
	}

	env := mergeSigningEnv(req.BaseEnv(), req.Signing)

	// The base ecosystem (csproj/Cargo.toml/package.json/go.mod next to the velopack
	// file) decides how each channel's binaries are produced before vpk packs them.
	base, err := a.resolveBase(pkgDir)
	if err != nil {
		return plugin.ArtifactsResponse{}, err
	}
	baseID := base.Info().ID

	for _, ch := range planned {
		pubRel, err := a.producePackDir(ctx, req, cfg, baseID, ch, ver, env)
		if err != nil {
			return plugin.ArtifactsResponse{}, err
		}

		// Native-Windows Azure Trusted Signing wants a JSON descriptor file; write it
		// next to the output (best-effort cleanup leaves it — it carries no secrets).
		// Cross-compiled Windows signs via --signTemplate instead, so no file there.
		azureFile := ""
		if osOf(ch) == osWindows && host == osWindows && cfg.Windows.TrustedSigning != nil && !req.Snapshot {
			azureFile = filepath.Join("dist", "trustedsigning.json")
			if err := writeTrustedSigningFile(filepath.Join(req.RepoRoot, azureFile), *cfg.Windows.TrustedSigning); err != nil {
				return plugin.ArtifactsResponse{}, err
			}
		}

		// Cross-compiled Windows signs via --signTemplate. Resolve it now: an
		// explicit signTemplate (with $VARs expanded from the build env), or one
		// synthesized from windows.trustedSigning (jsign + a minted Azure token) so
		// the same trustedSigning block works on a Mac/Linux host too. Skipped for a
		// snapshot (unsigned rehearsal) to avoid an unnecessary token mint.
		packCfg := cfg
		if osOf(ch) == osWindows && host != osWindows && !req.Snapshot {
			tmpl, err := resolveCrossWindowsSignTemplate(cfg.Windows, env)
			if err != nil {
				return plugin.ArtifactsResponse{}, err
			}
			packCfg.Windows.SignTemplate = tmpl
		}

		// Re-packing the same version is idempotent: drop this version's existing
		// nupkg(s) for the channel first, or vpk rejects the pack ("a release equal
		// or greater already exists"). Only this version+channel is cleared — prior
		// versions stay (their nupkgs feed delta generation, and vpk rebuilds the
		// channel manifest from what remains), so `--from build` resumes cleanly
		// after a partial failure and a local build can re-run the same version.
		if err := clearChannelVersion(filepath.Join(req.RepoRoot, releasesRel), cfg.PackId, ver, ch); err != nil {
			return plugin.ArtifactsResponse{}, err
		}

		packArgs := buildPackArgs(packCfg, ch, pubRel, releasesRel, ver, req.Snapshot, host, azureFile)
		if _, _, err := runCmdEnv(ctx, req.RepoRoot, env, "vpk", packArgs...); err != nil {
			return plugin.ArtifactsResponse{}, fmt.Errorf("vpk pack %s: %w", ch, err)
		}

		if osOf(ch) == osMac && cfg.Macos.WrapDmg() {
			if err := a.wrapDmg(ctx, req.RepoRoot, env, cfg, ch, releasesRel, req.Snapshot); err != nil {
				return plugin.ArtifactsResponse{}, fmt.Errorf("dmg %s: %w", ch, err)
			}
		}
	}

	arts := collectReleases(releasesAbs)
	return plugin.ArtifactsResponse{
		Built:     true,
		Artifacts: arts,
		Message: fmt.Sprintf("packed %d artifact(s) for %s@%s across [%s]%s%s",
			len(arts), cfg.PackId, ver, strings.Join(planned, ", "), snapshotSuffix(req.Snapshot), skipNote(skipped)),
	}, nil
}

// buildPublishArgs is the `dotnet publish` argv for one RID: a self-contained,
// non-single-file build (single-file would bundle the dylibs out of reach of
// codesign) into pubDir.
func buildPublishArgs(csproj, rid, pubDir string) []string {
	return []string{
		"publish", csproj,
		"-c", "Release",
		"-r", rid,
		"--self-contained",
		"-p:PublishSingleFile=false",
		"-o", pubDir,
		"--nologo",
	}
}

// producePackDir builds the application for one channel into a directory `vpk
// pack` can wrap and returns that directory (repo-root-relative; commands run with
// cwd = RepoRoot). The dotnet base builds automatically via `dotnet publish`; an
// explicit build.command (required for cargo/node/go, optional as a dotnet
// override) is run through the platform shell with the channel's build variables
// set and must fill $OUTPUT — vpk then packs that (or cfg.Build.PackDir).
func (a *Adapter) producePackDir(ctx context.Context, req plugin.ArtifactsRequest, cfg Config, baseID, ch, version string, env []string) (string, error) {
	pubRel := filepath.Join("dist", "publish", ch)

	if cfg.Build != nil && strings.TrimSpace(cfg.Build.Command) != "" {
		outAbs := filepath.Join(req.RepoRoot, pubRel)
		if err := os.MkdirAll(outAbs, 0o755); err != nil {
			return "", err
		}
		cmdEnv := append(append([]string{}, env...), ridBuildVars(ch, version, outAbs)...)
		shell, flag := shellFor()
		if _, _, err := runCmdEnv(ctx, req.RepoRoot, cmdEnv, shell, flag, cfg.Build.Command); err != nil {
			return "", fmt.Errorf("velopack: build command for %s: %w", ch, err)
		}
		if pd := strings.TrimSpace(cfg.Build.PackDir); pd != "" {
			return expandEnv(pd, cmdEnv), nil // honors $CHANNEL/$OUTPUT/... in the path
		}
		return pubRel, nil
	}

	if baseID == baseDotnet {
		if _, _, err := runCmdEnv(ctx, req.RepoRoot, env, "dotnet", buildPublishArgs(req.Package.ManifestPath, ch, pubRel)...); err != nil {
			return "", fmt.Errorf("dotnet publish %s: %w", ch, err)
		}
		return pubRel, nil
	}

	return "", fmt.Errorf("velopack: the %q base has no built-in build — set build.command in the velopack file to produce the pack directory for %s", baseID, ch)
}

// shellFor returns the platform shell and its "run this string" flag, used to run
// a build.command the way a user would type it (pipelines, &&, env refs).
func shellFor() (name, flag string) {
	if runtime.GOOS == "windows" {
		return "cmd", "/c"
	}
	return "sh", "-c"
}

// ridBuildVars are the variables exported into a build.command's environment for
// one channel: the RID, the output dir it must fill, the version, and the derived
// Go/Rust targets so a `go build` or `cargo build --target` needs no RID parsing.
func ridBuildVars(rid, version, outputAbs string) []string {
	vars := []string{
		"CHANNEL=" + rid,
		"RID=" + rid,
		"OUTPUT=" + outputAbs,
		"VERSION=" + version,
	}
	if goos, goarch := ridGo(rid); goos != "" {
		vars = append(vars, "GOOS="+goos, "GOARCH="+goarch)
	}
	if t := ridRustTarget(rid); t != "" {
		vars = append(vars, "RUST_TARGET="+t)
	}
	return vars
}

// ridArch returns the architecture component of a RID ("win-x64" -> "x64").
func ridArch(rid string) string {
	if i := strings.IndexByte(rid, '-'); i >= 0 {
		return rid[i+1:]
	}
	return ""
}

// ridGo maps a RID to its Go GOOS/GOARCH (empty when unrecognized).
func ridGo(rid string) (goos, goarch string) {
	switch osOf(rid) {
	case osWindows:
		goos = "windows"
	case osMac:
		goos = "darwin"
	case osLinux:
		goos = "linux"
	}
	switch ridArch(rid) {
	case "x64":
		goarch = "amd64"
	case "x86":
		goarch = "386"
	case "arm64":
		goarch = "arm64"
	case "arm":
		goarch = "arm"
	}
	if goos == "" || goarch == "" {
		return "", ""
	}
	return goos, goarch
}

// ridRustTarget maps a RID to its Rust target triple (empty when unrecognized).
func ridRustTarget(rid string) string {
	arch := map[string]string{"x64": "x86_64", "arm64": "aarch64", "x86": "i686"}[ridArch(rid)]
	if arch == "" {
		return ""
	}
	switch osOf(rid) {
	case osWindows:
		return arch + "-pc-windows-msvc"
	case osMac:
		return arch + "-apple-darwin"
	case osLinux:
		return arch + "-unknown-linux-gnu"
	default:
		return ""
	}
}

// buildPackArgs is the `vpk pack` argv for one channel, built for the given host.
// When the channel targets a different OS than the host it is prefixed with vpk's
// cross-compile directive (`[win]` / `[osx]` / `[linux]`); a native build omits it.
// It dispatches per OS: macOS gets --bundleId and, unless snapshot, the Developer
// ID identity + notary profile; Windows gets the .exe mainExe and, unless snapshot,
// the host-appropriate code-signing flags; Linux gets the common flags only. Empty
// optional values are omitted so a partially-configured velopack.json still
// produces a valid command.
func buildPackArgs(cfg Config, ch, pubDir, output, version string, snapshot bool, host targetOS, azureFile string) []string {
	mainExe := cfg.MainExe
	if osOf(ch) == osWindows {
		mainExe += ".exe"
	}
	var args []string
	if d := crossDirective(ch, host); d != "" {
		args = append(args, d) // cross-compile target directive, e.g. "[win]"
	}
	args = append(args,
		"pack",
		"--packId", cfg.PackId,
		"--packVersion", version,
		"--packDir", pubDir,
		"--mainExe", mainExe,
		"--packTitle", cfg.PackTitle,
	)
	args = appendFlag(args, "--packAuthors", cfg.PackAuthors)

	switch osOf(ch) {
	case osMac:
		args = appendFlag(args, "--bundleId", cfg.Macos.BundleId)
		args = appendFlag(args, "--icon", cfg.Icon.Macos)
		if !snapshot {
			args = appendFlag(args, "--signAppIdentity", cfg.Macos.SignIdentity)
			args = appendFlag(args, "--notaryProfile", cfg.Macos.NotaryProfile)
		}
	case osWindows:
		args = appendFlag(args, "--icon", cfg.Icon.Windows)
		if !snapshot {
			args = appendWindowsSigning(args, cfg, host, azureFile)
		}
	}

	args = append(args, "--channel", ch, "-r", ch, "-o", output)
	return args
}

// crossDirective returns vpk's target-platform directive for a channel built on
// host — "[win]" / "[osx]" / "[linux]" when cross-compiling (target OS ≠ host),
// or "" for a native build (vpk defaults to the host platform).
func crossDirective(ch string, host targetOS) string {
	t := osOf(ch)
	if t == host {
		return ""
	}
	switch t {
	case osWindows:
		return "[win]"
	case osMac:
		return "[osx]"
	case osLinux:
		return "[linux]"
	default:
		return ""
	}
}

// appendWindowsSigning adds the host-appropriate Windows code-signing flags. On a
// Windows host it uses vpk's native Azure Trusted Signing (--azureTrustedSignFile,
// from the metadata file the caller wrote). Cross-compiling from macOS/Linux —
// where that flag isn't available — it uses the configured --signTemplate (jsign)
// plus --signExclude '\.dll$' so only the .exe / Setup.exe are signed, not the
// bundled runtime DLLs. Each is a no-op when its config is absent.
func appendWindowsSigning(args []string, cfg Config, host targetOS, azureFile string) []string {
	if host == osWindows {
		return appendFlag(args, "--azureTrustedSignFile", azureFile)
	}
	if tmpl := strings.TrimSpace(cfg.Windows.SignTemplate); tmpl != "" {
		args = append(args, "--signTemplate", tmpl, "--signExclude", `\.dll$`)
	}
	return args
}

// expandEnv expands $VAR / ${VAR} in s from the given KEY=VALUE environment. The
// signTemplate is handed to vpk, which runs it WITHOUT a shell, so a token
// reference like $AZURE_CODESIGN_TOKEN must be expanded here — from the build env
// shiprig passes in (the layered .env/.env.local + ambient) — or it would reach
// the signer as the literal string. Unset variables expand to "".
func expandEnv(s string, env []string) string {
	if !strings.ContainsRune(s, '$') {
		return s
	}
	m := envMap(env)
	return os.Expand(s, func(k string) string { return m[k] })
}

// envMap turns a KEY=VALUE slice into a map (later entries win, ambient over .env).
func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, e := range env {
		if i := strings.IndexByte(e, '='); i > 0 {
			m[e[:i]] = e[i+1:]
		}
	}
	return m
}

// resolveCrossWindowsSignTemplate returns the `--signTemplate` command for a
// Windows build cross-compiled from a non-Windows host. An explicit
// Windows.SignTemplate wins (with $VARs expanded from the build env). Otherwise,
// when Windows.TrustedSigning is set, it synthesizes a jsign + Azure Trusted
// Signing command — minting the access token from the build env — so the same
// trustedSigning block that drives the native-Windows path works here too, with no
// hand-written template or pre-exported token. Empty when neither is configured.
func resolveCrossWindowsSignTemplate(win Windows, env []string) (string, error) {
	if t := strings.TrimSpace(win.SignTemplate); t != "" {
		return expandEnv(t, env), nil
	}
	if win.TrustedSigning == nil {
		return "", nil
	}
	ts := *win.TrustedSigning
	token, err := trustedSigningToken(envMap(env))
	if err != nil {
		return "", err
	}
	// jsign signs each PE in turn ({{file}}); --tsmode RFC3161 + the Azure TSA are
	// required because Trusted Signing certs are short-lived (an un-timestamped
	// signature expires within days) and jsign's default timestamp mode misparses
	// the RFC3161 response. The adapter adds --signExclude '\.dll$' separately, and
	// the --storepass token is redacted from any echoed command.
	return fmt.Sprintf(
		"jsign --storetype TRUSTEDSIGNING --keystore %s --storepass %s --alias %s/%s "+
			"--tsmode RFC3161 --tsaurl http://timestamp.acs.microsoft.com {{file}}",
		ts.Endpoint, token, ts.Account, ts.Profile), nil
}

// trustedSigningToken returns an Azure Trusted Signing access token: a pre-set
// AZURE_CODESIGN_TOKEN if present (CI / explicit), otherwise one minted from the
// service-principal creds (AZURE_TENANT_ID/CLIENT_ID/CLIENT_SECRET) in the build
// env. It names the missing creds rather than letting the signer fail opaquely on
// an empty token.
func trustedSigningToken(env map[string]string) (string, error) {
	if t := strings.TrimSpace(env["AZURE_CODESIGN_TOKEN"]); t != "" {
		return t, nil
	}
	tenant, clientID, secret := env["AZURE_TENANT_ID"], env["AZURE_CLIENT_ID"], env["AZURE_CLIENT_SECRET"]
	var missing []string
	for _, kv := range []struct{ k, v string }{
		{"AZURE_TENANT_ID", tenant}, {"AZURE_CLIENT_ID", clientID}, {"AZURE_CLIENT_SECRET", secret},
	} {
		if strings.TrimSpace(kv.v) == "" {
			missing = append(missing, kv.k)
		}
	}
	if len(missing) > 0 {
		return "", fmt.Errorf(
			"velopack: Windows Trusted Signing needs an access token — set AZURE_CODESIGN_TOKEN, or the service-principal creds (%s) in the signing env / .env.local so it can be minted",
			strings.Join(missing, ", "))
	}
	return mintAzureCodeSigningToken(tenant, clientID, secret)
}

// mintAzureCodeSigningToken exchanges service-principal client credentials for a
// Trusted Signing access token (the codesigning.azure.net audience).
func mintAzureCodeSigningToken(tenant, clientID, secret string) (string, error) {
	form := url.Values{
		"client_id":     {clientID},
		"client_secret": {secret},
		"grant_type":    {"client_credentials"},
		"scope":         {"https://codesigning.azure.net/.default"},
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.PostForm("https://login.microsoftonline.com/"+tenant+"/oauth2/v2.0/token", form)
	if err != nil {
		return "", fmt.Errorf("velopack: minting Trusted Signing token: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("velopack: Trusted Signing token request failed (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.AccessToken == "" {
		return "", fmt.Errorf("velopack: Trusted Signing token response had no access_token")
	}
	return out.AccessToken, nil
}

// resolvePackVersion validates the version vpk will pack. vpk rejects anything
// below 0.0.1, and 0.0.0 is rigsmith's "nothing released yet" sentinel — so a
// snapshot (unsigned rehearsal) falls back to the minimum, while a real build
// fails with guidance instead of vpk's terse rejection deep in the build.
func resolvePackVersion(ver string, snapshot bool) (string, error) {
	if ver != zeroVersion {
		return ver, nil
	}
	if snapshot {
		return minVpkVersion, nil
	}
	return "", fmt.Errorf(
		"velopack: package version is %s — run the `version` step (consume a changeset) or set <Version> >= %s before building real artifacts (use --dry-build for an unsigned rehearsal)",
		zeroVersion, minVpkVersion)
}

// wrapDmg turns Velopack's portable .app (shipped in <PackId>-<ch>-Portable.zip)
// into a distributable .dmg: extract preserving the code signature, staple the
// notarization ticket, hdiutil into a compressed DMG, then (when signing) sign the
// DMG. Stapling and DMG-signing are best-effort (mirroring the original pack.sh),
// so an unsigned/un-notarized rehearsal still yields a DMG. Finally it removes the
// portable zip and the .pkg Velopack also emits, leaving the .dmg + update feed.
func (a *Adapter) wrapDmg(ctx context.Context, repoRoot string, env []string, cfg Config, ch, output string, snapshot bool) error {
	outAbs := filepath.Join(repoRoot, output)
	portable := filepath.Join(outAbs, fmt.Sprintf("%s-%s-Portable.zip", cfg.PackId, ch))
	if _, err := os.Stat(portable); err != nil {
		// No portable zip means vpk did not emit one (older/newer layout) — nothing
		// to wrap; leave the feed as-is rather than fail the whole build.
		return nil
	}

	tmp, err := os.MkdirTemp("", "velopack-dmg-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	// Stage the DMG window contents: the .app next to an /Applications symlink the
	// user drags onto — the standard macOS "drag to install" target. The old layout
	// shipped the bare .app with no drop target.
	stage := filepath.Join(tmp, "stage")
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return err
	}
	if _, _, err := runCmdEnv(ctx, repoRoot, env, "ditto", "-x", "-k", portable, stage); err != nil {
		return fmt.Errorf("ditto extract: %w", err)
	}
	appPath := filepath.Join(stage, cfg.MainExe+".app")
	_, _, _ = runCmdEnv(ctx, repoRoot, env, "xcrun", "stapler", "staple", appPath) // best-effort
	if err := os.Symlink("/Applications", filepath.Join(stage, "Applications")); err != nil {
		return fmt.Errorf("applications symlink: %w", err)
	}

	dmg := filepath.Join(outAbs, fmt.Sprintf("%s-%s.dmg", cfg.PackId, ch))
	if err := buildDmg(ctx, repoRoot, env, cfg.PackTitle, cfg.MainExe, stage, dmg, dmgLayoutFor(repoRoot, cfg.Macos)); err != nil {
		return err
	}
	if !snapshot && cfg.Macos.SignIdentity != "" {
		_, _, _ = runCmdEnv(ctx, repoRoot, env, "codesign", "--force", "--timestamp", "--sign", cfg.Macos.SignIdentity, dmg) // best-effort
	}

	_ = os.Remove(portable)
	_ = os.Remove(filepath.Join(outAbs, fmt.Sprintf("%s-%s-Setup.pkg", cfg.PackId, ch)))
	return nil
}

// dmgLayout describes the install-window: its content size (logical points) and
// a background image, supplied either as a file path (a config override) or as
// embedded bytes (the built-in default).
type dmgLayout struct {
	bgAbs         string // override image path from macos.dmgBackground
	bgBytes       []byte // embedded default, used when there is no override
	bgExt         string // extension of the chosen image (".tiff"/".png")
	width, height int
}

// dmgLayoutFor resolves the window size + background from the macOS config. An
// explicit macos.dmgBackground overrides the built-in default; its window size
// comes from DmgWindow, else the image's own pixel size (PNG only). With no
// override, the embedded default backdrop is used at its native 640×400.
func dmgLayoutFor(repoRoot string, m Macos) dmgLayout {
	if m.DmgBackground == "" {
		return dmgLayout{bgBytes: defaultDmgBackground, bgExt: ".tiff", width: defaultDmgWidth, height: defaultDmgHeight}
	}
	lay := dmgLayout{
		bgAbs:  filepath.Join(repoRoot, m.DmgBackground),
		bgExt:  filepath.Ext(m.DmgBackground),
		width:  defaultDmgWidth,
		height: defaultDmgHeight,
	}
	switch {
	case m.DmgWindow != nil && m.DmgWindow.Width > 0 && m.DmgWindow.Height > 0:
		lay.width, lay.height = m.DmgWindow.Width, m.DmgWindow.Height
	default:
		if w, h, err := pngSize(lay.bgAbs); err == nil {
			lay.width, lay.height = w, h
		}
	}
	return lay
}

// buildDmg packs the staging folder (the .app + an Applications symlink) into a
// compressed DMG at dmg. It first tries to lay the window out in icon view — the
// app on the left, the Applications folder on the right, over the optional
// background — so the "drag to install" gesture is obvious. That layout uses
// Finder scripting (needs a GUI session), so it is best-effort: any failure falls
// back to a plain compressed DMG that still carries the app and the Applications
// drop target, just in the default view.
func buildDmg(ctx context.Context, repoRoot string, env []string, volName, mainExe, stage, dmg string, lay dmgLayout) error {
	if err := layoutDmg(ctx, repoRoot, env, volName, mainExe, stage, dmg, lay); err == nil {
		return nil
	}
	if _, _, err := runCmdEnv(ctx, repoRoot, env, "hdiutil", "create",
		"-volname", volName, "-srcfolder", stage, "-ov", "-format", "UDZO", dmg); err != nil {
		return fmt.Errorf("hdiutil create: %w", err)
	}
	return nil
}

// layoutDmg builds a DMG with an icon-view window arranged for drag-to-install:
// create a read-write image, mount it, (optionally) install a background picture,
// position the .app and the Applications symlink via Finder, then flush + detach
// cleanly and convert to a compressed read-only image. Any step failing returns
// an error so buildDmg can fall back to a plain DMG.
func layoutDmg(ctx context.Context, repoRoot string, env []string, volName, mainExe, stage, dmg string, lay dmgLayout) error {
	rw := dmg + ".rw.dmg"
	defer os.Remove(rw)
	if _, _, err := runCmdEnv(ctx, repoRoot, env, "hdiutil", "create",
		"-volname", volName, "-srcfolder", stage, "-fs", "HFS+", "-format", "UDRW", "-ov", rw); err != nil {
		return err
	}
	out, _, err := runCmdEnv(ctx, repoRoot, env, "hdiutil", "attach", rw, "-nobrowse", "-readwrite", "-noverify")
	if err != nil {
		return err
	}
	mount := dmgMountPoint(out)
	if mount == "" {
		return fmt.Errorf("velopack: could not parse DMG mount point from hdiutil output")
	}
	// Target the volume by the name it ACTUALLY mounted under: if "Halyards" was
	// already taken (a prior copy of the dmg still open), macOS mounts this one as
	// "Halyards 1" etc., and a hardcoded name would script the wrong window — or
	// none — leaving the build's dmg unarranged. This was the cause of the layout
	// "reverting" to a default icon view.
	disk := filepath.Base(mount)
	detached := false
	defer func() {
		if !detached {
			_, _, _ = runCmdEnv(ctx, repoRoot, env, "hdiutil", "detach", mount, "-force")
		}
	}()

	bgClause := ""
	if lay.bgAbs != "" || len(lay.bgBytes) > 0 {
		bgDir := filepath.Join(mount, ".background")
		if err := os.MkdirAll(bgDir, 0o755); err != nil {
			return err
		}
		ext := lay.bgExt
		if ext == "" {
			ext = ".png"
		}
		bgName := "background" + ext
		dst := filepath.Join(bgDir, bgName)
		if lay.bgAbs != "" {
			if err := copyFile(lay.bgAbs, dst); err != nil {
				return err
			}
		} else if err := os.WriteFile(dst, lay.bgBytes, 0o644); err != nil {
			return err
		}
		// Hide the .background folder: Finder gives .DS_Store the hidden flag
		// automatically but not a folder we create, so without this it shows up in
		// the install window (a dotfile alone isn't hidden on recent Finder).
		_, _, _ = runCmdEnv(ctx, repoRoot, env, "chflags", "hidden", bgDir)
		bgClause = "\n    set background picture of vo to file \".background:" + bgName + "\""
	}

	w, h := lay.width, lay.height
	if w <= 0 || h <= 0 {
		w, h = 600, 400
	}
	appX, appsX, iconY := w/4, w-w/4, h/2

	// `activate` (bring Finder to the front) is required for icon positions to
	// actually commit to .DS_Store under automation — without it the layout renders
	// but the positions are dropped, so the window reverts to a default grid.
	script := fmt.Sprintf(`tell application "Finder"
  activate
  tell disk %q
    open
    delay 1
    set current view of container window to icon view
    set toolbar visible of container window to false
    set statusbar visible of container window to false
    set the bounds of container window to {200, 120, %d, %d}
    set vo to the icon view options of container window
    set arrangement of vo to not arranged
    set icon size of vo to 128%s
    set position of item %q of container window to {%d, %d}
    set position of item "Applications" of container window to {%d, %d}
    update without registering applications
    delay 2
    close
  end tell
end tell`, disk, 200+w, 120+h, bgClause, mainExe+".app", appX, iconY, appsX, iconY)
	if _, _, err := runCmdEnv(ctx, repoRoot, env, "osascript", "-e", script); err != nil {
		return err
	}

	// Finder writes .DS_Store asynchronously after closing the window — wait for it
	// to land, then flush and unmount CLEANLY (a forced detach can skip the flush,
	// dropping the layout from the converted image). Only then convert.
	time.Sleep(2 * time.Second)
	_, _, _ = runCmdEnv(ctx, repoRoot, env, "sync")
	if err := detachVolume(ctx, repoRoot, env, mount); err != nil {
		return err
	}
	detached = true
	if _, _, err := runCmdEnv(ctx, repoRoot, env, "hdiutil", "convert", rw, "-format", "UDZO", "-ov", "-o", dmg); err != nil {
		return err
	}
	return nil
}

// detachVolume unmounts a DMG, retrying while the volume is briefly busy (Finder
// may still hold it just after writing .DS_Store) before falling back to a forced
// detach — so the clean unmount that flushes the layout is tried first.
func detachVolume(ctx context.Context, repoRoot string, env []string, mount string) error {
	var last error
	for i := 0; i < 6; i++ {
		if _, _, err := runCmdEnv(ctx, repoRoot, env, "hdiutil", "detach", mount); err == nil {
			return nil
		} else {
			last = err
		}
		time.Sleep(700 * time.Millisecond)
	}
	if _, _, err := runCmdEnv(ctx, repoRoot, env, "hdiutil", "detach", mount, "-force"); err == nil {
		return nil
	}
	return last
}

// copyFile copies src to dst (truncating dst).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// pngSize returns a PNG's pixel dimensions (used to size the DMG window when no
// explicit DmgWindow is given). Non-PNG images return an error so the caller
// falls back to the configured/default size.
func pngSize(path string) (int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	cfg, err := png.DecodeConfig(f)
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}

// dmgMountPoint pulls the /Volumes/... mount path out of `hdiutil attach` output
// (tab-separated rows; the mounted volume row ends with the path).
func dmgMountPoint(hdiutilOut string) string {
	for _, line := range strings.Split(hdiutilOut, "\n") {
		if i := strings.Index(line, "/Volumes/"); i >= 0 {
			return strings.TrimRight(line[i:], " \t\r\n")
		}
	}
	return ""
}

// clearChannelVersion removes any existing nupkg for exactly this
// packId+version+channel from outDir, so re-packing that version succeeds — vpk
// refuses to pack when a release equal-or-greater is already present. Only this
// version's files are removed; prior versions stay for delta generation and vpk
// regenerates the channel manifest from what remains. A missing dir or missing
// files are not errors (nothing to clear on a first build).
func clearChannelVersion(outDir, packId, ver, ch string) error {
	pattern := filepath.Join(outDir, fmt.Sprintf("%s-%s-%s-*.nupkg", packId, ver, ch))
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	for _, m := range matches {
		if err := os.Remove(m); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("velopack: clearing prior pack %s: %w", filepath.Base(m), err)
		}
	}
	return nil
}

// writeTrustedSigningFile writes vpk's Azure Trusted Signing descriptor (the
// non-secret endpoint/account/profile; the credential comes from the environment).
func writeTrustedSigningFile(path string, ts TrustedSigning) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("{\n  \"Endpoint\": %q,\n  \"CodeSigningAccountName\": %q,\n  \"CertificateProfileName\": %q\n}\n",
		ts.Endpoint, ts.Account, ts.Profile)
	return os.WriteFile(path, []byte(body), 0o644)
}

// checkVpkMajor verifies the `vpk` CLI is installed and, when the csproj pins a
// Velopack <PackageReference>, that the CLI's major matches the library's — pack
// flags differ across Velopack majors, so a mismatch fails fast with guidance.
//
// vpk has no `--version` flag; its version is printed in the `--help` banner
// ("Velopack CLI X.Y.Z, for distributing applications."), so we read that.
func (a *Adapter) checkVpkMajor(ctx context.Context, req plugin.ArtifactsRequest) error {
	out, _, err := runCmdEnv(ctx, req.RepoRoot, nil, "vpk", "--help")
	if err != nil {
		return fmt.Errorf("velopack: `vpk` CLI not found on PATH (install with `dotnet tool install -g vpk`): %w", err)
	}
	content, err := os.ReadFile(filepath.Join(req.RepoRoot, req.Package.ManifestPath))
	if err != nil {
		return nil // can't read csproj — skip the optional check
	}
	ref := velopackRefVersion(string(content))
	if ref == "" {
		return nil // not pinned (or central package management) — nothing to compare
	}
	vpkVer := vpkVersion(out)
	if !sameMajor(ref, vpkVer) {
		return fmt.Errorf("velopack: the project references Velopack %s but the installed `vpk` CLI is %s — install a matching major (`dotnet tool update -g vpk --version %s.*`)",
			ref, vpkVer, majorOf(ref))
	}
	return nil
}

// vpkBannerRe captures the version from vpk's `--help` banner, e.g.
// "Velopack CLI 1.2.0, for distributing applications.".
var vpkBannerRe = regexp.MustCompile(`(?i)Velopack CLI\s+(\d+\.\d+(?:\.\d+)?)`)

// vpkVersion extracts the CLI version from `vpk --help` output: the banner form
// when present, else the first dotted-number version anywhere in the text.
func vpkVersion(helpOutput string) string {
	if m := vpkBannerRe.FindStringSubmatch(helpOutput); m != nil {
		return m[1]
	}
	return extractVersion(helpOutput)
}

// sameMajor reports whether two versions share a major — or true when either is
// unparseable, so the compatibility check is skipped rather than guessed wrong.
func sameMajor(a, b string) bool {
	am, bm := majorOf(a), majorOf(b)
	return am == "" || bm == "" || am == bm
}

// versionRe finds the first dotted-number version in a string.
var versionRe = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?`)

// extractVersion returns the first dotted-number version found in s, or "".
func extractVersion(s string) string { return versionRe.FindString(s) }

// majorOf returns the leading numeric component of a version ("1.2.3" -> "1"),
// or "" when s has no version.
func majorOf(s string) string {
	v := extractVersion(s)
	if v == "" {
		return ""
	}
	if i := strings.IndexByte(v, '.'); i >= 0 {
		return v[:i]
	}
	return v
}

// hostOS reports the family of the machine running the build.
func hostOS() targetOS {
	switch runtime.GOOS {
	case "darwin":
		return osMac
	case "windows":
		return osWindows
	default:
		return osLinux
	}
}

// buildableOn reports whether a channel targeting channelOS can be built on host.
// macOS packaging (codesign/notarytool/hdiutil) is macOS-only; Windows and Linux
// channels cross-build from any host (vpk + the .NET RID handle it).
func buildableOn(channelOS, host targetOS) bool {
	if channelOS == osMac {
		return host == osMac
	}
	return true
}

// appendFlag appends "name value" only when value is non-empty.
func appendFlag(args []string, name, value string) []string {
	if strings.TrimSpace(value) == "" {
		return args
	}
	return append(args, name, value)
}

// snapshotSuffix is a short " (snapshot, unsigned)" tag for build messages.
func snapshotSuffix(snapshot bool) string {
	if snapshot {
		return " (snapshot, unsigned)"
	}
	return ""
}

// skipNote describes any channels skipped because the host can't build them.
func skipNote(skipped []string) string {
	if len(skipped) == 0 {
		return ""
	}
	return fmt.Sprintf("; skipped on this host: %s", strings.Join(skipped, ", "))
}

// mergeSigningEnv appends the resolved signing secrets to base as KEY=VALUE
// entries (sorted, for determinism), returning base unchanged when there is
// nothing to sign with — so an unsigned build inherits the ambient environment.
func mergeSigningEnv(base []string, s *plugin.SigningCreds) []string {
	if s == nil || len(s.Env) == 0 {
		return base
	}
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := append([]string{}, base...)
	for _, k := range keys {
		out = append(out, k+"="+s.Env[k])
	}
	return out
}

// runCmdEnv runs name+args in dir with the given environment (nil = inherit the
// parent's) and returns captured stdout/stderr; a non-zero exit wraps stderr.
func runCmdEnv(ctx context.Context, dir string, env []string, name string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = env
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	stdout, stderr = outBuf.String(), errBuf.String()
	if err != nil {
		// Surface the tool's real message. Many CLIs (notably `vpk`) write their
		// fatal/diagnostic line to stdout, not stderr — including only stderr left
		// errors like "exit status 255:" with no reason. Prefer stderr, fall back
		// to (or append) stdout, bounded so a noisy log can't bury the cause.
		detail := strings.TrimSpace(stderr)
		if tail := lastLines(strings.TrimSpace(stdout), 15); tail != "" {
			if detail != "" {
				detail += "\n" + tail
			} else {
				detail = tail
			}
		}
		// Redact a signing token (jsign's `--storepass`) before echoing the command
		// in an error — the Windows --signTemplate carries it.
		err = fmt.Errorf("%s %s: %w: %s", name, redactCommand(args), err, detail)
	}
	return stdout, stderr, err
}

// lastLines returns the final n non-empty-bounded lines of s (all of it when it
// has n or fewer), so a command's error detail is surfaced without dumping a long
// build log into the message.
func lastLines(s string, n int) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// storepassRe matches a jsign `--storepass <value>` (or `--storepass=<value>`)
// inside a command argument, so the token can be redacted from echoed commands.
var storepassRe = regexp.MustCompile(`(--storepass[=\s]+)(\S+)`)

// redactCommand joins args for display with any `--storepass` token replaced by
// `***`. The token lives inside the single --signTemplate argument, so redaction
// is applied per-arg.
func redactCommand(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = storepassRe.ReplaceAllString(a, "${1}***")
	}
	return strings.Join(out, " ")
}
