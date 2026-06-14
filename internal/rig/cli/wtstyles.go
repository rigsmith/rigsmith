package cli

import "github.com/charmbracelet/lipgloss"

// Shared output styles for the worktree/branch/prune commands (moved here from
// clauderig). They mirror the brand palette via the package's brand* color
// aliases (see theme.go), so rig's worktree output reads like the rest of rig.
var (
	HeaderStyle = lipgloss.NewStyle().Bold(true).Underline(true)
	DimStyle    = lipgloss.NewStyle().Foreground(brandMuted)
	OkStyle     = lipgloss.NewStyle().Foreground(brandGreen)
	WarnStyle   = lipgloss.NewStyle().Foreground(brandYellow)
)
