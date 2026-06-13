package cli

import (
	"image/color"

	lgv2 "charm.land/lipgloss/v2"
	"github.com/charmbracelet/fang"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// Brand palette — the single source of truth for rig's TUI accents, mirrored
// from design/ (marks.js RS_PALETTE + the semantic CLI mapping in
// "RigSmith CLI.html"). The design system defines colors in oklch; these are the
// sRGB hex equivalents so truecolor terminals render the brand exactly.
//
// Each accent is an AdaptiveColor: the Dark variant is the design value
// (oklch lightness ~0.70, tuned for the "ink" background); the Light variant is
// the same hue dropped to ~0.50 lightness so it keeps contrast on a white
// terminal. lipgloss picks per the terminal's detected background.
//
//	rig    250  blue    core accent (prompt, selector, titles)
//	change 300  violet  changeRig
//	ship   150  green   shipRig / success
//	claude 55   amber   claudeRig
//	info   200  cyan    info / verbs
//	warn   85   yellow
//	error  25   red
var (
	brandBlue   = lipgloss.AdaptiveColor{Dark: "#4BA3F7", Light: "#006BBB"} // rig — core accent
	brandViolet = lipgloss.AdaptiveColor{Dark: "#AD87ED", Light: "#7750B1"} // change
	brandGreen  = lipgloss.AdaptiveColor{Dark: "#4CB86A", Light: "#007329"} // ship / success
	brandAmber  = lipgloss.AdaptiveColor{Dark: "#E48233", Light: "#A74A00"} // claude
	brandCyan   = lipgloss.AdaptiveColor{Dark: "#5DCBD1", Light: "#00747A"} // info / verbs
	brandYellow = lipgloss.AdaptiveColor{Dark: "#E4B750", Light: "#9D7200"} // warn
	brandRed    = lipgloss.AdaptiveColor{Dark: "#EF6661", Light: "#B63132"} // error
	brandMuted  = lipgloss.AdaptiveColor{Dark: "#8A8A96", Light: "#5A5E63"} // secondary text
	brandPaper  = lipgloss.AdaptiveColor{Dark: "#ECECEE", Light: "#0E0E12"} // foreground (paper on dark, ink on light)
)

// rigTheme is the brand huh theme shared by every rig picker. It starts from
// huh's ThemeBase (structure only, no colors) and paints the brand palette over
// the fields that show in a select / multi-select: blue for the active cursor
// and titles, green for chosen items, muted for the rest.
func rigTheme() *huh.Theme {
	t := huh.ThemeBase()

	// Left accent rail on the focused group.
	t.Focused.Base = t.Focused.Base.BorderForeground(brandBlue)

	// Titles / prompts — the rig accent.
	t.Focused.Title = t.Focused.Title.Foreground(brandBlue).Bold(true)
	t.Focused.NoteTitle = t.Focused.NoteTitle.Foreground(brandBlue).Bold(true)
	t.Focused.Description = t.Focused.Description.Foreground(brandMuted)

	// Errors.
	t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Foreground(brandRed)
	t.Focused.ErrorMessage = t.Focused.ErrorMessage.Foreground(brandRed)

	// Cursor / selector glyphs.
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(brandBlue)
	t.Focused.MultiSelectSelector = t.Focused.MultiSelectSelector.Foreground(brandBlue)
	t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(brandBlue)
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(brandBlue)

	// Options: chosen rows go green, the rest stay paper / muted.
	t.Focused.Option = t.Focused.Option.Foreground(brandPaper)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(brandGreen)
	t.Focused.UnselectedOption = t.Focused.UnselectedOption.Foreground(brandPaper)
	t.Focused.SelectedPrefix = lipgloss.NewStyle().Foreground(brandGreen).SetString("✓ ")
	t.Focused.UnselectedPrefix = lipgloss.NewStyle().Foreground(brandMuted).SetString("• ")

	// Filter (`/`) text input.
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(brandGreen)
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(brandBlue)
	t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.Foreground(brandMuted)

	// Help line (the keybind hints under the picker): keys read as actions
	// (cyan, like verbs), their descriptions and the separators stay muted.
	t.Help.ShortKey = t.Help.ShortKey.Foreground(brandCyan)
	t.Help.FullKey = t.Help.FullKey.Foreground(brandCyan)
	t.Help.ShortDesc = t.Help.ShortDesc.Foreground(brandMuted)
	t.Help.FullDesc = t.Help.FullDesc.Foreground(brandMuted)
	t.Help.ShortSeparator = t.Help.ShortSeparator.Foreground(brandMuted)
	t.Help.FullSeparator = t.Help.FullSeparator.Foreground(brandMuted)
	t.Help.Ellipsis = t.Help.Ellipsis.Foreground(brandMuted)

	// Blurred mirrors focused but hides the accent rail.
	t.Blurred = t.Focused
	t.Blurred.Base = t.Focused.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()

	t.Group.Title = t.Focused.Title
	t.Group.Description = t.Focused.Description
	return t
}

// rigColorScheme is the brand theme for fang's `--help`, usage, and error
// output. fang renders with lipgloss v2 and resolves a light/dark value per
// field via the LightDarkFunc it passes in, so we bridge each brand
// AdaptiveColor's two hex values through it. We start from fang's default scheme
// and override only the tokens the design system speaks to (verbs cyan, flags
// green, muted secondary text, errors red), leaving the rest of fang's layout
// defaults intact.
func rigColorScheme(c lgv2.LightDarkFunc) fang.ColorScheme {
	pick := func(a lipgloss.AdaptiveColor) color.Color {
		return c(lgv2.Color(a.Light), lgv2.Color(a.Dark))
	}
	cs := fang.DefaultColorScheme(c)
	cs.Title = pick(brandBlue)        // section headings
	cs.Program = pick(brandBlue)      // the `rig` program name
	cs.Command = pick(brandCyan)      // subcommand / verb names
	cs.Flag = pick(brandGreen)        // --flags
	cs.FlagDefault = pick(brandMuted) // (default: …) hints
	cs.Argument = pick(brandPaper)    // positional args
	cs.DimmedArgument = pick(brandMuted)
	cs.Description = pick(brandMuted) // flag/command descriptions
	cs.Comment = pick(brandMuted)
	cs.QuotedString = pick(brandAmber) // "quoted" literals
	cs.Help = pick(brandMuted)         // the bottom help hint
	cs.Dash = pick(brandMuted)
	cs.ErrorDetails = pick(brandRed)
	cs.ErrorHeader = [2]color.Color{lgv2.Color(brandPaper.Dark), pick(brandRed)}
	return cs
}
