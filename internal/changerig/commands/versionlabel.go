package commands

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/core/versionstate"
)

// versionLabel renders a package's current version for a listing. A package
// whose manifest carries no version — one computed at build time, MinVer from
// git tags or a CI stamp — has nothing to show until a release has recorded
// one, and an empty column would read as a rendering bug rather than a fact.
func versionLabel(version string) string {
	if version == "" {
		return "(no version in the tree)"
	}
	return version
}

// printUnversionedNote names, under a listing or a plan, the packages with no
// version anywhere yet — none in the tree (computed at build time) and none
// recorded — so a plan that starts them from 0.0.0 is not mistaken for a
// package that really is at 0.0.0, and says where the number comes from.
func printUnversionedNote(out io.Writer, pkgs []plugin.Package) {
	var names []string
	for _, p := range pkgs {
		if p.Version == "" {
			names = append(names, p.Name)
		}
	}
	if len(names) == 0 {
		return
	}
	sort.Strings(names)
	fmt.Fprintln(out, DimStyle.Render(fmt.Sprintf("  no version in the tree and none recorded for %s (computed at build time): a plan starts from 0.0.0 — seed .changeset/%s with the current version, or type it at the override prompt; `version` records what it computes there from then on.", strings.Join(names, ", "), versionstate.FileName)))
}
