package commands

import "github.com/charmbracelet/lipgloss"

var (
	HeaderStyle = lipgloss.NewStyle().Bold(true).Underline(true)
	DimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	OkStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	WarnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	ErrStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)
