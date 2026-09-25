package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/rigsmith/rigsmith/core/auth"
	"github.com/rigsmith/rigsmith/core/gitutil"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/core/prestate"
	"github.com/rigsmith/rigsmith/internal/changerig/commands"
	"github.com/spf13/cobra"
)

// The publish plan, in @changesets v3's file format (see
// docs/PUBLISH-PLAN-DESIGN.md): what a publish would do, chunked so each
// chunk's packages depend only on earlier chunks'. `pack` and
// `publish --from-pack-dir` read the same file.

// publishPlanVersion is the plan file's format version, as canon's.
const publishPlanVersion = 1

// planRelease is one entry of the plan. Kind is "publish" (not on its
// registry yet) or "tag-only" (released by its git tag alone, and the tag is
// missing). Tag is the npm dist-tag, as canon has it. Ecosystem is
// shiprig's own addition, which canon's readers ignore.
type planRelease struct {
	Kind      string      `json:"kind"`
	Name      string      `json:"name"`
	Version   string      `json:"version"`
	Access    string      `json:"access,omitempty"`
	Tag       string      `json:"tag,omitempty"`
	Ecosystem string      `json:"ecosystem,omitempty"`
	Tarball   *packedFile `json:"tarball,omitempty"` // set by pack, as canon's
}

// packedFile is where pack put a release's package file, relative to the
// pack directory, and its sha256 integrity ("sha256-<base64>"), as canon's
// tarball entry has it. The name stays "tarball" whatever the ecosystem (a
// .nupkg too), so canon's readers find it.
type packedFile struct {
	Path      string `json:"path"`
	Integrity string `json:"integrity"`
}

// readPublishPlan reads a plan file, as canon's readPlanFile does: an object
// with version 1 and a plan array.
func readPublishPlan(path string) ([][]planRelease, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file struct {
		Version *int             `json:"version"`
		Plan    *[][]planRelease `json:"plan"`
	}
	if err := json.Unmarshal(data, &file); err != nil || file.Plan == nil {
		return nil, fmt.Errorf("%s: not a publish plan file", path)
	}
	if file.Version == nil || *file.Version != publishPlanVersion {
		v := "none"
		if file.Version != nil {
			v = fmt.Sprint(*file.Version)
		}
		return nil, fmt.Errorf("%s: publish plan file version %s, expected %d", path, v, publishPlanVersion)
	}
	return *file.Plan, nil
}

type publishPlanFile struct {
	Version int             `json:"version"`
	Plan    [][]planRelease `json:"plan"`
}

func newPublishPlanCmd() *cobra.Command {
	var (
		output string
		tag    string
	)
	cmd := &cobra.Command{
		Use:   "publish-plan",
		Short: "Show (or write) what a publish would release, as @changesets' publish-plan",
		Long: `Ask each package's registry whether its version is already there, and list
the ones a publish would push ("publish"), plus the packages released by their
git tag alone whose tag is missing ("tag-only"). With --output, write the plan
as @changesets v3's publish-plan JSON, for ` + "`pack`" + ` and
` + "`publish --from-pack-dir`" + ` to read.

A registry that can't be reached fails the command: the plan is never a guess.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := commands.Open()
			if err != nil {
				return err
			}
			// The same environment a publish sees, so a registry URL or
			// credential set in .env is asked the same way.
			if _, err := applyReleaseEnv(ws.Root, noEnv); err != nil {
				return err
			}
			plan, err := buildPublishPlan(cmd.Context(), ws, tag)
			if err != nil {
				return err
			}
			if output != "" {
				return writePublishPlan(output, plan)
			}
			printPublishPlan(cmd.OutOrStdout(), plan)
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "write the plan as @changesets publish-plan JSON to this file")
	cmd.Flags().StringVar(&tag, "tag", "", "the npm dist-tag to publish under (default: the prerelease tag in pre mode, else latest)")
	return cmd
}

// buildPublishPlan works out what a publish would release, in dependency
// order. Every package's registry is asked concurrently (bounded, as a dry-run
// publish is); any error fails the plan.
func buildPublishPlan(ctx context.Context, ws *commands.Workspace, distTag string) ([][]planRelease, error) {
	pkgs, ecoOf, err := ws.Discover(ctx)
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(pkgs))
	for _, p := range pkgs {
		known[p.Name] = true
	}
	gen, err := generatedPackages(ws.Root, ws.Config, known)
	if err != nil {
		return nil, err
	}
	for name, eco := range gen.Eco {
		ecoOf[name] = eco
	}
	generated := make(map[string]bool, len(gen.Packages))
	for _, p := range gen.Packages {
		generated[p.Name] = true
	}
	pre, err := prestate.Read(ws.ChangesetDir)
	if err != nil {
		return nil, err
	}
	if distTag == "" {
		distTag = "latest"
		if pre != nil && pre.Mode == prestate.ModePre {
			if strings.TrimSpace(pre.Tag) == "" {
				return nil, fmt.Errorf("%s/pre.json is in pre mode with no tag: set its tag, or pass --tag", ws.ChangesetDir)
			}
			distTag = pre.Tag
		}
	}

	candidates := make([]plugin.Package, 0, len(pkgs)+len(gen.Packages))
	candidates = append(candidates, pkgs...)
	candidates = append(candidates, gen.Packages...)
	solo := singleApp(pkgs)
	remote := gitutil.DefaultRemote(ctx, ws.Root)

	// tagOnly is the entry for a package released by its tag alone, or nil
	// when it's never tagged or the tag is already there.
	tagOnly := func(p plugin.Package) *planRelease {
		if generated[p.Name] || ws.Config.SkipsTag(p.Name) {
			return nil
		}
		t := gitutil.RenderTag(ws.Config.TagTemplate, ecoOf[p.Name], p.Dir, p.Name, p.Version, solo)
		if gitutil.TagExists(ctx, ws.Root, t) || (remote != "" && gitutil.RemoteTagExists(ctx, ws.Root, remote, t)) {
			return nil
		}
		return &planRelease{Kind: "tag-only", Name: p.Name, Version: p.Version, Ecosystem: ecoOf[p.Name]}
	}

	// Each ecosystem's configured credential, for a registry that won't
	// answer an anonymous read. Resolved once, up front, so a secret manager
	// prompts once; one that can't be resolved (a plan job without the
	// secret) sends none, and is named if the registry then asks for it.
	redactor := auth.NewRedactor()
	readCreds := map[string]*plugin.AuthCredential{}
	credErrs := map[string]error{}
	credCache := map[string]*plugin.AuthCredential{}
	for _, p := range candidates {
		eco := ecoOf[p.Name]
		if _, done := readCreds[eco]; done || credErrs[eco] != nil || ws.Config.EcoConfig(eco).Auth == "" {
			continue
		}
		cred, _, err := resolvePublishCreds(ctx, ws.Config, eco, "", credCache, redactor)
		if err != nil {
			credErrs[eco] = err
			continue
		}
		readCreds[eco] = cred
	}

	entries := make([]*planRelease, len(candidates))
	errs := make([]error, len(candidates))
	sem := make(chan struct{}, dryRunProbeLimit)
	var wg sync.WaitGroup
	for i, p := range candidates {
		// A private package never reaches a registry; with privatePackages.tag
		// it is still released by its tag, as canon's plan has it.
		if p.Private {
			entries[i] = tagOnly(p)
			continue
		}
		if ws.Config.IsIgnored(p.Name) {
			continue
		}
		eco, ok := ws.EcosystemFor(ecoOf[p.Name])
		if !ok {
			continue
		}
		wg.Add(1)
		go func(i int, p plugin.Package, eco plugin.Ecosystem) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ecoID := ecoOf[p.Name]
			resp, err := eco.Published(ctx, plugin.PublishedRequest{
				RepoRoot:        ws.Root,
				Package:         p,
				PackageSource:   packageSourceFor(ws.Config, ecoID),
				Auth:            readCreds[ecoID],
				User:            ws.Config.EcoConfig(ecoID).User,
				AuthUnavailable: credErrs[ecoID] != nil,
			})
			switch {
			case err != nil:
				// An adapter's error can carry a registry URL with credentials
				// in it (npm echoes --registry); keep them out of the output,
				// and any resolved token too.
				msg := redactor.Redact(redactURLCredentials(err.Error()))
				if credErr := credErrs[ecoID]; credErr != nil {
					msg += fmt.Sprintf(" (the configured `%s.auth` couldn't be resolved: %s)", ecoID, redactor.Redact(credErr.Error()))
				}
				errs[i] = fmt.Errorf("%s: %s", p.Name, msg)
			case resp.NoRegistry, resp.Published:
				// Released by its tag alone, or already published: either
				// way the tag may still be missing (a push that failed after
				// the upload, say), and publish would create it, so the plan
				// lists it for the job that does.
				entries[i] = tagOnly(p)
			case !resp.Published:
				access := ws.Config.Access
				if a, ok := gen.Access[p.Name]; ok && a != "" {
					access = a
				}
				entries[i] = &planRelease{Kind: "publish", Name: p.Name, Version: p.Version, Access: access, Tag: distTag, Ecosystem: ecoID}
			}
		}(i, p, eco)
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("couldn't tell what is already published:\n%w", err)
	}

	var releases []planRelease
	byName := map[string]plugin.Package{}
	for i, e := range entries {
		if e != nil {
			releases = append(releases, *e)
			byName[e.Name] = candidates[i]
		}
	}
	return chunkByDependencies(releases, byName), nil
}

// chunkByDependencies orders releases so each chunk's packages depend (dev
// dependencies aside, as canon) only on packages in earlier chunks. A cycle
// can't be ordered inside itself, so its members go out together, in a chunk
// of their own once nothing outside the cycle is still waiting; a package
// depending on the cycle comes after it.
func chunkByDependencies(releases []planRelease, pkgOf map[string]plugin.Package) [][]planRelease {
	pending := map[string]planRelease{}
	for _, r := range releases {
		pending[r.Name] = r
	}
	// waitsOn is what name still waits for: its pending, non-dev dependencies.
	waitsOn := func(name string) []string {
		var out []string
		for _, d := range pkgOf[name].Dependencies {
			if _, ok := pending[d.Name]; ok && d.Kind != plugin.DepDev && d.Name != name {
				out = append(out, d.Name)
			}
		}
		return out
	}
	var chunks [][]planRelease
	for len(pending) > 0 {
		var ready []string
		for name := range pending {
			if len(waitsOn(name)) == 0 {
				ready = append(ready, name)
			}
		}
		if len(ready) == 0 {
			ready = firstCycle(pending, waitsOn)
		}
		sort.Strings(ready)
		chunk := make([]planRelease, 0, len(ready))
		for _, name := range ready {
			chunk = append(chunk, pending[name])
		}
		for _, name := range ready {
			delete(pending, name)
		}
		chunks = append(chunks, chunk)
	}
	return chunks
}

// firstCycle picks, when nothing pending is free to go, a cycle whose members
// wait on nothing outside it: the set of packages that reach each other
// through what they wait on, closed to everything else. One always exists
// when every package waits on something. Packages are tried in name order,
// so the choice is stable.
func firstCycle(pending map[string]planRelease, waitsOn func(string) []string) []string {
	reach := func(from string) map[string]bool {
		seen := map[string]bool{}
		stack := []string{from}
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, d := range waitsOn(n) {
				if !seen[d] {
					seen[d] = true
					stack = append(stack, d)
				}
			}
		}
		return seen
	}
	names := make([]string, 0, len(pending))
	for n := range pending {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fromN := reach(n)
		if !fromN[n] {
			continue // not on a cycle: it waits on one
		}
		// n's cycle: what n reaches that reaches n back.
		var members []string
		inCycle := map[string]bool{}
		for m := range fromN {
			if reach(m)[n] {
				members = append(members, m)
				inCycle[m] = true
			}
		}
		// Closed: nothing a member waits on lies outside the cycle.
		closed := true
		for _, m := range members {
			for _, d := range waitsOn(m) {
				if !inCycle[d] {
					closed = false
				}
			}
		}
		if closed {
			return members
		}
	}
	return names // unreachable when every package waits on something
}

func writePublishPlan(path string, plan [][]planRelease) error {
	if plan == nil {
		plan = [][]planRelease{}
	}
	data, err := json.MarshalIndent(publishPlanFile{Version: publishPlanVersion, Plan: plan}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func printPublishPlan(w io.Writer, plan [][]planRelease) {
	var publish, tagOnly []string
	for _, chunk := range plan {
		for _, r := range chunk {
			if r.Kind == "publish" {
				publish = append(publish, fmt.Sprintf("- %s@%s (%s)", r.Name, r.Version, r.Tag))
			} else {
				tagOnly = append(tagOnly, fmt.Sprintf("- %s@%s", r.Name, r.Version))
			}
		}
	}
	if len(publish) == 0 && len(tagOnly) == 0 {
		fmt.Fprintln(w, commands.DimStyle.Render("No projects to publish or tag."))
		return
	}
	if len(publish) > 0 {
		fmt.Fprintf(w, "Packages to publish:\n%s\n", strings.Join(publish, "\n"))
	}
	if len(tagOnly) > 0 {
		fmt.Fprintf(w, "Packages to tag:\n%s\n", strings.Join(tagOnly, "\n"))
	}
}

// urlCredentials matches the user[:password]@ part of a URL, up to the last
// '@' before the path: a password can hold an unencoded '@'.
var urlCredentials = regexp.MustCompile(`(://)[^/?#\s]*@`)

// redactURLCredentials masks credentials embedded in any URL in s.
func redactURLCredentials(s string) string {
	return urlCredentials.ReplaceAllString(s, "${1}***@")
}
