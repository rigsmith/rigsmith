package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStackEdgePackDotnetE2E(t *testing.T) {
	if os.Getenv("RIG_STACK_E2E") != "1" {
		t.Skip("set RIG_STACK_E2E=1 for real dotnet builds")
	}
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join("..", "..", "..", "core", "ecosystem", "dotnet", "testdata", "edge-rename")
	if err := os.CopyFS(filepath.Join(root, "lib"), os.DirFS(fixture)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Directory.Build.props"), []byte(`<Project><PropertyGroup><Version>1.2.3</Version><CustomAfterMicrosoftCommonTargets>$(MSBuildThisFileDirectory)original &amp; hook.targets</CustomAfterMicrosoftCommonTargets></PropertyGroup></Project>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "original & hook.targets"), []byte(`<Project><PropertyGroup><Description>chained $(MSBuildProjectName)</Description></PropertyGroup></Project>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "lib", "BuildOnly"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "lib", "BuildOnly", "BuildOnly.csproj"), []byte(`<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><IsPackable>false</IsPackable></PropertyGroup></Project>`), 0o644); err != nil {
		t.Fatal(err)
	}
	feed := edgeTestFeed(t)
	var out bytes.Buffer
	dir := filepath.Join(root, "packages")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// This package did not appear during the build and must never be verified
	// or copied into the report, regardless of the adapter's predicted path.
	if err := os.WriteFile(filepath.Join(dir, "stale.nupkg"), []byte("not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := stackEdgePackTree(ctx, &out, edgeTestManifest(), root, "lib", stackEdgePackOptions{
		OutputDir: dir, Number: 7, Commit: strings.Repeat("a", 40), Here: true, PublicFeed: feed.URL, Client: feed.Client(),
	})
	if err != nil {
		t.Fatalf("pack: %v\n%s", err, out.String())
	}
	if result.Publishable || !result.Here || result.Version != "1.2.4-edge.7" || len(result.Artifacts) != 2 {
		t.Fatalf("result=%+v", result)
	}
	for _, artifact := range result.Artifacts {
		path := filepath.Join(dir, artifact.File)
		spec, assemblies, err := stackEdgeReadNupkg(path)
		if err != nil {
			t.Fatal(err)
		}
		original := strings.TrimPrefix(spec.ID, "You.")
		if spec.Description != "chained "+original {
			t.Fatalf("original hook lost for %s: description %q", spec.ID, spec.Description)
		}
		if original != "Lib" && original != "Lib.Controls" || len(assemblies) != 1 || assemblies[0] != original+".dll" {
			t.Fatalf("spec=%+v, assemblies=%v", spec, assemblies)
		}
		if original == "Lib.Controls" {
			deps := spec.Dependencies.Groups[0].Dependencies
			if len(deps) != 1 || deps[0].ID != "You.Lib" || deps[0].Version != result.Version {
				t.Fatalf("dependencies=%+v", deps)
			}
		}
		archive, err := zip.OpenReader(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range archive.File {
			if !strings.HasSuffix(file.Name, ".nuspec") {
				continue
			}
			stream, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			var metadata struct {
				Metadata struct {
					Repository struct {
						URL    string `xml:"url,attr"`
						Commit string `xml:"commit,attr"`
					} `xml:"repository"`
				} `xml:"metadata"`
			}
			err = xml.NewDecoder(stream).Decode(&metadata)
			stream.Close()
			if err != nil {
				t.Fatal(err)
			}
			if metadata.Metadata.Repository.URL != "https://github.com/you/Lib" || metadata.Metadata.Repository.Commit != strings.Repeat("a", 40) {
				t.Fatalf("repository=%+v", metadata.Metadata.Repository)
			}
		}
		archive.Close()
	}
	data, err := os.ReadFile(filepath.Join(dir, "edge-pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved stackEdgePackResult
	if err := json.Unmarshal(data, &saved); err != nil || saved.Publishable || !saved.Here || len(saved.Artifacts) != 2 {
		t.Fatalf("report: %s, %v", data, err)
	}
	// The publish seam needs neither a checkout nor MSBuild.
	downloads := t.TempDir()
	for _, artifact := range saved.Artifacts {
		data, err := os.ReadFile(filepath.Join(dir, artifact.File))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(downloads, artifact.File), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := stackEdgeVerify(downloads, saved.Expected); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "bytes") {
		t.Fatalf("no size report: %s", out.String())
	}
	// A project deriving its assembly from the renamed PackageId must refuse.
	project := filepath.Join(root, "lib", "Lib.Controls", "Lib.Controls.csproj")
	data, err = os.ReadFile(project)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(data), `<Project Sdk="Microsoft.NET.Sdk">`, `<Project><Import Project="Sdk.props" Sdk="Microsoft.NET.Sdk"/>`, 1)
	text = strings.Replace(text, "</Project>", `<Import Project="Sdk.targets" Sdk="Microsoft.NET.Sdk"/><PropertyGroup><AssemblyName>$(PackageId)</AssemblyName></PropertyGroup></Project>`, 1)
	data = []byte(text)
	if err := os.WriteFile(project, data, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = stackEdgePackTree(ctx, io.Discard, edgeTestManifest(), root, "lib", stackEdgePackOptions{OutputDir: filepath.Join(root, "refused"), Number: 8, Commit: strings.Repeat("a", 40), Here: true, PublicFeed: feed.URL, Client: feed.Client()})
	if err == nil || !strings.Contains(err.Error(), "AssemblyName changed") {
		t.Fatalf("derived assembly: %v", err)
	}
}
