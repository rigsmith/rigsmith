package cli

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"

	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/internal/changerig/commands"
	"github.com/spf13/cobra"
)

// packDirPlan is the plan file pack writes, and publish --from-pack-dir reads,
// inside the pack directory.
const packDirPlan = "publish-plan.json"

func newPackCmd() *cobra.Command {
	var outDir, fromPlan string
	cmd := &cobra.Command{
		Use:   "pack",
		Short: "Build each package a publish would push into a directory, as @changesets' pack",
		Long: `Build the package file for every "publish" release in the plan (npm pack, dotnet
pack) into <out-dir>/packages/, and write <out-dir>/publish-plan.json recording
each file and its sha256 integrity. ` + "`publish --from-pack-dir`" + ` then pushes
exactly those files, so the job holding registry credentials never builds.

The plan is --from-publish-plan's file, or worked out as ` + "`publish-plan`" + ` does.
Cargo can't publish a prebuilt crate (` + "`cargo publish`" + ` builds from source),
so a cargo release in the plan is refused rather than packed for nothing.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if outDir == "" {
				return fmt.Errorf("--out-dir is required")
			}
			ws, err := commands.Open()
			if err != nil {
				return err
			}
			if _, err := applyReleaseEnv(ws.Root, noEnv); err != nil {
				return err
			}
			var plan [][]planRelease
			if fromPlan != "" {
				if plan, err = readPublishPlan(fromPlan); err != nil {
					return err
				}
			} else if plan, err = buildPublishPlan(cmd.Context(), ws, ""); err != nil {
				return err
			}
			packed, err := packPlan(cmd.Context(), ws, plan, outDir)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(packed) == 0 {
				fmt.Fprintln(out, commands.DimStyle.Render("No packages to pack."))
				return nil
			}
			fmt.Fprintln(out, "Packed packages:")
			for _, r := range packed {
				fmt.Fprintf(out, "- %s@%s\n", r.Name, r.Version)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&outDir, "out-dir", "", "directory to write the packages and publish-plan.json into")
	cmd.Flags().StringVar(&fromPlan, "from-publish-plan", "", "pack the releases in this publish-plan file rather than working the plan out")
	return cmd
}

// packPlan builds each "publish" release's package file into
// <outDir>/packages, records it on the release, and writes the plan to
// <outDir>/publish-plan.json. It returns the releases it packed.
func packPlan(ctx context.Context, ws *commands.Workspace, plan [][]planRelease, outDir string) ([]planRelease, error) {
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
	byName := map[string]plugin.Package{}
	for _, p := range append(pkgs, gen.Packages...) {
		byName[p.Name] = p
	}

	// Everything is checked before anything is built: a plan that can't be
	// packed fails in seconds, not after the first builds.
	for _, chunk := range plan {
		for _, r := range chunk {
			if r.Kind != "publish" {
				continue
			}
			p, ok := byName[r.Name]
			switch {
			case !ok:
				return nil, fmt.Errorf("the plan's %s isn't in this workspace", r.Name)
			case p.Version != r.Version:
				return nil, fmt.Errorf("the plan has %s@%s, but the workspace has %s: pack from the commit the plan was made on", r.Name, r.Version, p.Version)
			case ecoOf[r.Name] == "cargo":
				return nil, fmt.Errorf("%s: cargo can't publish a prebuilt crate (cargo publish builds from source), so it can't go through pack; publish it with `shiprig publish`", r.Name)
			}
		}
	}

	packagesDir := filepath.Join(outDir, "packages")
	if err := os.MkdirAll(packagesDir, 0o755); err != nil {
		return nil, err
	}
	var packed []planRelease
	for ci, chunk := range plan {
		for ri, r := range chunk {
			if r.Kind != "publish" {
				continue
			}
			file, err := packOne(ctx, ws, byName[r.Name], ecoOf[r.Name], outDir, packagesDir)
			if err != nil {
				return nil, fmt.Errorf("packing %s: %w", r.Name, err)
			}
			plan[ci][ri].Tarball = file
			packed = append(packed, plan[ci][ri])
		}
	}
	if err := writePublishPlan(filepath.Join(outDir, packDirPlan), plan); err != nil {
		return nil, err
	}
	return packed, nil
}

// packOne builds one package through its ecosystem's artifacts method and
// moves its single package file into packagesDir.
func packOne(ctx context.Context, ws *commands.Workspace, p plugin.Package, ecoID, outDir, packagesDir string) (*packedFile, error) {
	eco, ok := ws.EcosystemFor(ecoID)
	if !ok {
		return nil, fmt.Errorf("no %s adapter", ecoID)
	}
	tmp, err := os.MkdirTemp(outDir, ".pack-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	resp, err := eco.Artifacts(ctx, plugin.ArtifactsRequest{RepoRoot: ws.Root, Package: p, OutputDir: tmp})
	if err != nil {
		return nil, err
	}
	var files []string
	for _, a := range resp.Artifacts {
		if a.Kind == plugin.ArtifactPackage {
			files = append(files, a.Path)
		}
	}
	if len(files) != 1 {
		return nil, fmt.Errorf("expected one package file from the %s adapter, got %d%s", ecoID, len(files), messageSuffix(resp.Message))
	}
	name := filepath.Base(files[0])
	dest := filepath.Join(packagesDir, name)
	if err := os.Rename(files[0], dest); err != nil {
		return nil, err
	}
	integrity, err := sha256Integrity(dest)
	if err != nil {
		return nil, err
	}
	return &packedFile{Path: path.Join("packages", name), Integrity: integrity}, nil
}

func messageSuffix(msg string) string {
	if msg == "" {
		return ""
	}
	return " (" + msg + ")"
}

// sha256Integrity is canon's integrity string: "sha256-" and the file's
// sha256 digest in base64.
func sha256Integrity(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256-" + base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}
