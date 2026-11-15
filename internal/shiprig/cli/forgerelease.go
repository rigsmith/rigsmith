package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/envstack"
	"github.com/rigsmith/rigsmith/core/gitutil"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/internal/changerig/commands"
	"github.com/rigsmith/rigsmith/internal/shiprig/forge"
	"github.com/spf13/cobra"
)

// newForgeRunner is how forge-release reaches the forge CLI: a variable so a
// test can answer for gh.
var newForgeRunner = execForgeRunner

// forgeReleaseOutput is `shiprig forge-release --json`.
type forgeReleaseOutput struct {
	Mode     string         `json:"mode"`
	DryRun   bool           `json:"dryRun"`
	Releases []forge.Result `json:"releases"`
	// Message says why nothing was released (no forge, nothing at HEAD), or
	// what failed.
	Message string `json:"message,omitempty"`
}

// newForgeReleaseCmd builds `forge-release`: the release pipeline's forge
// `release` step on its own, for CI that publishes with `shiprig publish` and
// then wants the forge releases for exactly what that released. What was
// released is read from the tags at HEAD: the release commit's per-package
// tags, which publish has just created there.
func newForgeReleaseCmd() *cobra.Command {
	var (
		dryRun   bool
		jsonOut  bool
		forgeArg string
	)
	cmd := &cobra.Command{
		Use:   "forge-release",
		Short: "Create the forge release(s) for the packages tagged at HEAD",
		Long: "Create the GitHub/GitLab/Gitea release(s) for the packages released at the\n" +
			"current commit (those whose tag points at HEAD), under the forgeRelease mode\n" +
			"in .changeset/config.json: one release per package (default), or one combined\n" +
			"release. Existing releases are skipped, so a re-run is safe. Run it after\n" +
			"`shiprig publish`; it attaches no build assets.",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			// With --json, stdout carries only the JSON; progress goes to stderr.
			progress := out
			if jsonOut {
				progress = cmd.ErrOrStderr()
			}
			result := forgeReleaseOutput{Mode: config.ForgeReleasePerPackage, DryRun: dryRun, Releases: []forge.Result{}}
			finish := func(ok bool) error {
				if jsonOut {
					enc := json.NewEncoder(out)
					enc.SetIndent("", "  ")
					if err := enc.Encode(result); err != nil {
						return err
					}
				} else if result.Message != "" && ok {
					fmt.Fprintln(out, result.Message)
				}
				if !ok {
					cmd.SilenceUsage = true
					return errors.New(result.Message)
				}
				return nil
			}
			// Every failure goes out through finish, so --json always prints
			// the envelope, with the failure in its message.
			fail := func(err error) error {
				result.Message = err.Error()
				return finish(false)
			}

			ws, err := commands.Open()
			if err != nil {
				return fail(err)
			}
			if ws.Config.ForgeRelease.Combined() {
				result.Mode = config.ForgeReleaseCombined
			}
			if err := ws.Config.ForgeRelease.Validate(); err != nil {
				return fail(err)
			}
			if ws.Stackspace != nil {
				result.Message = "stackspace: a fused history is not released — nothing to do"
				return finish(true)
			}

			sel := forge.Selection{Forge: forgeArg}
			if !cmd.Flags().Changed("forge") {
				rcfg, err := resolveReleaseConfig(ws.Root, ws.ChangesetDir)
				if err != nil {
					return fail(err)
				}
				sel = forge.Selection{Forge: stepForge(rcfg), URL: stepForgeURL(rcfg)}
			}
			releaseEnv, err := loadReleaseEnv(ws.Root, noEnv)
			if err != nil {
				return fail(err)
			}
			run := newForgeRunner(cmd, envstack.Environ(releaseEnv))

			pkgs, ecoOf, err := ws.Discover(cmd.Context())
			if err != nil {
				return fail(err)
			}
			released, err := releasedAtHead(cmd.Context(), ws, pkgs, ecoOf)
			if err != nil {
				return fail(err)
			}
			if len(released) == 0 {
				result.Message = "No package is tagged at HEAD; nothing to release."
				return finish(true)
			}
			results, ok, msg := forge.Make(forge.Request{
				Packages: pkgs, Released: released, EcoOf: ecoOf, Config: ws.Config,
				Selection: sel, RepoRoot: ws.Root, Run: run,
				Report: func(lines ...string) { printLines(progress, lines) },
				DryRun: dryRun, WantURLs: true,
				EnsureTag: combinedTagEnsurer(cmd.Context(), ws.Root, gitutil.DefaultRemote(cmd.Context(), ws.Root)),
			})
			result.Releases = append(result.Releases, results...)
			result.Message = msg
			if !jsonOut && ok {
				for _, r := range results {
					line := fmt.Sprintf("%s %s", r.Action, r.Tag)
					if r.URL != "" {
						line += "  " + commands.DimStyle.Render(r.URL)
					}
					fmt.Fprintln(out, line)
				}
			}
			return finish(ok)
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&dryRun, "dry-run", "n", false, "show the release(s) that would be created, and their notes, without creating anything")
	f.BoolVar(&jsonOut, "json", false, "print the releases made or skipped as JSON on stdout (progress goes to stderr)")
	f.StringVar(&forgeArg, "forge", "", "the forge: auto|github|gitlab|gitea|none (default: the release step's \"forge\", else auto)")
	return cmd
}

func printLines(w io.Writer, lines []string) {
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
}

// releasedAtHead returns the packages whose own tag points at HEAD: what the
// release commit released. Never nil, so forge.Request reads it as a subset.
// A git failure (an unborn HEAD) is an error, not "nothing released".
func releasedAtHead(ctx context.Context, ws *commands.Workspace, pkgs []plugin.Package, ecoOf map[string]string) ([]plugin.Package, error) {
	tags, err := gitutil.TagsAt(ctx, ws.Root, "HEAD")
	if err != nil {
		return nil, err
	}
	at := map[string]bool{}
	for _, t := range tags {
		at[t] = true
	}
	solo := singleApp(pkgs)
	released := []plugin.Package{}
	for _, p := range pkgs {
		if ws.Config.SkipsTag(p.Name) {
			continue
		}
		if at[gitutil.RenderTag(ws.Config.TagTemplate, ecoOf[p.Name], p.Dir, p.Name, p.Version, solo)] {
			released = append(released, p)
		}
	}
	return released, nil
}

// combinedTagEnsurer makes the combined release's tag the way the publish step
// makes the others: an annotated tag at HEAD, pushed to remote ("" keeps it
// local). It is the forge.Request.EnsureTag shape of ensureCombinedTag.
func combinedTagEnsurer(ctx context.Context, root, remote string) func(tag string, dryRun bool) (string, error) {
	return func(tag string, dryRun bool) (string, error) {
		_, line, err := ensureCombinedTag(ctx, root, remote, tag, dryRun)
		return line, err
	}
}

// ensureCombinedTag makes tag exist at HEAD: created when missing, and pushed
// to remote unless remote is "". A tag already there (on the remote, or with
// no remote, locally) is done, existed=true, but only when it points at HEAD:
// one on another commit belongs to an earlier release, and reusing it would
// hang this release's notes and assets on that one, so it is an error.
func ensureCombinedTag(ctx context.Context, root, remote, tag string, dryRun bool) (existed bool, line string, err error) {
	head, err := gitutil.CommitOf(ctx, root, "HEAD")
	if err != nil {
		return false, "", err
	}
	elsewhere := func(at string) error {
		return fmt.Errorf("the combined release's tag %s already exists at %s, not at the release commit %s; "+
			"each release needs its own tag (use ${version} in forgeRelease.tag)", tag, shortHash(at), shortHash(head))
	}
	local := gitutil.TagExists(ctx, root, tag)
	if local {
		at, err := gitutil.CommitOf(ctx, root, "refs/tags/"+tag)
		if err != nil {
			return false, "", err
		}
		if at != head {
			return false, "", elsewhere(at)
		}
	}
	if remote != "" {
		at, found, err := gitutil.RemoteTagCommit(ctx, root, remote, tag)
		if err != nil {
			return false, "", fmt.Errorf("looking up tag %s on %s: %w", tag, remote, err)
		}
		if found {
			if at != head {
				return false, "", elsewhere(at)
			}
			return true, "tag exists " + tag, nil
		}
	} else if local {
		return true, "tag exists " + tag, nil
	}
	if dryRun {
		if remote == "" {
			return false, "would tag " + tag, nil
		}
		if local {
			return false, "would push " + tag + " → " + remote, nil
		}
		return false, "would tag " + tag + " → push " + remote, nil
	}
	if !local {
		if err := gitutil.CreateTag(ctx, root, tag, tag); err != nil {
			return false, "", err
		}
	}
	if remote == "" {
		return false, "tagged " + tag, nil
	}
	if err := gitutil.PushTag(ctx, root, remote, tag); err != nil {
		// Another job may have pushed its own tag object for this release
		// between the lookup and the push; git refuses to overwrite it. The
		// same commit means the same release, so that is a success.
		if at, found, lerr := gitutil.RemoteTagCommit(ctx, root, remote, tag); lerr == nil && found && at == head {
			return true, "tag exists " + tag + " (pushed concurrently)", nil
		}
		return false, "", fmt.Errorf("pushing: %w", err)
	}
	return false, "tagged+pushed " + tag + " → " + remote, nil
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// combinedReleaseTag returns the combined release's tag for released, or ""
// when the mode is per-package or nothing is released. It fails as the forge
// step would: on templates using ${version} over packages on different ones.
func combinedReleaseTag(cfg *config.Config, released []plugin.Package) (string, error) {
	if cfg == nil {
		return "", nil
	}
	if err := cfg.ForgeRelease.Validate(); err != nil {
		return "", err
	}
	if !cfg.ForgeRelease.Combined() || len(released) == 0 {
		return "", nil
	}
	version, err := forge.SharedVersion(released, cfg.ForgeRelease)
	if err != nil {
		return "", err
	}
	tag, _ := cfg.ForgeRelease.Render(version)
	return tag, nil
}

// notPerPackage reports whether forgeRelease asks for anything but the default
// per-package releases: combined, or a mode combinedReleaseTag will reject.
// Per-package repos skip the combined-tag work, git lookups included.
func notPerPackage(cfg *config.Config) bool {
	m := cfg.ForgeRelease.Mode
	return m != "" && m != config.ForgeReleasePerPackage
}

// unionByName returns a followed by the packages of b not already in a.
func unionByName(a, b []plugin.Package) []plugin.Package {
	seen := make(map[string]bool, len(a))
	out := append([]plugin.Package(nil), a...)
	for _, p := range a {
		seen[p.Name] = true
	}
	for _, p := range b {
		if !seen[p.Name] {
			seen[p.Name] = true
			out = append(out, p)
		}
	}
	return out
}
