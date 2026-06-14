package cli

import (
	lgv2 "charm.land/lipgloss/v2"
	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/core/fang"
)

// The brand palette and theme constructors now live in the shared core/brand
// package so every rigsmith binary renders the identity identically. These
// package-local aliases keep rig's many internal lipgloss styles (coverage,
// dashboards, menus) reading as brandBlue / brandMuted / … without each style
// reaching across packages.
var (
	brandBlue   = brand.Blue   // rig — core accent
	brandViolet = brand.Violet // change
	brandGreen  = brand.Green  // ship / success
	brandAmber  = brand.Amber  // claude
	brandCyan   = brand.Cyan   // info / verbs
	brandYellow = brand.Yellow // warn
	brandRed    = brand.Red    // error
	brandMuted  = brand.Muted  // secondary text
	brandPaper  = brand.Paper  // foreground
)

// rigTheme is the brand huh theme for rig's pickers — the shared theme painted
// with rig's blue accent.
func rigTheme() *huh.Theme { return brand.Theme(brand.AccentRig) }

// rigColorScheme is the brand fang scheme for rig's --help / usage / errors.
func rigColorScheme(c lgv2.LightDarkFunc) fang.ColorScheme {
	return brand.ColorSchemeFunc(brand.AccentRig)(c)
}
