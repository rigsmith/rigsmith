package velopack

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/core/jsonc"
)

// Config is the velopack.json packaging configuration that sits next to a
// project's .csproj. It carries the knobs `vpk pack` needs — the settings the
// original pack.sh hard-coded — so packaging is declarative and version
// controlled. Secrets are NOT stored here: code-signing/notarization secrets
// (the macOS .p12 password, the Azure client secret) ride in through the engine's
// signing config and are exposed to the build as environment variables.
type Config struct {
	// PackId is Velopack's application id (vpk --packId), e.g. "Halyards". It is
	// the stable identity the updater keys on; required.
	PackId string `json:"packId"`
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
	// "com.acme.app". Required when building an osx-* channel.
	BundleId string `json:"bundleId,omitempty"`
	// SignIdentity is the Developer ID Application identity (vpk --signAppIdentity).
	// Empty means build unsigned (rehearsal / CI without certs).
	SignIdentity string `json:"signIdentity,omitempty"`
	// NotaryProfile is the `xcrun notarytool` stored-credential profile
	// (vpk --notaryProfile). Empty means skip notarization.
	NotaryProfile string `json:"notaryProfile,omitempty"`
	// Dmg wraps the notarized .app in a distributable .dmg (the first-install
	// medium, separate from the update feed). Defaults to true.
	Dmg *bool `json:"dmg,omitempty"`
}

// WrapDmg reports whether the macOS .app should be wrapped in a .dmg (default true).
func (m Macos) WrapDmg() bool { return m.Dmg == nil || *m.Dmg }

// Windows holds Windows packaging settings. Code signing is host-dependent
// because vpk exposes different flags on each side:
//
//   - Building ON Windows (e.g. a CI runner): vpk's native Azure Trusted Signing
//     (--azureTrustedSignFile) — configured by TrustedSigning.
//   - Cross-compiling FROM macOS/Linux (the local-first path): that flag isn't
//     available, so a custom --signTemplate command (jsign) is used — SignTemplate.
//
// Set whichever matches where you build (or both, to be correct everywhere); the
// adapter picks by host.
type Windows struct {
	// SignTemplate is a custom signing command for a Windows build cross-compiled
	// from a non-Windows host, passed to `vpk [win] pack --signTemplate`. vpk runs
	// it once per binary, substituting `{{file}}` for the path — e.g. a jsign +
	// Azure Trusted Signing invocation. The adapter also passes
	// `--signExclude '\.dll$'` so only the .exe / Setup.exe are signed.
	//
	// $VAR / ${VAR} are expanded from the build environment first (vpk itself runs
	// the command without a shell), so a token reference like
	// `--storepass $AZURE_CODESIGN_TOKEN` resolves from a pre-set env var (exported
	// or in .env.local). A `--storepass` token is redacted from any echoed command.
	// Empty leaves a cross-compiled build unsigned.
	SignTemplate string `json:"signTemplate,omitempty"`
	// TrustedSigning configures vpk's native Azure Trusted Signing
	// (--azureTrustedSignFile), used only when building on a Windows host. Empty
	// leaves a native Windows build unsigned.
	TrustedSigning *TrustedSigning `json:"trustedSigning,omitempty"`
}

// TrustedSigning holds the non-secret Azure Trusted Signing identifiers vpk
// writes into its azureTrustedSignFile JSON. The credential itself
// (AZURE_CLIENT_SECRET etc.) is supplied via the signing env and consumed by
// Azure's DefaultAzureCredential chain.
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
