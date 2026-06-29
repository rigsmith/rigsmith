package velopack

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/plugin"
)

// Artifacts builds the Velopack installers and update feeds for every configured
// channel and returns them so the release step can attach/upload them. For each
// channel (a .NET RID) it runs `dotnet publish --self-contained` then `vpk pack`
// with the per-OS signing flags, and on macOS wraps the notarized .app in a .dmg.
// Outputs land in the configured output dir (default dist/releases).
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

	ver := req.Package.Version
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

	env := mergeSigningEnv(os.Environ(), req.Signing)
	csproj := req.Package.ManifestPath // repo-relative; commands run with cwd = RepoRoot

	for _, ch := range planned {
		pubRel := filepath.Join("dist", "publish", ch)
		if _, _, err := runCmdEnv(ctx, req.RepoRoot, env, "dotnet", buildPublishArgs(csproj, ch, pubRel)...); err != nil {
			return plugin.ArtifactsResponse{}, fmt.Errorf("dotnet publish %s: %w", ch, err)
		}

		// Windows Azure Trusted Signing wants a JSON descriptor file; write it next
		// to the output (best effort cleanup leaves it — it carries no secrets).
		azureFile := ""
		if osOf(ch) == osWindows && cfg.Windows.TrustedSigning != nil && !req.Snapshot {
			azureFile = filepath.Join("dist", "trustedsigning.json")
			if err := writeTrustedSigningFile(filepath.Join(req.RepoRoot, azureFile), *cfg.Windows.TrustedSigning); err != nil {
				return plugin.ArtifactsResponse{}, err
			}
		}

		packArgs := buildPackArgs(cfg, ch, pubRel, releasesRel, ver, req.Snapshot, azureFile)
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

// buildPackArgs is the `vpk pack` argv for one channel. It dispatches per OS:
// macOS gets --bundleId and, unless snapshot, the Developer ID identity + notary
// profile; Windows gets the .exe mainExe and, unless snapshot, the Azure Trusted
// Signing descriptor; Linux gets the common flags only. Empty optional values are
// omitted so a partially-configured velopack.json still produces a valid command.
func buildPackArgs(cfg Config, ch, pubDir, output, version string, snapshot bool, azureFile string) []string {
	mainExe := cfg.MainExe
	if osOf(ch) == osWindows {
		mainExe += ".exe"
	}
	args := []string{
		"pack",
		"--packId", cfg.PackId,
		"--packVersion", version,
		"--packDir", pubDir,
		"--mainExe", mainExe,
		"--packTitle", cfg.PackTitle,
	}
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
		if !snapshot && azureFile != "" {
			args = appendFlag(args, "--azureTrustedSignFile", azureFile)
		}
	}

	args = append(args, "--channel", ch, "-r", ch, "-o", output)
	return args
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

	if _, _, err := runCmdEnv(ctx, repoRoot, env, "ditto", "-x", "-k", portable, tmp); err != nil {
		return fmt.Errorf("ditto extract: %w", err)
	}
	appPath := filepath.Join(tmp, cfg.MainExe+".app")
	_, _, _ = runCmdEnv(ctx, repoRoot, env, "xcrun", "stapler", "staple", appPath) // best-effort

	dmg := filepath.Join(outAbs, fmt.Sprintf("%s-%s.dmg", cfg.PackId, ch))
	if _, _, err := runCmdEnv(ctx, repoRoot, env, "hdiutil", "create",
		"-volname", cfg.PackTitle, "-srcfolder", appPath, "-ov", "-format", "UDZO", dmg); err != nil {
		return fmt.Errorf("hdiutil create: %w", err)
	}
	if !snapshot && cfg.Macos.SignIdentity != "" {
		_, _, _ = runCmdEnv(ctx, repoRoot, env, "codesign", "--force", "--timestamp", "--sign", cfg.Macos.SignIdentity, dmg) // best-effort
	}

	_ = os.Remove(portable)
	_ = os.Remove(filepath.Join(outAbs, fmt.Sprintf("%s-%s-Setup.pkg", cfg.PackId, ch)))
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
		err = fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr))
	}
	return stdout, stderr, err
}
