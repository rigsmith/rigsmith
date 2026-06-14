package brand

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// banner is one tool's header identity: the interior glyph, the wordmark (an
// optional muted prefix + a bold stem), and the tagline — rendered as the
// design's bracket box painted in the tool's accent. Mirrors the per-tool
// banners in design/"RigSmith CLI.html".
type banner struct {
	accent  lipgloss.AdaptiveColor
	glyph   string // interior mark: ● ↻ ↑ ✳
	lite    string // muted wordmark prefix ("change"/"ship"/"claude"); "" for rig
	stem    string // bold wordmark stem ("rig"/"Rig")
	tagline string
}

// Per-tool banners. The accent paints the bracket + glyph; the wordmark stem is
// paper-bold over a muted prefix; the tagline and version read muted. The brand
// wordmarks (rig/changeRig/shipRig/claudeRig) match the design, not the binary
// names (changerig/relrig) — see design/"RigSmith CLI.html".
var (
	rigBanner    = banner{AccentRig, "●", "", "rig", "convention-first dev launcher"}
	changeBanner = banner{AccentChange, "↻", "change", "Rig", "changeset lifecycle"}
	shipBanner   = banner{AccentShip, "↑", "ship", "Rig", "release front door"}
	claudeBanner = banner{AccentClaude, "✳", "claude", "Rig", "Claude Code setup sync"}
)

// RigBanner, ChangeBanner, ShipBanner, and ClaudeBanner render a tool's header
// for a given version string. Pass one straight to fang:
//
//	fang.WithBanner(brand.RigBanner)
func RigBanner(version string) string    { return rigBanner.render(version) }
func ChangeBanner(version string) string { return changeBanner.render(version) }
func ShipBanner(version string) string   { return shipBanner.render(version) }
func ClaudeBanner(version string) string { return claudeBanner.render(version) }

// render lays out the three-line header: the accent bracket box on the left, the
// wordmark + version on the middle row, and the tagline on the bottom row.
func (b banner) render(version string) string {
	accent := lipgloss.NewStyle().Foreground(b.accent)
	bold := lipgloss.NewStyle().Foreground(Paper).Bold(true)
	muted := lipgloss.NewStyle().Foreground(Muted)

	word := bold.Render(b.stem)
	if b.lite != "" {
		word = muted.Render(b.lite) + word
	}
	if v := formatVersion(version); v != "" {
		word += "  " + muted.Render(v)
	}

	// The bracket box (design mini): a constant frame with the glyph centered,
	// the whole frame painted in the tool accent.
	const pad = "   " // gap between the box and the wordmark/tagline column
	return strings.Join([]string{
		"  " + accent.Render("╭─╴ ╶─╮"),
		"  " + accent.Render("│  "+b.glyph+"  │") + pad + word,
		"  " + accent.Render("╰─╴ ╶─╯") + pad + muted.Render(b.tagline+" · rigsmith.dev"),
	}, "\n")
}

// formatVersion prefixes a bare semver with "v" (1.4.0 → v1.4.0) and leaves
// anything else (an already "v"-prefixed tag, "dev", "unknown …") untouched.
func formatVersion(v string) string {
	if v == "" {
		return ""
	}
	if v[0] >= '0' && v[0] <= '9' {
		return "v" + v
	}
	return v
}
