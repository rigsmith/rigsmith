package dotnet

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/core/plugin"
)

// TestArtifactsPropertiesRenameE2E settles STACK-EDGE-DESIGN's hook-order spike
// on the installed SDK, through the adapter rather than a hand-built command.
func TestArtifactsPropertiesRenameE2E(t *testing.T) {
	if os.Getenv("RIG_STACK_E2E") != "1" {
		t.Skip("set RIG_STACK_E2E=1 for real dotnet builds")
	}
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	sdk, _, err := runCmd(ctx, "", "dotnet", "--version")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("SDK %s", strings.TrimSpace(sdk))
	// NuGet's solution restore can treat /var and /private/var on macOS as
	// separate projects while writing the same assets file through the symlink.
	temp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	version := "1.2.3-edge.7"
	description := `literal ; , "quotes" %3B $(Unexpected) @(Items) 'end' &`
	for _, mode := range []string{"double", "package-id", "assembly-fallback"} {
		t.Run(mode, func(t *testing.T) {
			root := filepath.Join(temp, mode, "edge build & packages")
			if err := os.CopyFS(root, os.DirFS("testdata/edge-rename")); err != nil {
				t.Fatal(err)
			}
			var hook strings.Builder
			hook.WriteString(`<Project><PropertyGroup><_RigEdgePackageIdBefore>$(PackageId)</_RigEdgePackageIdBefore></PropertyGroup>`)
			for _, id := range []string{"Lib", "Lib.Controls"} {
				condition := fmt.Sprintf("'$(PackageId)' == '%s' or ('$(PackageId)' == '' and '$(AssemblyName)' == '%s')", id, id)
				if mode == "package-id" {
					condition = fmt.Sprintf("'$(PackageId)' == '%s'", id)
				}
				if mode == "assembly-fallback" {
					condition = fmt.Sprintf("'$(PackageId)' == '' and '$(AssemblyName)' == '%s'", id)
				}
				fmt.Fprintf(&hook, `<PropertyGroup Condition="%s"><PackageId>You.%s</PackageId></PropertyGroup>`, condition, id)
			}
			// Capture what each project saw before the rename, at the hook's import.
			hook.WriteString(`<Target Name="RecordEdgeEvaluation" BeforeTargets="GenerateNuspec"><WriteLinesToFile File="$(MSBuildProjectDirectory)/edge-before.txt" Lines="$(_RigEdgePackageIdBefore)" Overwrite="true" /></Target></Project>`)
			hookPath := filepath.Join(root, "rename hook.targets")
			writeFile(t, hookPath, hook.String())
			out := filepath.Join(root, "out", mode)
			resp, err := New().Artifacts(ctx, plugin.ArtifactsRequest{
				RepoRoot: root, OutputDir: out,
				Package:    plugin.Package{Name: "Lib.Controls", ManifestPath: "Edge.sln", Version: version},
				Properties: map[string]string{"CustomAfterMicrosoftCommonTargets": hookPath, "Description": description},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !resp.Built {
				t.Fatalf("no build: %+v", resp)
			}
			files, err := filepath.Glob(filepath.Join(out, "*.nupkg"))
			if err != nil || len(files) != 2 {
				t.Fatalf("packages = %v, %v", files, err)
			}
			found := map[string]bool{}
			for _, file := range files {
				spec, assemblies := readEdgePackage(t, file)
				original := strings.TrimPrefix(spec.ID, "You.")
				if original != "Lib" && original != "Lib.Controls" {
					t.Fatalf("unexpected package %q", spec.ID)
				}
				before, err := os.ReadFile(filepath.Join(root, original, "edge-before.txt"))
				if err != nil {
					t.Fatal(err)
				}
				priorID := strings.TrimSpace(string(before))
				renamed := mode == "double" || mode == "package-id" && priorID == original || mode == "assembly-fallback" && priorID == ""
				wantID := original
				if renamed {
					wantID = "You." + original
				}
				t.Logf("SDK %s, %s: PackageId at hook=%q, nuspec id=%s", strings.TrimSpace(sdk), original, priorID, spec.ID)
				if spec.ID != wantID || spec.Version != version {
					t.Fatalf("nuspec = %+v, want %s@%s", spec, wantID, version)
				}
				if spec.Description != description {
					t.Fatalf("literal property changed: got %q, want %q", spec.Description, description)
				}
				found[original] = true
				if len(assemblies) != 1 || assemblies[0] != original+".dll" {
					t.Fatalf("%s assemblies = %v", spec.ID, assemblies)
				}
				if original == "Lib.Controls" {
					depID := "You.Lib"
					// At this SDK import point Lib either has its default already or is empty.
					libBefore, err := os.ReadFile(filepath.Join(root, "Lib", "edge-before.txt"))
					if err != nil {
						t.Fatal(err)
					}
					if mode == "package-id" && strings.TrimSpace(string(libBefore)) == "" || mode == "assembly-fallback" && strings.TrimSpace(string(libBefore)) != "" {
						depID = "Lib"
					}
					if len(spec.Dependencies) != 1 || spec.Dependencies[0].ID != depID || spec.Dependencies[0].Version != version {
						t.Fatalf("%s dependencies = %+v, want %s@%s", spec.ID, spec.Dependencies, depID, version)
					}
				} else if len(spec.Dependencies) != 0 {
					t.Fatalf("unexpected Lib dependencies: %+v", spec.Dependencies)
				}
			}
			if !found["Lib"] || !found["Lib.Controls"] {
				t.Fatalf("missing packages: %v", found)
			}
		})
	}
}

type edgeNuspec struct {
	ID           string `xml:"id"`
	Version      string `xml:"version"`
	Description  string `xml:"description"`
	Dependencies []struct {
		ID      string `xml:"id,attr"`
		Version string `xml:"version,attr"`
	} `xml:"dependencies>group>dependency"`
}

func readEdgePackage(t *testing.T, path string) (edgeNuspec, []string) {
	t.Helper()
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	var spec edgeNuspec
	var assemblies []string
	for _, file := range archive.File {
		if strings.HasSuffix(file.Name, ".dll") {
			assemblies = append(assemblies, filepath.Base(file.Name))
		}
		if !strings.HasSuffix(file.Name, ".nuspec") {
			continue
		}
		stream, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(stream)
		stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			Metadata edgeNuspec `xml:"metadata"`
		}
		if err := xml.Unmarshal(data, &envelope); err != nil {
			t.Fatal(err)
		}
		spec = envelope.Metadata
	}
	if spec.ID == "" {
		t.Fatalf("no nuspec in %s", path)
	}
	return spec, assemblies
}
