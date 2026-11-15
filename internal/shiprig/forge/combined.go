package forge

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/core/semver"
)

// CombinedPlan is the one release forgeRelease mode "combined" makes for a set
// of released packages.
type CombinedPlan struct {
	Tag        string
	Name       string
	Notes      string
	Prerelease bool
	// Packages are the released packages in notes order: by display title,
	// ordinal, the order per-package releases are made in.
	Packages []plugin.Package
}

// PlanCombined works out the combined release for released (already filtered
// to packages that get tags). It fails when the templates use ${version} and
// the packages don't share one.
func PlanCombined(released []plugin.Package, fr config.ForgeRelease, repoRoot string) (CombinedPlan, error) {
	pkgs := append([]plugin.Package(nil), released...)
	sort.SliceStable(pkgs, func(i, j int) bool { return title(pkgs[i]) < title(pkgs[j]) })

	version, err := SharedVersion(pkgs, fr)
	if err != nil {
		return CombinedPlan{}, err
	}
	plan := CombinedPlan{Packages: pkgs}
	plan.Tag, plan.Name = fr.Render(version)

	sections := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		if isPrerelease(p.Version) {
			plan.Prerelease = true
		}
		section := "## " + title(p) + "@" + p.Version
		if notes := extractNotes(p, repoRoot); notes != "" {
			section += "\n\n" + notes
		}
		sections = append(sections, section)
	}
	plan.Notes = strings.Join(sections, "\n\n")
	return plan, nil
}

// SharedVersion returns the one version the packages share, for the combined
// release's ${version}. When neither template uses ${version} any mix of
// versions is fine and it returns "". Otherwise packages on different versions
// are an error naming each.
func SharedVersion(pkgs []plugin.Package, fr config.ForgeRelease) (string, error) {
	if !fr.UsesVersion() || len(pkgs) == 0 {
		return "", nil
	}
	version := pkgs[0].Version
	same := true
	for _, p := range pkgs[1:] {
		if p.Version != version {
			same = false
			break
		}
	}
	if same {
		return version, nil
	}
	named := make([]plugin.Package, len(pkgs))
	copy(named, pkgs)
	sort.SliceStable(named, func(i, j int) bool { return title(named[i]) < title(named[j]) })
	list := make([]string, 0, len(named))
	for _, p := range named {
		list = append(list, p.Name+" "+p.Version)
	}
	return "", fmt.Errorf("forgeRelease: the combined release's tag or name uses ${version}, but the released packages don't share one version (%s); "+
		"a changesets \"fixed\" group keeps them on one version, or drop ${version} from forgeRelease.tag and forgeRelease.name",
		strings.Join(list, ", "))
}

// isPrerelease reports whether version carries a prerelease label.
func isPrerelease(version string) bool {
	if v, ok := semver.Parse(version); ok {
		return v.Prerelease != ""
	}
	return strings.Contains(strings.SplitN(version, "+", 2)[0], "-")
}

// releaseCombined makes the one combined release for released.
func releaseCombined(req Request, fr config.ForgeRelease, released []plugin.Package, report func(...string)) ([]Result, bool, string) {
	plan, err := PlanCombined(released, fr, req.RepoRoot)
	if err != nil {
		return nil, false, err.Error()
	}
	names := make([]string, 0, len(plan.Packages))
	for _, p := range plan.Packages {
		names = append(names, p.Name)
	}
	// A prerelease is never Latest: GitHub refuses to mark one so.
	res := Result{Tag: plan.Tag, Name: plan.Name, Packages: names, Prerelease: plan.Prerelease, Latest: !plan.Prerelease}

	provider, skip := selectProvider(req.Selection, req.RepoRoot, req.Run)
	if provider == nil {
		return nil, true, skip
	}

	// The per-package tags are pushed by the tag/publish steps; the combined
	// tag is made here too when they didn't, so the release hangs off a tag at
	// the release commit rather than one the forge invents at its default
	// branch.
	if req.EnsureTag != nil {
		line, err := req.EnsureTag(plan.Tag, req.DryRun)
		if line != "" {
			report(line)
		}
		if err != nil {
			return nil, false, fmt.Sprintf("tag %s: %v", plan.Tag, err)
		}
	}

	if provider.ReleaseExists(plan.Tag, req.RepoRoot, req.Run) {
		report(plan.Tag + ": release already exists, skipped create.")
		res.Action = ActionExists
	} else {
		r := Release{Tag: plan.Tag, Title: plan.Name, Notes: plan.Notes, Prerelease: plan.Prerelease, Latest: res.Latest}
		if ok, msg := createRelease(provider, r, &res, req, report); !ok {
			return nil, false, fmt.Sprintf("release %s failed: %s", plan.Tag, msg)
		}
	}

	// Every released package's assets go on the one release, in notes order.
	var files []string
	seen := map[string]bool{}
	for _, p := range plan.Packages {
		for _, f := range req.Attach[p.Name] {
			if !seen[f] {
				seen[f] = true
				files = append(files, f)
			}
		}
	}
	if ok, msg := uploadAssets(provider, plan.Tag, files, req, report); !ok {
		return nil, false, fmt.Sprintf("release upload %s failed: %s", plan.Tag, msg)
	}
	lookUpURL(provider, &res, req)
	return []Result{res}, true, ""
}

// createRelease creates r, or in a dry run says it would, setting res.Action.
func createRelease(provider Provider, r Release, res *Result, req Request, report func(...string)) (bool, string) {
	argv := provider.CreateReleaseCmd(r)
	if req.DryRun {
		marks := ""
		if r.Latest {
			marks += ", latest"
		}
		if r.Prerelease {
			marks += ", prerelease"
		}
		report(fmt.Sprintf("would create release %s %q on %s%s, with notes:", r.Tag, r.Title, provider.Name(), marks))
		report(strings.Split(r.Notes, "\n")...)
		res.Action = ActionWouldCreate
		return true, ""
	}
	if ok, msg := runForgeCmd(argv, req.RepoRoot, req.Run, report); !ok {
		return false, msg
	}
	res.Action = ActionCreated
	return true, ""
}

// uploadAssets attaches files to the release for tag. On a forge that can't
// upload to an existing release (Gitea), it reports a skip rather than fail.
func uploadAssets(provider Provider, tag string, files []string, req Request, report func(...string)) (bool, string) {
	if len(files) == 0 {
		return true, ""
	}
	argv := provider.UploadAssetsCmd(tag, files)
	if argv == nil {
		report(fmt.Sprintf("%s: %s cannot attach assets to an existing release; skipped %d asset(s).", tag, provider.Name(), len(files)))
		return true, ""
	}
	if req.DryRun {
		report(fmt.Sprintf("%s: would attach %d asset(s).", tag, len(files)))
		return true, ""
	}
	return runForgeCmd(argv, req.RepoRoot, req.Run, report)
}

// lookUpURL fills res.URL when the run asked for URLs and the release exists.
func lookUpURL(provider Provider, res *Result, req Request) {
	if !req.WantURLs || res.Action == ActionWouldCreate {
		return
	}
	argv := provider.ReleaseURLCmd(res.Tag)
	if argv == nil {
		return
	}
	out, err := req.Run(req.RepoRoot, argv[0], argv[1:]...)
	if err != nil {
		return
	}
	if out = strings.TrimSpace(out); out != "" {
		res.URL = strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
	}
}
