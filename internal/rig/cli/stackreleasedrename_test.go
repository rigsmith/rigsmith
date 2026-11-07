package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStackRepublishedID(t *testing.T) {
	for _, tc := range []struct {
		name           string
		pub            stackPublishing
		original, want string
		ok             bool
	}{
		{"none", stackPublishing{}, "Lib", "", false},
		{"prefix", stackPublishing{Prefix: "You."}, "Lib", "You.Lib", true},
		{"explicit", stackPublishing{As: map[string]string{"Lib": "Private.Core"}}, "Lib", "Private.Core", true},
		{"explicit-wins", stackPublishing{Prefix: "You.", As: map[string]string{"Lib": "Private.Core"}}, "Lib", "Private.Core", true},
		{"prefix-covers-rest", stackPublishing{Prefix: "You.", As: map[string]string{"Lib": "Private.Core"}}, "Lib.Controls", "You.Lib.Controls", true},
		{"unmatched", stackPublishing{As: map[string]string{"Lib": "Private.Core"}}, "Lib.Controls", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := stackRepublishedID(tc.pub, tc.original)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("%q %v", got, ok)
			}
		})
	}
}

func TestStackReleasedXMLRename(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"include", `<Project><!-- You.Lib 1.0.0 --><PackageReference Include="You.Lib" Version="1.0.0" /><PackageReference Include="Other" Version="1.0.0" /></Project>`, `<Project><!-- You.Lib 1.0.0 --><PackageReference Include="Lib" Version="2.0.0" /><PackageReference Include="Other" Version="1.0.0" /></Project>`},
		{"update", `<Project><PackageReference Update='You.Lib' Version = '1.0.0' /></Project>`, `<Project><PackageReference Update="Lib" Version = "2.0.0" /></Project>`},
		{"central", `<Project><PackageVersion Version="1.0.0" Include="You.Lib" /></Project>`, `<Project><PackageVersion Version="2.0.0" Include="Lib" /></Project>`},
		{"central-reference", `<Project><PackageReference Include="You.Lib" /></Project>`, `<Project><PackageReference Include="Lib" /></Project>`},
		{"quoted-label", `<Project><PackageReference Label='Include="You.Lib" Version="1.0.0"' Include="You.Lib" Version="1.0.0" /></Project>`, `<Project><PackageReference Label='Include="You.Lib" Version="1.0.0"' Include="Lib" Version="2.0.0" /></Project>`},
		{"comment-only", `<Project><!-- <PackageReference Include="You.Lib" Version="1.0.0" /> --></Project>`, `<Project><!-- <PackageReference Include="You.Lib" Version="1.0.0" /> --></Project>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, changed, notes := stackReleaseXMLRename([]byte(tc.in), "You.Lib", "Lib", "2.0.0")
			if string(got) != tc.want || changed != (tc.in != tc.want) || len(notes) != 0 {
				t.Fatalf("%s changed=%v notes=%v", got, changed, notes)
			}
		})
	}
}

func stackReleasedSwitchFixture(t *testing.T, extra string, central bool) (string, *stackManifest, stackDepsAPI, string) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"lib/Controls.csproj": `<Project><PropertyGroup><PackageId>Lib.Controls</PackageId><Version>2.0.0</Version></PropertyGroup></Project>`,
		"lib/Lib.csproj":      `<Project><PropertyGroup><PackageId>Lib</PackageId><Version>2.0.0</Version></PropertyGroup></Project>`,
		"app/App.csproj":      `<Project><ItemGroup><PackageReference Include="You.Lib" Version="2.0.0-edge.1" />` + extra + `</ItemGroup></Project>`,
		"app/Code.cs":         "#if STACK_YOU_LIB\nApi.New();\n#else\nApi.Old();\n#endif\n",
	}
	if central {
		files["app/App.csproj"] = `<Project><ItemGroup><PackageReference Include="You.Lib" />` + extra + `</ItemGroup></Project>`
		files["app/Directory.Packages.props"] = `<Project><PropertyGroup><ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally></PropertyGroup><ItemGroup><PackageVersion Include="You.Lib" Version="2.0.0-edge.1" /></ItemGroup></Project>`
	}
	for path, body := range files {
		p := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	sha := strings.Repeat("a", 40)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".nuspec"):
			if strings.Contains(r.URL.Path, "you.lib") {
				t.Error("release evidence requested private id")
			}
			fmt.Fprintf(w, `<package><metadata><repository url="https://github.com/acme/lib" commit="%s" /></metadata></package>`, strings.Repeat("b", 40))
		case strings.Contains(r.URL.Path, "/compare/"):
			fmt.Fprint(w, `{"status":"ahead"}`)
		case strings.HasSuffix(r.URL.Path, "/pulls/1"):
			fmt.Fprintf(w, `{"merged_at":"today","merge_commit_sha":%q}`, sha)
		case strings.HasSuffix(r.URL.Path, "/pulls"):
			fmt.Fprint(w, `[{"number":1,"head":{"ref":"stack/topic","repo":{"full_name":"you/lib"}}}]`)
		default:
			t.Errorf("unexpected request: %s", r.URL)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	m := &stackManifest{Repos: map[string]*stackRepo{"app": {Upstream: "github.com/acme/app"}, "lib": {Upstream: "github.com/acme/lib", Fork: "github.com/you/lib", PublishPrefix: "You."}}, LastSync: map[string]string{"lib": sha}, Proposals: map[string]map[string]stackProposal{"lib": {"topic": {Branch: "stack/topic"}}}}
	return root, m, stackDepsAPI{base: srv.URL, client: srv.Client()}, srv.URL
}

func TestStackReleasedRepublishedPlan(t *testing.T) {
	for _, central := range []bool{false, true} {
		t.Run(fmt.Sprint(central), func(t *testing.T) {
			root, m, api, feed := stackReleasedSwitchFixture(t, "", central)
			request := stackReleasedReport{Member: "app", Sibling: "lib", Options: stackReleasedOptions{Package: "Lib", Version: "2.0.0", Feed: feed}}
			report, err := stackReleasedCompute(context.Background(), api, root, m, request)
			if err != nil {
				t.Fatal(err)
			}
			if report.Status != "released" || len(report.Removed) != 1 || !strings.Contains(report.Removed[0], "STACK_YOU_LIB") {
				t.Fatalf("report %+v", report)
			}
			edited := map[string]string{}
			for _, e := range report.Edits {
				edited[e.Path] = e.Content
			}
			if edited["Code.cs"] != "Api.New();\n" || !strings.Contains(edited["App.csproj"], `Include="Lib"`) {
				t.Fatalf("edits %+v", edited)
			}
			pin := "App.csproj"
			if central {
				pin = "Directory.Packages.props"
			}
			if !strings.Contains(edited[pin], `Version="2.0.0"`) || strings.Contains(edited[pin], "You.Lib") {
				t.Fatalf("pin %s", edited[pin])
			}
			fresh, err := stackReleasedCompute(context.Background(), api, root, m, request)
			if err != nil || stackReleasedValidate(report, fresh) != nil {
				t.Fatalf("recompute: %v", err)
			}
			report.Edits[0].Content += "tampered"
			if stackReleasedValidate(report, fresh) == nil {
				t.Fatal("trusted altered rename")
			}
			data, err := os.ReadFile(filepath.Join(root, "app", "Code.cs"))
			if err != nil || !bytes.Contains(data, []byte("STACK_YOU_LIB")) {
				t.Fatal("read-only report edited tree")
			}
		})
	}
}

func TestStackReleasedRepublishedAllOrNothing(t *testing.T) {
	for _, tc := range []struct {
		name, extra string
		explicit    bool
	}{
		{"prefix", `<PackageReference Include="You.Lib.Controls" Version="2.0.0-edge.1" />`, false},
		{"explicit", `<PackageReference Include="Private.Controls" Version="2.0.0-edge.1" />`, true},
		{"unused-central", `<PackageVersion Include="You.Lib.Controls" Version="2.0.0-edge.1" />`, false},
		{"update", `<PackageReference Update="You.Lib.Controls" Version="2.0.0-edge.1" />`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, m, api, feed := stackReleasedSwitchFixture(t, tc.extra, false)
			left := "You.Lib.Controls"
			if tc.explicit {
				m.Repos["lib"].PublishesAs = map[string]string{"Lib.Controls": "Private.Controls"}
				left = "Private.Controls"
			}
			report, err := stackReleasedCompute(context.Background(), api, root, m, stackReleasedReport{Member: "app", Sibling: "lib", Options: stackReleasedOptions{Package: "Lib", Version: "2.0.0", Feed: feed}})
			if err == nil || !strings.Contains(err.Error(), "all-or-nothing") || !strings.Contains(err.Error(), left) || len(report.Edits) != 0 {
				t.Fatalf("partial migration accepted: %+v %v", report, err)
			}
		})
	}
}

func TestStackReleasedRepublishedSkippedReference(t *testing.T) {
	root, m, api, feed := stackReleasedSwitchFixture(t, "", false)
	path := filepath.Join(root, "app", "Generated.csproj")
	if err := os.WriteFile(path, []byte(`<Project><!-- <auto-generated --> <PackageReference Include="You.Lib" Version="2.0.0-edge.1" /></Project>`), 0644); err != nil {
		t.Fatal(err)
	}
	report, err := stackReleasedCompute(context.Background(), api, root, m, stackReleasedReport{Member: "app", Sibling: "lib", Options: stackReleasedOptions{Package: "Lib", Version: "2.0.0", Feed: feed}})
	if err == nil || !strings.Contains(err.Error(), "You.Lib") || len(report.Edits) != 0 {
		t.Fatalf("surviving alias: %+v %v", report, err)
	}
}

func TestStackReleasedRepublishedPinProof(t *testing.T) {
	root, m, api, feed := stackReleasedSwitchFixture(t, "", false)
	path := filepath.Join(root, "app", "App.csproj")
	if err := os.WriteFile(path, []byte(`<Project><ItemGroup><PackageReference Include="You.Lib" VersionOverride="2.0.0-edge.1" /></ItemGroup></Project>`), 0644); err != nil {
		t.Fatal(err)
	}
	report, err := stackReleasedCompute(context.Background(), api, root, m, stackReleasedReport{Member: "app", Sibling: "lib", Options: stackReleasedOptions{Package: "Lib", Version: "2.0.0", Feed: feed}})
	if err == nil || !strings.Contains(err.Error(), "VersionOverride on You.Lib") || len(report.Edits) != 0 {
		t.Fatalf("wrong pin proved: %+v %v", report, err)
	}
}

func TestStackReleasedRepublishedPublish(t *testing.T) {
	f := newReleasedPublishFixture(t)
	root := filepath.Dir(f.app)
	home := filepath.Join(root, "home")
	manifestPath := filepath.Join(home, "rig.stack.jsonc")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest stackManifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Repos["lib"].PublishPrefix = "You."
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(manifestPath, data, 0644); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"App.csproj", "Code.cs", "Complex.cs"} {
		p := filepath.Join(f.app, file)
		content, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		content = bytes.ReplaceAll(content, []byte("Acme.Lib"), []byte("You.Acme.Lib"))
		content = bytes.ReplaceAll(content, []byte("STACK_ACME_LIB"), []byte("STACK_YOU_ACME_LIB"))
		if err = os.WriteFile(p, content, 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{home, f.app} {
		mustGitStack(t, dir, "add", ".")
		mustGitStack(t, dir, "commit", "-qm", "republished consumer")
		mustGitStack(t, dir, "push", filepath.Join(root, filepath.Base(dir)+".git"), "main")
	}
	f.build()
	if _, err = f.run(true); err != nil {
		t.Fatal(err)
	}
	if f.opens != 1 || !strings.Contains(f.tree["App.csproj"], `Include="Acme.Lib" Version="2.0.0"`) || strings.Contains(f.tree["App.csproj"], "You.Acme.Lib") || f.tree["Code.cs"] != "Api.New();\n" || !strings.Contains(f.body, "STACK_YOU_ACME_LIB") {
		t.Fatalf("republished PR tree=%+v body=%s", f.tree, f.body)
	}
	// A hand migration between jobs must take the existing no-write path too.
	for path, content := range f.tree {
		if err = os.WriteFile(filepath.Join(f.app, path), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	mustGitStack(t, f.app, "add", ".")
	mustGitStack(t, f.app, "commit", "-qm", "hand migration")
	mustGitStack(t, f.app, "push", f.appBare, "main")
	f.writes = 0
	out, err := f.run(true)
	if err != nil || f.writes != 0 || !strings.Contains(out, "already has the delivery version") {
		t.Fatalf("hand migration: %s %v writes=%d", out, err, f.writes)
	}
}

func TestStackReleasedRepublishedE2E(t *testing.T) {
	if os.Getenv("RIG_STACK_E2E") != "1" {
		t.Skip("set RIG_STACK_E2E=1 for the local NuGet build fixture")
	}
	root, m, api, feed := stackReleasedSwitchFixture(t, "", false)
	files := map[string]string{
		"NuGet.Config":   `<configuration><packageSources><clear /><add key="local" value="feed" /></packageSources></configuration>`,
		"lib/Lib.csproj": `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><PackageId>Lib</PackageId><Version>2.0.0</Version></PropertyGroup></Project>`,
		"lib/Api.cs":     `public static class Api { public static int Released => 42; }`,
		"app/App.csproj": `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="You.Lib" Version="2.0.0-edge.1" /></ItemGroup></Project>`,
		"app/Code.cs":    "public static class Consumer { public static int Read() {\n#if STACK_YOU_LIB\nreturn Api.Released;\n#else\nreturn 0;\n#endif\n} }\n",
	}
	for path, body := range files {
		if err := os.WriteFile(filepath.Join(root, path), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	run := func(args ...string) {
		t.Helper()
		c := exec.CommandContext(ctx, "dotnet", args...)
		c.Dir = root
		c.Env = append(os.Environ(), "DOTNET_NOLOGO=1", "NUGET_PACKAGES="+filepath.Join(root, "cache"))
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("dotnet %v: %v\n%s", args, err, out)
		}
	}
	run("pack", "lib/Lib.csproj", "-o", filepath.Join(root, "feed"), "--nologo")
	r, err := stackReleasedCompute(ctx, api, root, m, stackReleasedReport{Member: "app", Sibling: "lib", Options: stackReleasedOptions{Package: "Lib", Version: "2.0.0", Feed: feed}})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range r.Edits {
		if err = os.WriteFile(filepath.Join(root, "app", e.Path), []byte(e.Content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	run("build", "app/App.csproj", "-p:UseStackSources=false", "--nologo")
}

func TestStackReleasedRepublishedPrecedence(t *testing.T) {
	root, m, api, feed := stackReleasedSwitchFixture(t, `<PackageReference Include="You.External" Version="1.0.0" /><PackageReference Include="You.Lib.Controls" Version="1.0.0" />`, false)
	m.Repos["lib"].PublishesAs = map[string]string{"Lib": "Private.Lib", "Lib.Controls": "Private.Controls"}
	for _, path := range []string{"App.csproj", "Code.cs"} {
		p := filepath.Join(root, "app", path)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		data = bytes.ReplaceAll(data, []byte("You.Lib\""), []byte("Private.Lib\""))
		data = bytes.ReplaceAll(data, []byte("STACK_YOU_LIB"), []byte("STACK_PRIVATE_LIB"))
		if err = os.WriteFile(p, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	report, err := stackReleasedCompute(context.Background(), api, root, m, stackReleasedReport{Member: "app", Sibling: "lib", Options: stackReleasedOptions{Package: "Lib", Version: "2.0.0", Feed: feed}})
	if err != nil {
		t.Fatal(err)
	}
	var project, code string
	for _, edit := range report.Edits {
		if edit.Path == "App.csproj" {
			project = edit.Content
		}
		if edit.Path == "Code.cs" {
			code = edit.Content
		}
	}
	if !strings.Contains(project, `Include="Lib" Version="2.0.0"`) || !strings.Contains(project, `Include="You.External" Version="1.0.0"`) || !strings.Contains(project, `Include="You.Lib.Controls" Version="1.0.0"`) || code != "Api.New();\n" {
		t.Fatalf("edits %+v", report.Edits)
	}
}
