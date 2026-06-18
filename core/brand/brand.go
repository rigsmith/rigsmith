// Package brand is the single source of truth for the rigsmith tools' terminal
// look: the brand color palette plus shared constructors for huh themes (the
// interactive pickers) and fang color schemes (--help / usage / errors).
//
// Every tool shares the semantic status colors (red/green/yellow/cyan/muted)
// but carries its own accent — the identity color used for titles, prompts and
// selection cursors:
//
//	rig        blue    AccentRig
//	changeRig  violet  AccentChange
//	shipRig    green   AccentShip
//	claudeRig  amber   AccentClaude
//
// Pass a tool's accent to Theme / ColorSchemeFunc; the status colors stay
// common. This is lifted from cli's old internal theme so all four binaries
// render the brand identically instead of each shipping its own ANSI colors.
package brand

import (
	"image/color"

	lgv2 "charm.land/lipgloss/v2"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/rigsmith/core/fang"
)

// Brand palette — mirrored from design/ (marks.js RS_PALETTE + the semantic CLI
// mapping in "RigSmith CLI.html"). The design system defines colors in oklch;
// these are the sRGB hex equivalents so truecolor terminals render the brand
// exactly.
//
// Each color is an AdaptiveColor: the Dark variant is the design value (oklch
// lightness ~0.70, tuned for the "ink" background); the Light variant is the
// same hue dropped to ~0.50 lightness so it keeps contrast on a white terminal.
// lipgloss picks per the terminal's detected background.
var (
	Blue   = lipgloss.AdaptiveColor{Dark: "#4BA3F7", Light: "#006BBB"} // rig — core accent
	Violet = lipgloss.AdaptiveColor{Dark: "#AD87ED", Light: "#7750B1"} // change
	Green  = lipgloss.AdaptiveColor{Dark: "#4CB86A", Light: "#007329"} // ship / success
	Amber  = lipgloss.AdaptiveColor{Dark: "#E48233", Light: "#A74A00"} // claude
	Cyan   = lipgloss.AdaptiveColor{Dark: "#5DCBD1", Light: "#00747A"} // info / verbs
	Yellow = lipgloss.AdaptiveColor{Dark: "#E4B750", Light: "#9D7200"} // warn
	Red    = lipgloss.AdaptiveColor{Dark: "#EF6661", Light: "#B63132"} // error
	Muted  = lipgloss.AdaptiveColor{Dark: "#8A8A96", Light: "#5A5E63"} // secondary text
	Paper  = lipgloss.AdaptiveColor{Dark: "#ECECEE", Light: "#0E0E12"} // foreground (paper on dark, ink on light)
)

// Per-tool accents. Each binary passes its own to Theme / ColorSchemeFunc; the
// semantic status colors above stay shared.
var (
	AccentRig    = Blue   // rig
	AccentChange = Violet // changeRig
	AccentShip   = Green  // shipRig / release
	AccentClaude = Amber  // claudeRig
)

// AccentFor returns a tool's accent by its binary name, defaulting to rig's blue
// for an unknown name. It lets a builder shared between tools — changerig and
// shiprig reuse the same command builders — resolve the right accent at runtime
// from cmd.Root().Name() instead of hard-coding one tool's color.
func AccentFor(tool string) lipgloss.AdaptiveColor {
	switch tool {
	case "changerig", "changeset":
		return AccentChange
	case "shiprig":
		return AccentShip
	case "clauderig":
		return AccentClaude
	default:
		return AccentRig
	}
}

// Theme builds the brand huh theme for a tool's interactive pickers, painted
// with the given accent. It starts from huh's ThemeBase (structure only, no
// colors) and applies the brand palette over the fields that show in a select /
// multi-select: the accent for the active cursor and titles, green for chosen
// items, muted for the rest.
func Theme(accent lipgloss.AdaptiveColor) *huh.Theme {
	t := huh.ThemeBase()

	// Left accent rail on the focused group.
	t.Focused.Base = t.Focused.Base.BorderForeground(accent)

	// Titles / prompts — the tool accent.
	t.Focused.Title = t.Focused.Title.Foreground(accent).Bold(true)
	t.Focused.NoteTitle = t.Focused.NoteTitle.Foreground(accent).Bold(true)
	t.Focused.Description = t.Focused.Description.Foreground(Muted)

	// Errors.
	t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Foreground(Red)
	t.Focused.ErrorMessage = t.Focused.ErrorMessage.Foreground(Red)

	// Cursor / selector glyphs.
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(accent)
	t.Focused.MultiSelectSelector = t.Focused.MultiSelectSelector.Foreground(accent)
	t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(accent)
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(accent)

	// Options: chosen rows go green, the rest stay paper / muted.
	t.Focused.Option = t.Focused.Option.Foreground(Paper)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(Green)
	t.Focused.UnselectedOption = t.Focused.UnselectedOption.Foreground(Paper)
	t.Focused.SelectedPrefix = lipgloss.NewStyle().Foreground(Green).SetString("✓ ")
	t.Focused.UnselectedPrefix = lipgloss.NewStyle().Foreground(Muted).SetString("• ")

	// Filter (`/`) text input.
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(Green)
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(accent)
	t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.Foreground(Muted)

	// Help line (the keybind hints under the picker): keys read as actions
	// (cyan, like verbs), their descriptions and the separators stay muted.
	t.Help.ShortKey = t.Help.ShortKey.Foreground(Cyan)
	t.Help.FullKey = t.Help.FullKey.Foreground(Cyan)
	t.Help.ShortDesc = t.Help.ShortDesc.Foreground(Muted)
	t.Help.FullDesc = t.Help.FullDesc.Foreground(Muted)
	t.Help.ShortSeparator = t.Help.ShortSeparator.Foreground(Muted)
	t.Help.FullSeparator = t.Help.FullSeparator.Foreground(Muted)
	t.Help.Ellipsis = t.Help.Ellipsis.Foreground(Muted)

	// Blurred mirrors focused but hides the accent rail.
	t.Blurred = t.Focused
	t.Blurred.Base = t.Focused.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()

	t.Group.Title = t.Focused.Title
	t.Group.Description = t.Focused.Description
	return t
}

// ColorSchemeFunc returns a fang color-scheme function for a tool's `--help`,
// usage and error output, painted with the given accent. fang renders with
// lipgloss v2 and resolves a light/dark value per field via the LightDarkFunc
// it passes in, so we bridge each brand AdaptiveColor's two hex values through
// it. We start from fang's default scheme and override only the tokens the
// design system speaks to (accent titles/program, verbs cyan, flags green,
// muted secondary text, errors red), leaving fang's layout defaults intact.
//
// Pass it straight to fang: fang.WithColorSchemeFunc(brand.ColorSchemeFunc(brand.AccentRig)).
func ColorSchemeFunc(accent lipgloss.AdaptiveColor) func(lgv2.LightDarkFunc) fang.ColorScheme {
	return func(c lgv2.LightDarkFunc) fang.ColorScheme {
		pick := func(a lipgloss.AdaptiveColor) color.Color {
			return c(lgv2.Color(a.Light), lgv2.Color(a.Dark))
		}
		cs := fang.DefaultColorScheme(c)
		cs.Title = pick(accent)      // section headings
		cs.Program = pick(accent)    // the program name
		cs.Command = pick(Cyan)      // subcommand / verb names
		cs.Flag = pick(Green)        // --flags
		cs.FlagDefault = pick(Muted) // (default: …) hints
		cs.Argument = pick(Paper)    // positional args
		cs.DimmedArgument = pick(Muted)
		cs.Description = pick(Muted) // flag/command descriptions
		cs.Comment = pick(Muted)
		cs.QuotedString = pick(Amber) // "quoted" literals
		cs.Help = pick(Muted)         // the bottom help hint
		cs.Dash = pick(Muted)
		cs.ErrorDetails = pick(Red)
		// Match the design's error treatment (design/RigSmith CLI.html → .badge.err):
		// the brand red as TEXT over a faint red wash, not a solid bright fill.
		// fang's default badge fills the block at full saturation, which turns the
		// design's coral c-red into "pink"; the wash keeps it reading as a red
		// error badge. The wash hexes are color-mix(c-red 22%, terminal bg) for the
		// dark (#0E0E12) and light (#ECECEE) backgrounds.
		cs.ErrorHeader = [2]color.Color{
			pick(Red),
			c(lgv2.Color("#F7DAD9"), lgv2.Color("#3F2123")),
		}
		return cs
	}
}
