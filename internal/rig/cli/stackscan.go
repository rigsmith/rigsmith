package cli

import (
	"context"

	"github.com/rigsmith/rigsmith/core/ecosystem"
	"github.com/rigsmith/rigsmith/core/plugin"
)

// stackScanOptions says how a stackspace-wide discovery should be run. The two
// consumers want genuinely different things from the same walk, and the
// differences are the whole reason this is a parameter rather than a constant.
type stackScanOptions struct {
	// IncludeOverlays asks the adapters that sit on top of a base language too.
	//
	// The link scan must not: an overlay adapter re-emits the base language's
	// project, so asking both counts one project twice. Anything that builds
	// artifacts must: Overlays means the overlay adapter *owns* that unit's
	// artifacts, so leaving it out builds the wrong thing — an Electron app
	// npm-packed instead of producing installers.
	IncludeOverlays bool
	// Registry widens discovery to packages with no version in the tree and to
	// siblings referenced the way an outside consumer would. That is what the
	// redirect analysis is looking for; a build wants the packages as they are.
	Registry bool
}

// stackScanResult is one ecosystem's answer. Err is kept rather than folded
// into a shared failure, because the two consumers disagree about what a failed
// scan means: the link report has to carry on and say which ecosystems it could
// not see (silence there would read as "nothing crosses"), while a build must
// stop rather than pack against a partial picture.
type stackScanResult struct {
	Eco      plugin.Ecosystem
	Packages []plugin.Package
	Err      error
}

// stackScan discovers packages across the stackspace, one entry per ecosystem
// that detected in it — plus one for an ecosystem whose Detect itself failed,
// since "could not look" is an answer a caller has to be able to see.
//
// Shared so that scan eligibility cannot drift between the code that works out
// what to redirect and the code that works out what to build. They differ in
// what they ask for, which is what the options are; they must not differ in
// which ecosystems get asked.
func stackScan(ctx context.Context, root string, opts stackScanOptions) []stackScanResult {
	var out []stackScanResult
	for _, eco := range ecosystem.Default().All() {
		if !opts.IncludeOverlays && len(eco.Info().Overlays) > 0 {
			continue
		}
		ok, err := eco.Detect(ctx, root)
		if err != nil {
			out = append(out, stackScanResult{Eco: eco, Err: err})
			continue
		}
		if !ok {
			continue
		}
		resp, err := eco.Discover(ctx, plugin.DiscoverRequest{
			RepoRoot:                root,
			SourcePath:              ".",
			IncludeUnversioned:      opts.Registry,
			IncludeRegistrySiblings: opts.Registry,
		})
		if err != nil {
			out = append(out, stackScanResult{Eco: eco, Err: err})
			continue
		}
		out = append(out, stackScanResult{Eco: eco, Packages: resp.Packages})
	}
	return out
}
