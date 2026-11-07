package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type stackEdgeCIReport struct {
	Schema                        int
	SeedBase, Member, Feed, Label string
	Number                        uint64
	Tip                           bool   `json:"tip"`
	Status                        string `json:"status"`
	Pack                          stackEdgePackResult
	SHA256                        map[string]string
}

type stackEdgeCIOptions struct {
	Seed, Member, Report, Publish, Out, Allowed, Build, Test string
	Number                                                   uint64
	Tip, AllowTip, DryRun                                    bool
	Client                                                   *http.Client
	PublicFeed                                               string
}

func newStackEdgeCICmd() *cobra.Command {
	o := stackEdgeCIOptions{}
	c := &cobra.Command{
		Use: "ci", Short: "Build an edge report or independently verify and publish it",
		Long: "Use --seed owner/repo@ref --member name with exactly one of --report or --publish.\n" +
			"Both jobs require --number from the trusted CI run; publish must match the report.\n" +
			"Report reconstructs trackBranch, wires, builds, tests and packs.\n" +
			"Use no credentials in the build job. Tip conflicts write an inconclusive report and exit 0.\n" +
			"Publish runs no project code: reloads the seed, proves ancestry, ids and version,\n" +
			"verifies downloaded packages and sha256s, checks feed monotonicity and pushes over HTTP.\n" +
			"Third-party dependency and assembly-name expectations are build-job claims.\n" +
			"The push key is read only from the environment variable named by edge.pushKeySecret.\n" +
			"Tip publishing requires --allow-tip-publish. -o selects packages (default dist/).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if o.Seed == "" || o.Member == "" || (o.Report == "") == (o.Publish == "") {
				return fmt.Errorf("--seed, --member and exactly one of --report or --publish are required")
			}
			if !cmd.Flags().Changed("number") {
				return fmt.Errorf("--report and --publish require an explicit --number")
			}
			if o.Publish != "" && (o.Tip || cmd.Flags().Changed("build") || cmd.Flags().Changed("test")) {
				return fmt.Errorf("--upstream-tip, --build and --test belong to the report job")
			}
			return stackEdgeCI(cmd.Context(), cmd.OutOrStdout(), o)
		},
	}
	f := c.Flags()
	f.StringVar(&o.Seed, "seed", "", "Trusted manifest home (owner/repo@ref, URL or local path)")
	f.StringVar(&o.Member, "member", "", "Edge member from the trusted seed")
	f.StringVar(&o.Report, "report", "", "Write a credential-free build report")
	f.StringVar(&o.Publish, "publish", "", "Verify and publish this downloaded report on a fresh runner")
	f.StringVarP(&o.Out, "out", "o", "dist", "Directory containing packages to upload or publish")
	f.StringVar(&o.Allowed, "allowed-hosts", "github.com", "Comma-separated allowed seed, fork and upstream hosts")
	f.StringVar(&o.Build, "build", "rig build", "Build command from reconstructed stack root (bash)")
	f.StringVar(&o.Test, "test", "rig test", "Test command from reconstructed stack root (bash)")
	f.Uint64Var(&o.Number, "number", 0, "Explicit build run number (0 is unpublishable)")
	f.BoolVar(&o.Tip, "upstream-tip", false, "Merge trackBranch onto upstream tip; conflicts are inconclusive")
	f.BoolVar(&o.AllowTip, "allow-tip-publish", false, "Allow publication of a tip-mode report (off by default)")
	f.BoolVarP(&o.DryRun, "dry-run", "n", false, "Preview report or verify publication without pushing")
	for _, name := range []string{"seed", "member", "allowed-hosts", "build", "test", "number"} {
		_ = c.RegisterFlagCompletionFunc(name, func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveNoFileComp
		})
	}
	return c
}

func stackEdgeCI(ctx context.Context, out io.Writer, o stackEdgeCIOptions) (err error) {
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 30 * time.Second}
	}
	if o.PublicFeed == "" {
		o.PublicFeed = stackNugetFeed
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "rig-edge-ci-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	home := filepath.Join(tmp, "seed")
	data, base, err := stackCISeed(ctx, cwd, home, o.Seed, o.Allowed)
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(home, "rig.stack.jsonc"), data, 0600); err != nil {
		return err
	}
	m, _, err := loadStackManifest(home)
	if err != nil {
		return err
	}
	if m == nil || m.Repos[o.Member] == nil || m.Edge[o.Member] == nil {
		return fmt.Errorf("--member must name an edge member in the trusted seed")
	}
	e := m.Edge[o.Member]
	key := os.Getenv(e.PushKeySecret)
	defer func() {
		if err != nil && key != "" {
			err = errors.New(strings.ReplaceAll(err.Error(), key, "[REDACTED]"))
		}
	}()
	if err = stackCIValidate(m, o.Allowed); err != nil {
		return err
	}
	if err = stackCheckHome(ctx, home, m); err != nil {
		return err
	}
	if o.Publish != "" {
		r, err := stackEdgeCIReadReport(o.Publish)
		if err != nil {
			return err
		}
		return stackEdgeCIPublish(ctx, out, cwd, tmp, m, base, r, key, o)
	}
	// A misconfigured job must fail before any source or build tool executes.
	for _, edge := range m.Edge {
		if edge != nil && edge.PushKeySecret != "" && os.Getenv(edge.PushKeySecret) != "" {
			return fmt.Errorf("report job must not receive an edge push credential")
		}
	}
	if o.DryRun {
		_, err = fmt.Fprintln(out, "Would reconstruct, wire, build, test and write an edge report")
		return err
	}
	r := stackEdgeCIReport{Schema: 1, SeedBase: base, Member: o.Member, Feed: e.Feed, Label: e.Label, Number: o.Number, Tip: o.Tip}
	if r.Label == "" {
		r.Label = "edge"
	}
	// Reuse the private seed clone: its HEAD is the independently loaded base,
	// so a moving remote ref cannot change inputs before reconstruction.
	rebuilt, cleanup, err := stackEdgeReconstruct(ctx, stackEdgeReconstructOptions{Checkout: cwd, Seed: home, Members: []string{o.Member}, AllowedHosts: o.Allowed, UpstreamTip: o.Tip})
	if err != nil {
		var conflict *stackEdgeInconclusiveError
		if !errors.As(err, &conflict) {
			return err
		}
		r.Status = "inconclusive"
		r.Pack = stackEdgePackResult{Status: r.Status, Member: o.Member, Commit: conflict.Provenance.TrackCommit, Remedy: conflict.Remedy(), Provenance: map[string]stackEdgeProvenance{o.Member: conflict.Provenance}}
		if err = stackEdgeClearOutput(ctx, cwd, o.Out); err != nil {
			return err
		}
		if err = stackEdgeWritePackReport(o.Out, r.Pack); err != nil {
			return err
		}
	} else {
		defer cleanup()
		exe, err := stackCIExecutable()
		if err != nil {
			return err
		}
		if err = stackCanaryRun(ctx, rebuilt.Root, exe, "stack", "wire"); err != nil {
			return fmt.Errorf("edge wire failed")
		}
		for i, command := range []string{o.Build, o.Test} {
			if err = stackCanaryRun(ctx, rebuilt.Root, "bash", "-eo", "pipefail", "-c", command); err != nil {
				return fmt.Errorf("edge %s failed", []string{"build", "test"}[i])
			}
		}
		builtManifest, _, err := loadStackManifest(rebuilt.Root)
		if err != nil {
			return err
		}
		r.Pack, err = stackEdgePackTree(ctx, io.Discard, builtManifest, rebuilt.Root, o.Member, stackEdgePackOptions{Checkout: cwd, OutputDir: o.Out, Number: o.Number, Commit: rebuilt.Provenance[o.Member].TrackCommit, Provenance: rebuilt.Provenance, Client: o.Client, PublicFeed: o.PublicFeed})
		if err != nil {
			return fmt.Errorf("edge pack failed")
		}
		r.Status = r.Pack.Status
		r.SHA256, err = stackEdgeCIHashes(o.Out, r.Pack.Artifacts)
		if err != nil {
			return err
		}
	}
	if err = stackEdgeCIWriteReport(o.Report, r); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Edge report: %s\n", r.Status)
	return err
}

func stackEdgeCIReadReport(path string) (stackEdgeCIReport, error) {
	var r stackEdgeCIReport
	f, err := os.Open(path)
	if err != nil {
		return r, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 8<<20))
	d.DisallowUnknownFields()
	if err = d.Decode(&r); err != nil {
		return r, fmt.Errorf("invalid edge report")
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		return r, fmt.Errorf("invalid trailing edge report data")
	}
	return r, nil
}

func stackEdgeCIWriteReport(path string, r stackEdgeCIReport) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".edge-report-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(append(b, '\n')); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func stackEdgeCIHashes(dir string, packages []stackEdgeVerifiedPackage) (map[string]string, error) {
	hashes := map[string]string{}
	for _, pkg := range packages {
		if !filepath.IsLocal(filepath.FromSlash(pkg.File)) {
			return nil, fmt.Errorf("invalid package path")
		}
		f, err := os.Open(filepath.Join(dir, filepath.FromSlash(pkg.File)))
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return nil, err
		}
		hashes[pkg.File] = hex.EncodeToString(h.Sum(nil))
	}
	return hashes, nil
}
