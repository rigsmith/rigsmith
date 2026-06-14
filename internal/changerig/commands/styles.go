package commands

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/core/changeset"
)

// Bump styles use the shared semantic brand colors: a major bump reads as the
// loudest (red), minor as a caution (yellow), patch as routine/success (green),
// and dimmed metadata as muted.
var (
	MajorStyle  = lipgloss.NewStyle().Bold(true).Foreground(brand.Red)
	MinorStyle  = lipgloss.NewStyle().Bold(true).Foreground(brand.Yellow)
	PatchStyle  = lipgloss.NewStyle().Foreground(brand.Green)
	DimStyle    = lipgloss.NewStyle().Foreground(brand.Muted)
	HeaderStyle = lipgloss.NewStyle().Bold(true).Underline(true)
)

func styleFor(b changeset.Bump) lipgloss.Style {
	switch b {
	case changeset.BumpMajor:
		return MajorStyle
	case changeset.BumpMinor:
		return MinorStyle
	default:
		return PatchStyle
	}
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}
