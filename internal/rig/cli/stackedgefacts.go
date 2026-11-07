package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/gitrepo"
)

type stackEdgeSetup struct {
	ctx     context.Context
	root    string
	m       *stackManifest
	out     io.Writer
	in      io.Reader
	api     stackDepsAPI
	authRef string
	tty     func() bool
	prompt  func(*huh.Confirm) error
	pack    func(context.Context, io.Writer, string, string) error
	run     func(context.Context, string, []string, io.Reader) ([]byte, error)
	now     func() time.Time
	sleep   func(context.Context, time.Duration) error
}

type stackEdgeSetupFact struct {
	id, description, action string
	check                   func(string) error
	repair                  func(string) error
}

// This is the single ordered fact list for init and doctor. A repair is never
// considered successful until the same check runs again.
func (s *stackEdgeSetup) facts() []stackEdgeSetupFact {
	facts := []stackEdgeSetupFact{
		{"a", "manifest edge block, trackBranch and publishing coverage", "print the required manifest edits; never write repos", s.checkManifest, nil},
		{"b", "fork trackBranch holds the integration line and all recorded topics", "run rig stack propose <member> --track-only (confirmed)", s.checkTrack, s.repairTrack},
		{"c", "republished references are redirected and the overlay is current", "run rig stack wire at the root, without editing members", s.checkWiring, s.repairWiring},
		{"d", "local unpublishable pack verifies nuspecs and reports sizes", "reconstruct and build in temporary directories with number 0; runs project code and can take a while", func(name string) error { return s.pack(s.ctx, s.out, s.root, name) }, nil},
	}
	// FactEWorkflowPublicationInsertionPoint: add the workflow/home publication
	// fact here once its template lands. There is deliberately no passing stub.
	facts = append(facts,
		stackEdgeSetupFact{"f", "publishing repo exists; upstream fork workflows are disabled", "configure the default manifest home with rig stack seed --publish; create an explicit publishFrom repo if missing; disable each non-edge fork workflow (each confirmed and read back)", s.checkPublishingRepo, s.repairPublishingRepo},
		stackEdgeSetupFact{"g", "publishing repo has the push secret", "set the missing push secret from stdin or --push-auth (confirmed), retry 5xx up to 3 tries, read back fresh metadata", s.checkSecret, s.repairSecret},
		stackEdgeSetupFact{"h", "external consumer package source mapping", "print a nuget.config snippet; never write inside a member", s.printConsumerConfig, nil},
	)
	return facts
}

func (s *stackEdgeSetup) checkFacts(names []string, repair, preview bool) error {
	var failures []error
	for _, name := range names {
		manifestOK := true
		for _, fact := range s.facts() {
			fmt.Fprintf(s.out, "%s (%s): %s\n", name, fact.id, fact.description)
			if !manifestOK {
				fmt.Fprintf(s.out, "  blocked by manifest edits; would %s\n", fact.action)
				continue
			}
			// A dry run of setup must not build or repair anything, including local
			// overlay changes. All other fact checks are reads.
			if preview && fact.id == "d" {
				fmt.Fprintf(s.out, "  would %s\n", fact.action)
				continue
			}
			err := fact.check(name)
			if err == nil {
				if fact.id != "h" {
					fmt.Fprintln(s.out, "  ready")
				}
				continue
			}
			fmt.Fprintf(s.out, "  %v\n  would %s\n", err, strings.ReplaceAll(fact.action, "<member>", name))
			if fact.id == "a" {
				manifestOK = false
			}
			if repair && !preview && fact.repair != nil {
				if err = fact.repair(name); err == nil {
					err = fact.check(name)
				}
				if err == nil {
					fmt.Fprintln(s.out, "  ready (checked again)")
					continue
				}
				fmt.Fprintf(s.out, "  %v\n", err)
			}
			failures = append(failures, fmt.Errorf("%s (%s): %w", name, fact.id, err))
		}
	}
	return errors.Join(failures...)
}

func (s *stackEdgeSetup) checkManifest(name string) error {
	r, e := s.m.Repos[name], s.m.Edge[name]
	var edits []string
	if e == nil {
		edits = append(edits, fmt.Sprintf(`add "edge": {%q: {"feed": "https://feed.example/index.json", "pushKeySecret": "EDGE_FEED_KEY"}} at the manifest root`, name))
	}
	if r.TrackBranch == "" {
		edits = append(edits, fmt.Sprintf(`set repos.%s.trackBranch to %q`, name, s.m.sendBranch(name, "integration")))
	}
	if r.PublishPrefix == "" && len(r.PublishesAs) == 0 {
		edits = append(edits, fmt.Sprintf(`set repos.%s.publishPrefix to "You." (or publishesAs for every package)`, name))
	}
	if len(edits) > 0 {
		return fmt.Errorf("manifest edit needed: %s", strings.Join(edits, "; "))
	}
	scans := stackScan(s.ctx, s.root, stackScanOptions{Registry: true, IncludeOverlays: true})
	var all []stackPackPackage
	for _, scan := range scans {
		if scan.Err != nil {
			return fmt.Errorf("publishing coverage cannot be checked: %w", scan.Err)
		}
		for _, pkg := range scan.Packages {
			all = append(all, stackPackPackage{eco: scan.Eco, info: scan.Eco.Info(), pkg: pkg})
		}
	}
	pkgs := slices.DeleteFunc(stackPackFor(stackPackReconcileOverlays(all), name), func(p stackPackPackage) bool { return p.pkg.Private })
	if _, err := stackEdgeResolveIDs(pkgs, s.m.publishing()[name], e.Exclude); err != nil {
		return fmt.Errorf("manifest edit needed: cover every non-excluded package with repos.%s.publishPrefix or publishesAs: %w", name, err)
	}
	return nil
}

func (s *stackEdgeSetup) checkTrack(name string) error {
	repo := &gitrepo.Repo{Dir: s.root}
	for _, topic := range slices.Sorted(maps.Keys(s.m.Proposals[name])) {
		// Unlike propose's repair, the fact cannot assert that a deleted topic is
		// contained. Restore it or remove the obsolete proposal record explicitly.
		if err := stackRequireIntegrationTopic(s.ctx, repo, topic); err != nil {
			return err
		}
	}
	receipt, err := repo.RevParse(s.ctx, "refs/rigsmith/integration/"+name)
	if err != nil {
		return fmt.Errorf("missing integration receipt; run rig stack propose %s --track-only", name)
	}
	tree, err := repo.RevParse(s.ctx, receipt+"^{tree}")
	if err != nil {
		return err
	}
	head, err := repo.RevParse(s.ctx, "HEAD:"+name)
	if err != nil {
		return err
	}
	if tree != head {
		return fmt.Errorf("integration receipt tree differs from HEAD:%s", name)
	}
	r := s.m.Repos[name]
	ref := "refs/heads/" + r.TrackBranch
	found, err := repo.LsRemoteRefs(s.ctx, stackRemoteURL(r.Fork), stackAuthFor(s.ctx, r.Fork), ref)
	if err != nil {
		return err
	}
	if found[ref] == "" || found[ref] != receipt {
		return fmt.Errorf("fork trackBranch is missing or differs from integration receipt")
	}
	return nil
}

func (s *stackEdgeSetup) repairTrack(name string) error {
	if err := s.confirm("run rig stack propose " + name + " --track-only on the PR fork"); err != nil {
		return err
	}
	cmd := newStackSendCmd()
	if err := cmd.Flags().Set("track-only", "true"); err != nil {
		return err
	}
	cmd.SetOut(s.out)
	cmd.SetErr(s.out)
	cmd.SetContext(s.ctx)
	return cmd.RunE(cmd, []string{name})
}

func (s *stackEdgeSetup) memberSafeManifest() *stackManifest {
	m := *s.m
	m.Repos = maps.Clone(s.m.Repos)
	for name, r := range m.Repos {
		copy := *r
		copy.Owned = false
		m.Repos[name] = &copy
	}
	return &m
}

func (s *stackEdgeSetup) checkWiring(_ string) error {
	if missing := stackMissingPrefixes(s.root, s.m.names()); len(missing) > 0 {
		return fmt.Errorf("members not imported: %s", strings.Join(missing, ", "))
	}
	scans := stackScan(s.ctx, s.root, stackScanOptions{Registry: true})
	links, _, _, failed := stackRedirectsFromScan(scans, s.m.names(), s.m.publishing())
	if len(failed) != 0 {
		return fmt.Errorf("cannot check redirects: an ecosystem scan failed")
	}
	// Check every discovered reference to every republished id, including clashes
	// that stackRedirects deliberately declines to link.
	aliases := map[string]bool{}
	for _, scan := range scans {
		for _, p := range scan.Packages {
			for name, pub := range s.m.publishing() {
				if stackPackUnder(name, p.Dir) {
					if as, ok := stackRepublishedID(pub, p.Name); ok {
						aliases[as] = true
					}
				}
			}
		}
	}
	for _, scan := range scans {
		for _, p := range scan.Packages {
			from := ""
			for _, member := range s.m.names() {
				if stackPackUnder(member, p.Dir) {
					from = member
					break
				}
			}
			if from == "" {
				continue
			}
			for _, d := range p.Dependencies {
				if !d.ViaRegistry || !aliases[d.Name] {
					continue
				}
				linked := slices.ContainsFunc(links[scan.Eco.Info().ID], func(l stackLink) bool { return l.Package == d.Name && slices.Contains(l.From, from) })
				if !linked {
					return fmt.Errorf("reference to republished id %s in %s has no stackRedirects link", d.Name, p.ManifestPath)
				}
			}
		}
	}
	reports, _, _, failures := stackCheckOverlay(s.ctx, s.root, s.memberSafeManifest())
	if len(failures) != 0 {
		return fmt.Errorf("cannot check overlay: an ecosystem scan failed")
	}
	for _, rep := range reports {
		if len(rep.Resp.Problems) > 0 {
			return fmt.Errorf("overlay needs rig stack wire: %s", rep.Resp.Problems[0].Message)
		}
	}
	return nil
}

func (s *stackEdgeSetup) repairWiring(_ string) error {
	// wire may remove stale root overlays. Confirmation also covers that local
	// destructive action; owned members get no special exemption from edge's rule.
	if err := s.confirm("run rig stack wire at the root without editing any member"); err != nil {
		return err
	}
	_, err := stackWire(s.ctx, s.out, s.memberSafeManifest(), &gitrepo.Repo{Dir: s.root}, "", true)
	return err
}

func (s *stackEdgeSetup) publishingRepo(name string) (string, error) {
	spec := s.m.Edge[name].PublishFrom
	if spec == "" {
		spec = stackNormalizeSpec(stackHomeConfig(s.ctx, s.root, "URL"))
	}
	host, slug, ok := strings.Cut(spec, "/")
	if !ok || host != "github.com" || strings.Count(slug, "/") != 1 {
		return "", fmt.Errorf("configure a GitHub manifest home or edge.publishFrom (the PR fork is not the publishing default)")
	}
	if err := stackValidRepoSpec(spec); err != nil {
		return "", err
	}
	// Match the repo's existing API host policy, allowing loopback fixtures.
	u, err := url.Parse(s.api.base)
	if err != nil || (u.Hostname() != "api.github.com" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1") {
		return "", fmt.Errorf("API host does not match publishing repo")
	}
	return slug, nil
}

func (s *stackEdgeSetup) printConsumerConfig(name string) error {
	e := s.m.Edge[name]
	patterns := map[string]bool{}
	pub := s.m.publishing()[name]
	if pub.Prefix != "" {
		patterns[pub.Prefix+"*"] = true
	}
	for _, as := range pub.As {
		patterns[as] = true
	}
	fmt.Fprintln(s.out, "  Add to the external consumer's nuget.config (printed only; no member is edited):")
	fmt.Fprintln(s.out, "<configuration>\n  <packageSources>")
	fmt.Fprintf(s.out, "    <add key=\"edge\" value=\"%s\" />\n", stackEdgeXML(e.Feed))
	fmt.Fprintln(s.out, "    <add key=\"nuget.org\" value=\"https://api.nuget.org/v3/index.json\" />\n  </packageSources>\n  <packageSourceMapping>\n    <packageSource key=\"edge\">")
	for _, pattern := range slices.Sorted(maps.Keys(patterns)) {
		fmt.Fprintf(s.out, "      <package pattern=\"%s\" />\n", stackEdgeXML(pattern))
	}
	fmt.Fprintln(s.out, "    </packageSource>\n    <packageSource key=\"nuget.org\"><package pattern=\"*\" /></packageSource>\n  </packageSourceMapping>\n</configuration>")
	return nil
}

func stackEdgeXML(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(s)
}
