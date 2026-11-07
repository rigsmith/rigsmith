package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
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
	mustGitStack(t, root, "init", "-q")
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
	// Earlier package outputs must be cleared, without touching unrelated files
	// or recursing into directories, before the new packages are written.
	if err := os.WriteFile(filepath.Join(dir, "stale.nupkg"), []byte("not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "keep.nupkg"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"keep.txt", "keep.nupkg/nested.nupkg", "stale.snupkg", "edge-pack.json"} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(path)), []byte("existing output"), 0600); err != nil {
			t.Fatal(err)
		}
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
	for _, path := range []string{"keep.txt", "keep.nupkg/nested.nupkg"} {
		assertStackEdgeFile(t, dir, path, "existing output")
	}
	for _, name := range []string{"stale.nupkg", "stale.snupkg"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("success retained stale package %s: %v", name, err)
		}
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

func TestStackEdgePackReconstructedDotnetE2E(t *testing.T) {
	if os.Getenv("RIG_STACK_E2E") != "1" {
		t.Skip("set RIG_STACK_E2E=1 for real dotnet builds")
	}
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet unavailable")
	}
	requireStackEdgeEngine(t)
	t.Setenv("RIG_STACK_CACHE", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Second)
	defer cancel()
	server := newGitServer(t, t.TempDir())
	upstream, fork := server.bare(t, "acme/Lib"), server.bare(t, "you/Lib")
	work := t.TempDir()
	mustGitStack(t, work, "init", "-q", "-b", "main")
	mustGitStack(t, work, "config", "user.name", "test")
	mustGitStack(t, work, "config", "user.email", "t@t")
	fixture, err := filepath.Abs(filepath.Join("..", "..", "..", "core", "ecosystem", "dotnet", "testdata", "edge-rename"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(work, os.DirFS(fixture)); err != nil {
		t.Fatal(err)
	}
	props := filepath.Join(work, "Directory.Build.props")
	if err := os.WriteFile(props, []byte(`<Project><PropertyGroup><Version>1.2.3</Version></PropertyGroup></Project>`), 0600); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, work, "add", ".")
	mustGitStack(t, work, "commit", "-qm", "public cursor without fix")
	base := strings.TrimSpace(mustGitStack(t, work, "rev-parse", "HEAD"))
	mustGitStack(t, work, "push", "-q", upstream, "main")
	// Both the version and a shipped content file exist only on trackBranch.
	// A stale cursor or the local checkout cannot produce this package.
	if err := os.WriteFile(props, []byte(`<Project><PropertyGroup><Version>2.0.0</Version><Description>carried fix</Description></PropertyGroup><ItemGroup><None Include="$(MSBuildThisFileDirectory)fix.txt" Pack="true" PackagePath="content/" /></ItemGroup></Project>`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "fix.txt"), []byte("carried fix\n"), 0600); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, work, "add", ".")
	mustGitStack(t, work, "commit", "-qm", "carry fix")
	track := strings.TrimSpace(mustGitStack(t, work, "rev-parse", "HEAD"))
	mustGitStack(t, work, "push", "-q", fork, "HEAD:refs/heads/stack/integration")
	m := edgeTestManifest()
	m.Repos["lib"].Upstream, m.Repos["lib"].Fork = server.spec("acme/Lib"), server.spec("you/Lib")
	m.LastSync = map[string]string{"lib": base}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	checkout := inTempStackspace(t, string(data))
	mustGitStack(t, checkout, "add", ".")
	mustGitStack(t, checkout, "-c", "user.name=test", "-c", "user.email=t@t", "commit", "-qm", "committed manifest")
	seed := filepath.Join(t.TempDir(), "seed.git")
	mustGitStack(t, checkout, "clone", "--bare", "-q", checkout, seed)
	// Deliberately poison both the working manifest and the working member.
	if err := os.CopyFS(filepath.Join(checkout, "lib"), os.DirFS(fixture)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "lib", "Directory.Build.props"), []byte(`<Project><PropertyGroup><Version>99.0.0</Version></PropertyGroup></Project>`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "rig.stack.jsonc"), []byte("invalid dirty manifest"), 0600); err != nil {
		t.Fatal(err)
	}
	feed := edgeTestFeed(t)
	transport := http.DefaultTransport
	http.DefaultTransport = stackDepsRoundTrip(func(req *http.Request) (*http.Response, error) {
		if strings.HasPrefix(req.URL.String(), stackNugetFeed+"/") {
			copy := req.Clone(req.Context())
			u := *req.URL
			u.Scheme, u.Host = "http", strings.TrimPrefix(feed.URL, "http://")
			copy.URL = &u
			return feed.Client().Transport.RoundTrip(copy)
		}
		return transport.RoundTrip(req)
	})
	t.Cleanup(func() { http.DefaultTransport = transport })
	guarded := filepath.Join(checkout, "tracked-output")
	if err := os.Mkdir(guarded, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(guarded, "x.nupkg"), []byte("tracked package input\n"), 0600); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, checkout, "add", "tracked-output/x.nupkg")
	mustGitStack(t, checkout, "-c", "user.name=test", "-c", "user.email=t@t", "commit", "-qm", "tracked package input")
	blocked := newStackEdgePackCmd()
	blocked.SetContext(ctx)
	blocked.SetArgs([]string{"lib", "--number", "7", "--allowed-hosts", server.addr, "--out", guarded})
	var output bytes.Buffer
	blocked.SetOut(&output)
	blocked.SetErr(&output)
	if err := blocked.Execute(); err == nil || !strings.Contains(err.Error(), "tracked source") {
		t.Fatalf("reconstructed pack did not protect the real checkout: %v\n%s", err, output.String())
	}
	assertStackEdgeFile(t, guarded, "x.nupkg", "tracked package input\n")
	if entries, err := os.ReadDir(guarded); err != nil || len(entries) != 1 || entries[0].Name() != "x.nupkg" {
		t.Fatalf("refused output changed: %v, %v", entries, err)
	}
	output.Reset()
	cmd := newStackEdgePackCmd()
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{"lib", "--number", "7", "--allowed-hosts", server.addr})
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pack: %v\n%s", err, output.String())
	}
	dir := filepath.Join(checkout, stackPackDefaultOut)
	data, err = os.ReadFile(filepath.Join(dir, "edge-pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report stackEdgePackResult
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	want := stackEdgeProvenance{TrackCommit: track, TrackParent: base, UpstreamCommit: base}
	if report.Status != "packed" || report.Commit != track || report.Provenance["lib"] != want || report.Here || !report.Publishable || report.Version != "2.0.0-edge.7" || len(report.Artifacts) != 2 {
		t.Fatalf("wrong reconstruction report: %+v", report)
	}
	for _, artifact := range report.Artifacts {
		path := filepath.Join(dir, artifact.File)
		spec, assemblies, err := stackEdgeReadNupkg(path)
		if err != nil {
			t.Fatal(err)
		}
		original := strings.TrimPrefix(spec.ID, "You.")
		if (original != "Lib" && original != "Lib.Controls") || spec.Description != "carried fix" || len(assemblies) != 1 || assemblies[0] != original+".dll" {
			t.Fatalf("spec=%+v, assemblies=%v", spec, assemblies)
		}
		if original == "Lib.Controls" {
			deps := spec.Dependencies.Groups[0].Dependencies
			if len(deps) != 1 || deps[0].ID != "You.Lib" || deps[0].Version != report.Version {
				t.Fatalf("dependencies=%+v", deps)
			}
		}
		archive, err := zip.OpenReader(path)
		if err != nil {
			t.Fatal(err)
		}
		foundFix, foundRepository := false, false
		for _, file := range archive.File {
			if file.Name != "content/fix.txt" && !strings.HasSuffix(file.Name, ".nuspec") {
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
			if file.Name == "content/fix.txt" {
				foundFix = strings.ReplaceAll(string(data), "\r\n", "\n") == "carried fix\n"
			} else {
				var spec struct {
					Metadata struct {
						Repository struct {
							URL    string `xml:"url,attr"`
							Commit string `xml:"commit,attr"`
						} `xml:"repository"`
					} `xml:"metadata"`
				}
				if err := xml.Unmarshal(data, &spec); err != nil {
					t.Fatal(err)
				}
				foundRepository = spec.Metadata.Repository.Commit == track && spec.Metadata.Repository.URL == "https://"+server.spec("you/Lib")
			}
		}
		archive.Close()
		if !foundFix || !foundRepository {
			t.Fatalf("%s missing carried fix (%t) or track repository metadata (%t)", spec.ID, foundFix, foundRepository)
		}
	}
	assertStackEdgeFile(t, checkout, "rig.stack.jsonc", "invalid dirty manifest")
	// Exercise explicit sources through the same CLI without more builds.
	for i, source := range [][]string{{"--manifest", "rig.stack.jsonc"}, {"--seed", seed, "--upstream-tip"}} {
		cmd := newStackEdgePackCmd()
		cmd.SetContext(ctx)
		cmd.SetArgs(append([]string{"lib", "--dry-run", "--number", fmt.Sprint(i + 8), "--allowed-hosts", server.addr}, source...))
		output.Reset()
		cmd.SetOut(&output)
		cmd.SetErr(&output)
		if err := cmd.Execute(); err != nil || !strings.Contains(output.String(), fmt.Sprintf("2.0.0-edge.%d", i+8)) {
			t.Fatalf("source %v: %v\n%s", source, err, output.String())
		}
	}
}
