package commands

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/core/changeset"
)

var (
	MajorStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	MinorStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	PatchStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	DimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
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
