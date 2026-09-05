package commands

import (
	"fmt"
	"io"

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

// printUnversionedNote says, under a package listing, where the version of a
// package with none in the tree comes from and where the release puts it.
func printUnversionedNote(out io.Writer, pkgs []plugin.Package) {
	n := 0
	for _, p := range pkgs {
		if p.Version == "" {
			n++
		}
	}
	if n == 0 {
		return
	}
	fmt.Fprintln(out, DimStyle.Render(fmt.Sprintf("  %d package(s) carry no version in the tree (computed at build time); `version` records the number it computes in .changeset/%s, and bumps from there next time.", n, versionstate.FileName)))
}
