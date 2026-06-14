package planner

import (
	"context"

	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/plugin"
)

// BuiltinGenerator is the in-process reference implementation of
// plugin.ChangelogGenerator. It is the default ("default" id) and the renderer
// every built-in path goes through, so the changelog plugin contract can't
// silently grow gaps that only external plugins would hit.
type BuiltinGenerator struct {
	groups []config.ChangelogGroup
}

// NewBuiltinGenerator returns the built-in generator using the given changelog
// groups (nil falls back to the conventional defaults).
func NewBuiltinGenerator(groups []config.ChangelogGroup) BuiltinGenerator {
	if groups == nil {
		groups = config.DefaultChangelogGroups
	}
	return BuiltinGenerator{groups: groups}
}

// ID identifies the built-in generator.
func (BuiltinGenerator) ID() string { return "default" }

// Render produces the rendered release entry from a ChangelogRequest — the same
// value (and contract) a subprocess generator returns on stdout.
func (g BuiltinGenerator) Render(_ context.Context, req plugin.ChangelogRequest) (string, error) {
	groups := g.groups
	if groups == nil {
		groups = config.DefaultChangelogGroups
	}
	out := renderSections(req.Package.NewVersion, req.Changes, groups)
	out += renderContributors(req.Contributors, req.ContributorsSection)
	return out, nil
}

var _ plugin.ChangelogGenerator = BuiltinGenerator{}

// Builtins returns the built-in changelog generators keyed by id, for
// plugin.ResolveChangelogGenerator, using the given groups.
func Builtins(groups []config.ChangelogGroup) map[string]plugin.ChangelogGenerator {
	return map[string]plugin.ChangelogGenerator{"default": NewBuiltinGenerator(groups)}
}
