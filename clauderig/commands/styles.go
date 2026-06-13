package commands

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/core/brand"
)

var (
	HeaderStyle = lipgloss.NewStyle().Bold(true).Underline(true)
	DimStyle    = lipgloss.NewStyle().Foreground(brand.Muted)
	OkStyle     = lipgloss.NewStyle().Foreground(brand.Green)
	WarnStyle   = lipgloss.NewStyle().Foreground(brand.Yellow)
	ErrStyle    = lipgloss.NewStyle().Foreground(brand.Red)
)
