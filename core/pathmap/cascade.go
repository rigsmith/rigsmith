package pathmap

import "strings"

// Cascade is the cross-OS path data for a single entry — a token or a sync root.
// It resolves a raw (un-expanded) template for a given OS by walking the agreed
// precedence: per-machine override → per-OS literal → portable default. "Raw"
// means tokens ($HOME, …) are still present; the Resolver expands them after.
// Keeping this free of expansion logic lets every entry share one cascade.
type Cascade struct {
	// Override wins on this machine only (empty when this machine doesn't diverge).
	Override string
	// PerOS maps an OS token (macos/windows/linux) to a literal path. The literal
	// for the *target* OS is the roaming default; other OSes' entries are dormant.
	PerOS map[string]string
	// Portable is a single token-bearing path used when no per-OS literal exists —
	// the layout is identical across OSes (e.g. $HOME/Dropbox).
	Portable string
}

// RawFor picks the raw template for os by precedence: override → per-OS literal →
// portable. It returns "" when the entry has no path configured for this OS.
func (c Cascade) RawFor(os string) string {
	if strings.TrimSpace(c.Override) != "" {
		return c.Override
	}
	if c.PerOS != nil {
		if literal, ok := c.PerOS[os]; ok && strings.TrimSpace(literal) != "" {
			return literal
		}
	}
	if strings.TrimSpace(c.Portable) == "" {
		return ""
	}
	return c.Portable
}
