package cli

import (
	"fmt"
	"os"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/internal/rig/config"
)

// pickedEcosystem caches an interactively-chosen primary ecosystem per repo
// root, so an ambiguous repo is prompted at most once per process — even when
// the user declines to persist the choice.
var pickedEcosystem = map[string]string{}

// pickPrimaryEcosystem resolves an ambiguous repo's primary ecosystem by asking,
// on an interactive terminal: a select over the coexisting ecosystems, then an
// offer to remember the choice in .rig.json. Returns ok=false off a TTY or on
// cancel, so resolvePrimary falls back to its "set ecosystem" error.
func pickPrimaryEcosystem(root string, candidates []string) (string, bool) {
	if !interactive() {
		return "", false
	}
	opts := make([]huh.Option[string], 0, len(candidates))
	for _, eco := range candidates {
		opts = append(opts, huh.NewOption(ecoDisplayName(eco), eco))
	}
	var chosen string
	remember := true
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Which ecosystem should rig use here?").
			Description("Several coexist in this repo — pick the primary one.").
			Options(opts...).
			Value(&chosen),
		huh.NewConfirm().
			Title("Remember this in "+config.FileName+"?").
			Value(&remember),
	)).WithKeyMap(huhEscKeyMap()).WithTheme(rigTheme())
	if err := form.Run(); err != nil {
		return "", false // esc / ctrl+c → cancelled
	}
	if remember {
		if _, ok := config.SetRepoString(root, "ecosystem", chosen); ok {
			fmt.Fprintln(os.Stderr, dimStyle.Render("set ecosystem = "+chosen+" in "+config.FileName))
		} else {
			fmt.Fprintln(os.Stderr, dimStyle.Render("could not save ecosystem to "+config.FileName))
		}
	}
	return chosen, true
}
