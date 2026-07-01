package velopack

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/core/jsonc"
)

// Config is the velopack.json packaging configuration that sits next to a
// project's manifest (a .csproj, Cargo.toml, package.json, or go.mod). It carries
// the knobs `vpk pack` needs — the settings the original pack.sh hard-coded — so
// packaging is declarative and version controlled. Velopack is not .NET-only: vpk
// wraps any directory of built binaries, so the base ecosystem is configurable
// (Base) and non-dotnet apps describe their build with Build. Secrets are NOT
// stored here: code-signing/notarization secrets (the macOS .p12 password, the
// Azure client secret) ride in through the engine's signing config and are exposed
// to the build as environment variables.
type Config struct {
	// PackId is Velopack's application id (vpk --packId), e.g. "Halyards". It is
	// the stable identity the updater keys on; required.
	PackId string `json:"packId"`
	// Base names the underlying language ecosystem whose project sits next to this
	// velopack file — "dotnet", "cargo", "node", or "go". Empty auto-detects from
	// the sibling manifest (.csproj/Cargo.toml/package.json/go.mod). The base owns
	// the version (its manifest stays the single source of truth) and, for dotnet,
	// the build; every other base describes its build via Build.
	Base string `json:"base,omitempty"`
	// Build configures how the app is built into the directory `vpk pack` wraps.
	// Optional for a dotnet base (defaults to `dotnet publish`); required for the
	// cargo/node/go bases, which have no built-in publish-to-directory step.
	Build *Build `json:"build,omitempty"`
	// PackTitle is the human title (vpk --packTitle); defaults to PackId.
	PackTitle string `json:"packTitle,omitempty"`
	// PackAuthors is the author/vendor string (vpk --packAuthors).
	PackAuthors string `json:"packAuthors,omitempty"`
	// MainExe is the entry executable name without extension (vpk --mainExe). On
	// Windows ".exe" is appended. Defaults to PackId.
	MainExe string `json:"mainExe,omitempty"`
	// Channels are the Velopack channels to build — each is a .NET RID, e.g.
	// "osx-arm64", "osx-x64", "win-x64". A channel is the per-architecture update
	// feed the app subscribes to. Required (at least one).
	Channels []string `json:"channels"`
	// Icon is the per-OS icon path (relative to the repo root): a .icns for macOS,
	// a .ico for Windows.
	Icon Icon `json:"icon,omitempty"`
	// Output is the directory (relative to the repo root) vpk writes installers and
	// feeds to. Defaults to "dist/releases".
	Output string `json:"output,omitempty"`
	// Macos holds macOS-only packaging settings (signing identity, notarization,
	// DMG wrapping).
	Macos Macos `json:"macos,omitempty"`
	// Windows holds Windows-only packaging settings (Azure Trusted Signing).
	Windows Windows `json:"windows,omitempty"`
}

// Build tells the adapter how to produce the per-channel directory of built
// binaries that `vpk pack` wraps — needed for bases without an automatic build
// (cargo/node/go) or to override the dotnet default. Command runs once per channel
// through the platform shell, with the release build environment plus these
// variables set:
//
//	CHANNEL / RID   the channel RID (e.g. win-x64)
//	OUTPUT          absolute directory the command must fill (what vpk then packs)
//	VERSION         the resolved release version
//	GOOS / GOARCH   the Go target for this RID (a `go build` can use them directly)
//	RUST_TARGET     the Rust target triple for this RID (cargo build --target)
//
// The command builds the app for that target into $OUTPUT. PackDir overrides which
// directory vpk packs when the build emits elsewhere (e.g. an electron-builder
// out/ tree); it may reference the same variables and defaults to $OUTPUT.
type Build struct {
	Command string `json:"command,omitempty"`
	PackDir string `json:"packDir,omitempty"`
}

// Icon is the per-OS application icon.
type Icon struct {
	Macos   string `json:"macos,omitempty"`
	Windows string `json:"windows,omitempty"`
}

// Macos holds macOS packaging settings. SignIdentity/NotaryProfile are the
// non-secret identifiers (a keychain identity name and a stored notarytool
// profile); the unlock/notary secrets ride in via the signing env.
type Macos struct {
	// BundleId is the macOS bundle identifier (vpk --bundleId), e.g.
	// "com.acme.app". Required when building an osx-* channel — unless Plist is
	// set, in which case the plist supplies CFBundleIdentifier (vpk forbids
	// --bundleId together with --plist) and BundleId is ignored.
	BundleId string `json:"bundleId,omitempty"`
	// Plist is a repo-root-relative path to a custom Info.plist (vpk --plist).
	// vpk uses it VERBATIM in the .app bundle — it does not inject CFBundleVersion
	// or CFBundleIdentifier — so the file must carry all bundle keys. The token
	// ${version} is rendered to the release version before packing (so
	// CFBundleVersion tracks releases). Use it for bundle keys vpk can't set,
	// e.g. NSServices / URL schemes. When set, --bundleId is dropped.
	Plist string `json:"plist,omitempty"`
	// SignIdentity is the Developer ID Application identity (vpk --signAppIdentity).
	// Empty means build unsigned (rehearsal / CI without certs).
	SignIdentity string `json:"signIdentity,omitempty"`
	// NotaryProfile is the `xcrun notarytool` stored-credential profile
	// (vpk --notaryProfile). Empty means skip notarization.
	NotaryProfile string `json:"notaryProfile,omitempty"`
	// Dmg wraps the notarized .app in a distributable .dmg (the first-install
	// medium, separate from the update feed). Defaults to true.
	Dmg *bool `json:"dmg,omitempty"`
	// DmgBackground is a repo-root-relative path to a background image for the
	// install-DMG window — a branded backdrop, typically with a "drag to
	// Applications" arrow. When set, the window is sized to DmgWindow (or, for a
	// PNG, the image's own pixel size) and the app + Applications icons are
	// centered in the left and right halves over it. Supply a HiDPI TIFF (1×+2×
	// reps) or a 2× PNG with DmgWindow set for a crisp result on Retina displays.
	DmgBackground string `json:"dmgBackground,omitempty"`
	// DmgWindow overrides the install-window content size, in logical points.
	// Required for a TIFF/2× background (its pixel size isn't the logical size);
	// for a 1× PNG it defaults to the image's pixel dimensions.
	DmgWindow *DmgWindow `json:"dmgWindow,omitempty"`
}

// DmgWindow is the install-DMG window's content size in logical points.
type DmgWindow struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// WrapDmg reports whether the macOS .app should be wrapped in a .dmg (default true).
func (m Macos) WrapDmg() bool { return m.Dmg == nil || *m.Dmg }

// Windows holds Windows packaging settings. For code signing, prefer
// TrustedSigning — it works on every host: vpk's native Azure Trusted Signing
// (--azureTrustedSignFile) when building ON Windows, and a jsign command the
// adapter synthesizes (minting the token itself) when cross-compiling FROM
// macOS/Linux, where that flag isn't exposed. SignTemplate is an escape hatch for
// a custom signer.
type Windows struct {
	// SignTemplate is an OPTIONAL custom signing command for a Windows build
	// cross-compiled from a non-Windows host (passed to `vpk [win] pack
	// --signTemplate`; the adapter also adds `--signExclude '\.dll$'`). vpk runs it
	// once per binary, substituting `{{file}}`. Set this only to override the
	// jsign command the adapter derives from TrustedSigning — e.g. a different
	// signer. $VAR / ${VAR} are expanded from the build env (vpk runs the command
	// without a shell); a `--storepass` token is redacted from any echoed command.
	// When empty, the adapter uses TrustedSigning (recommended).
	SignTemplate string `json:"signTemplate,omitempty"`
	// TrustedSigning configures Azure Trusted Signing for Windows on ANY host. On
	// a Windows host the adapter passes vpk's native --azureTrustedSignFile; when
	// cross-compiling from macOS/Linux it synthesizes a jsign command from these
	// identifiers and a token it mints from the service-principal creds in the
	// build env. Empty leaves a Windows build unsigned.
	TrustedSigning *TrustedSigning `json:"trustedSigning,omitempty"`
}

// TrustedSigning holds the non-secret Azure Trusted Signing identifiers. The
// credentials ride in via the build/signing env: on a Windows host vpk's
// DefaultAzureCredential chain consumes AZURE_* (or a managed identity); when
// cross-compiling, the adapter mints a token from AZURE_TENANT_ID/CLIENT_ID/
// CLIENT_SECRET (or uses a pre-set AZURE_CODESIGN_TOKEN).
type TrustedSigning struct {
	Endpoint string `json:"endpoint"`
	Account  string `json:"account"`
	Profile  string `json:"profile"`
}

// loadConfig reads and validates the velopack.json/velopack.jsonc in dir.
func loadConfig(dir string) (Config, error) {
	var path string
	for name := range configNames {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			path = p
			break
		}
	}
	if path == "" {
		return Config{}, fmt.Errorf("no velopack.json in %s", dir)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := jsonc.Unmarshal(content, &cfg); err != nil {
		return Config{}, fmt.Errorf("velopack: parse %s: %w", path, err)
	}
	return cfg.withDefaults(), cfg.validate()
}

// configBase reads only the `base` pin from the velopack file in dir, tolerant of
// a missing, malformed, or invalid config (returns ""). Discovery uses this rather
// than loadConfig so a broken velopack file never fails workspace-wide discovery;
// the full parse/validate happens later in Artifacts, where the error is scoped to
// the one app being built.
func configBase(dir string) string {
	for name := range configNames {
		p := filepath.Join(dir, name)
		if !fileExists(p) {
			continue
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return ""
		}
		var c struct {
			Base string `json:"base"`
		}
		_ = jsonc.Unmarshal(content, &c) // tolerate parse errors: base stays ""
		return c.Base
	}
	return ""
}

// withDefaults fills the optional fields that have sensible defaults.
func (c Config) withDefaults() Config {
	if c.PackTitle == "" {
		c.PackTitle = c.PackId
	}
	if c.MainExe == "" {
		c.MainExe = c.PackId
	}
	if c.Output == "" {
		c.Output = filepath.Join("dist", "releases")
	}
	return c
}

// validate reports the first configuration error that would make packaging fail.
func (c Config) validate() error {
	if strings.TrimSpace(c.PackId) == "" {
		return fmt.Errorf("velopack: packId is required")
	}
	switch c.Base {
	case "", baseDotnet, baseCargo, baseNode, baseGo:
	default:
		return fmt.Errorf("velopack: base %q is not recognized (expected dotnet, cargo, node, or go)", c.Base)
	}
	if len(c.Channels) == 0 {
		return fmt.Errorf("velopack: at least one channel is required")
	}
	for _, ch := range c.Channels {
		if osOf(ch) == osUnknown {
			return fmt.Errorf("velopack: channel %q is not a recognized RID (expected osx-*/win-*/linux-*)", ch)
		}
	}
	return nil
}

// Base ecosystem ids velopack overlays. These match the EcosystemInfo.ID of the
// embedded base adapters (gomod reports "go").
const (
	baseDotnet = "dotnet"
	baseCargo  = "cargo"
	baseNode   = "node"
	baseGo     = "go"
)

// detectBase infers the base ecosystem from the manifest sitting next to a
// velopack file: a *.csproj → dotnet, Cargo.toml → cargo, package.json → node,
// go.mod → go. Empty when none is found (a base-less app — not yet supported).
func detectBase(dir string) string {
	switch {
	case hasFileWithExt(dir, ".csproj"):
		return baseDotnet
	case fileExists(filepath.Join(dir, "Cargo.toml")):
		return baseCargo
	case fileExists(filepath.Join(dir, "package.json")):
		return baseNode
	case fileExists(filepath.Join(dir, "go.mod")):
		return baseGo
	default:
		return ""
	}
}

// fileExists reports whether p is an existing regular file.
func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// hasFileWithExt reports whether dir contains a regular file with the given
// extension (case-insensitive), e.g. any *.csproj.
func hasFileWithExt(dir, ext string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ext) {
			return true
		}
	}
	return false
}

// targetOS is the host family a channel (RID) builds for.
type targetOS int

const (
	osUnknown targetOS = iota
	osMac
	osWindows
	osLinux
)

// osOf maps a .NET RID to its target OS family (osx-arm64 -> osMac, win-x64 ->
// osWindows, linux-x64 -> osLinux). Unknown prefixes yield osUnknown.
func osOf(rid string) targetOS {
	switch {
	case strings.HasPrefix(rid, "osx-"):
		return osMac
	case strings.HasPrefix(rid, "win-"):
		return osWindows
	case strings.HasPrefix(rid, "linux-"):
		return osLinux
	default:
		return osUnknown
	}
}
