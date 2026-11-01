package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/rigsmith/rigsmith/core/ecosystem/dotnet"
	"github.com/rigsmith/rigsmith/core/plugin"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStackCIGuardOutput(t *testing.T) {
	var meta stackGuardMetadata
	meta.Items.StackOnlyWarning = []stackGuardItem{{Identity: "ACME0001"}}
	project := filepath.Join(t.TempDir(), "acme", "App.csproj")
	output := "a.cs(1,1): warning ACME0001: source only [" + project + "]\na.cs(2,1): warning CS0168: unused [" + project + "]"
	held, visible := stackGuardOutput(output, project, meta, false)
	if len(held) != 1 || len(visible) != 1 || !strings.Contains(visible[0], "CS0168") {
		t.Fatalf("held=%v visible=%v", held, visible)
	}
	held, _ = stackGuardOutput(output, project, meta, true)
	if len(held) != 0 {
		t.Fatal("strict held back warnings")
	}
	held, _ = stackGuardOutput(output, filepath.Join(t.TempDir(), "other", "App.csproj"), meta, false)
	if len(held) != 0 {
		t.Fatal("other project warnings held back")
	}
	meta.Properties.RigStackspace = "true"
	held, _ = stackGuardOutput(output, project, meta, false)
	if len(held) != 0 {
		t.Fatal("stack held plain diagnostics")
	}
}

func TestStackCIGuardE2E(t *testing.T) {
	if os.Getenv("RIG_STACK_E2E") != "1" {
		t.Skip("set RIG_STACK_E2E=1 for local dotnet fixtures")
	}
	root := t.TempDir()
	summary := filepath.Join(root, "summary.md")
	t.Setenv("GITHUB_STEP_SUMMARY", summary)
	write := func(path, body string) {
		t.Helper()
		path = filepath.Join(root, path)
		if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(path, []byte(body), 0644); e != nil {
			t.Fatal(e)
		}
	}
	// Packages contain only a nuspec and the empty-library marker. No feed access.
	pack := func(id, version, deps string) {
		t.Helper()
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		w, _ := z.Create(id + ".nuspec")
		fmt.Fprintf(w, `<package><metadata><id>%s</id><version>%s</version><authors>acme</authors><description>fixture</description>%s</metadata></package>`, id, version, deps)
		w, _ = z.Create("lib/net10.0/_._")
		w.Write(nil)
		z.Close()
		write("feed/"+id+"."+version+".nupkg", b.String())
	}
	pack("Acme.Core", "1.0.0", "")
	pack("Acme.Core", "2.0.0", "")
	pack("Acme.Parent", "1.0.0", `<dependencies><dependency id="Acme.Core" version="[2.0.0]" /></dependencies>`)
	write("NuGet.Config", `<configuration><packageSources><clear/><add key="local" value="./feed" /></packageSources></configuration>`)
	write("lib/Lib.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><PackageId>Acme.Core</PackageId><Version>1.0.0</Version></PropertyGroup></Project>`)
	project := filepath.Join(root, "app/App.csproj")
	write("app/App.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><RestorePackagesPath>`+filepath.Join(root, "packages")+`</RestorePackagesPath></PropertyGroup><ItemGroup><StackOnlyWarning Include="CS1030" /></ItemGroup></Project>`)
	invoke := func(want bool) string {
		t.Helper()
		c := newStackCIGuardCmd()
		var out bytes.Buffer
		c.SetOut(&out)
		c.SetErr(&out)
		c.SetArgs([]string{project})
		err := c.Execute()
		t.Log(out.String())
		if (err == nil) != want {
			t.Fatalf("success=%v: %v\n%s", want, err, out.String())
		}
		return out.String()
	}
	write("app/App.cs", "#warning package build only\npublic class App {}")
	observedPlain := invoke(true)
	if strings.Contains(observedPlain, " Warning(s)") {
		t.Fatal("raw warning total printed as guard result")
	}
	if !strings.Contains(observedPlain, "1 stack-only warnings held back:") {
		t.Fatal("plain diagnostic was not counted")
	}
	summaryBody, summaryErr := os.ReadFile(summary)
	if summaryErr != nil || !strings.Contains(string(summaryBody), "CS1030") {
		t.Fatalf("observed summary missing: %v %s", summaryErr, summaryBody)
	}
	strictCmd := newStackCIGuardCmd()
	strictCmd.SetArgs([]string{project, "--strict"})
	strictCmd.SetOut(&bytes.Buffer{})
	strictCmd.SetErr(&bytes.Buffer{})
	if strictCmd.Execute() == nil {
		t.Fatal("strict accepted declared warning")
	}
	write("app/App.cs", "#warning package build only\npublic class App { void M() { int unused; } }")
	invoke(false)

	write("app/App.cs", "public class App {}")
	pack("Acme.Other", "1.0.0", "")
	pack("Acme.Other", "2.0.0", "")
	pack("Acme.OtherParent", "1.0.0", `<dependencies><dependency id="Acme.Other" version="2.0.0" /></dependencies>`)
	// Per-PackageReference NoWarn scopes to identity while project NoWarn does not.
	references := `<PackageReference Include="Acme.Core" Version="1.0.0" NoWarn="NU1605"/><PackageReference Include="Acme.Parent" Version="1.0.0"/><PackageReference Include="Acme.Other" Version="1.0.0"/><PackageReference Include="Acme.OtherParent" Version="1.0.0"/>`
	write("app/App.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><RestorePackagesPath>`+filepath.Join(root, "packages")+`</RestorePackagesPath></PropertyGroup><ItemGroup>`+references+`</ItemGroup></Project>`)
	probe := func(extra ...string) string {
		t.Helper()
		args := append([]string{"build", project, "-p:TreatWarningsAsErrors=true", "-p:RestoreForce=true", "-tl:off"}, extra...)
		cmd := exec.Command("dotnet", args...)
		out, err := cmd.CombinedOutput()
		t.Log(string(out))
		if err == nil {
			t.Fatal("non-swapped downgrade disappeared")
		}
		return string(out)
	}
	observed := probe()
	if strings.Contains(observed, "Detected package downgrade: Acme.Core") || !strings.Contains(observed, "Detected package downgrade: Acme.Other") {
		t.Fatalf("package metadata scoping failed:\n%s", observed)
	}
	// Exercise the actual wire output using the CLI, so the fixture is also a
	// contract between the guard and overlay rather than a hand-written NoWarn.
	write("app/App.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><RestorePackagesPath>`+filepath.Join(root, "packages")+`</RestorePackagesPath></PropertyGroup><ItemGroup><PackageReference Include="Acme.Core" Version="1.0.0"/><PackageReference Include="Acme.Parent" Version="1.0.0"/></ItemGroup></Project>`)
	invoke(false)
	_, err := dotnet.New().LocalOverlay(context.Background(), plugin.LocalOverlayRequest{Root: root, Write: true, Redirects: []plugin.Redirect{{Package: "Acme.Core", Path: "lib/Lib.csproj"}}})
	if err != nil {
		t.Fatal(err)
	}
	write("app/App.cs", "public class App {}")
	invoke(true)
	write("app/App.cs", "public class App { void M() { int unused; } }")
	invoke(false)
	// The same-code unrelated package must remain fatal after wire demotes
	// candidates; this also proves the restore target's timing with real NuGet.
	write("app/App.cs", "public class App {}")
	write("app/App.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><RestorePackagesPath>`+filepath.Join(root, "packages")+`</RestorePackagesPath></PropertyGroup><ItemGroup><PackageReference Include="Acme.Core" Version="1.0.0"/><PackageReference Include="Acme.Other" Version="1.0.0"/><PackageReference Include="Acme.OtherParent" Version="1.0.0"/></ItemGroup></Project>`)
	observed = invoke(false)
	if !strings.Contains(observed, ": warning NU1605:") || !strings.Contains(observed, "Acme.Other") {
		t.Fatalf("late warning demotion not observed:\n%s", observed)
	}
	for _, file := range []string{"Directory.Build.targets", "Directory.Build.props"} {
		if err := os.Remove(filepath.Join(root, file)); err != nil {
			t.Fatal(err)
		}
	}
	pack("Acme.Approx", "2.0.0", "")
	pack("Acme.ExactParent", "1.0.0", `<dependencies><dependency id="Acme.Core" version="[1.0.0]"/></dependencies>`)
	for _, tc := range []struct{ code, refs string }{
		{"NU1603", `<PackageReference Include="Acme.Approx" Version="1.0.0"/>`},
		{"NU1608", `<PackageReference Include="Acme.Core" Version="2.0.0"/><PackageReference Include="Acme.ExactParent" Version="1.0.0"/>`},
		{"NU1605", `<PackageReference Include="Acme.Core" Version="1.0.0"/><PackageReference Include="Acme.Parent" Version="1.0.0"/>`},
	} {
		write("app/App.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><RestorePackagesPath>`+filepath.Join(root, "packages")+`</RestorePackagesPath></PropertyGroup><ItemGroup><StackOnlyWarning Include="`+tc.code+`"/>`+tc.refs+`</ItemGroup></Project>`)
		observed = invoke(true)
		if !strings.Contains(observed, "1 stack-only warnings held back:") || !strings.Contains(observed, tc.code) {
			t.Fatalf("%s was not observed and held:\n%s", tc.code, observed)
		}
	}

}

func TestStackCIGuardNuGet(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "App.csproj")
	var meta stackGuardMetadata
	meta.Properties.RigStackspace = "true"
	meta.Properties.ProjectAssetsFile = filepath.Join(root, "project.assets.json")
	meta.Items.Present = []stackGuardItem{{Identity: "Acme.Core"}}
	message := "Detected package downgrade: Acme.Core from 2 to 1.\n App -> Acme.Parent -> Acme.Core"
	assets := `{"logs":[{"code":"NU1605","libraryId":"Acme.Core","message":"Detected package downgrade: Acme.Core from 2 to 1.\n App -> Acme.Parent -> Acme.Core"}]}`
	if e := os.WriteFile(meta.Properties.ProjectAssetsFile, []byte(assets), 0644); e != nil {
		t.Fatal(e)
	}
	output := project + " : warning NU1605: " + strings.ReplaceAll(message, "\n", "\n"+project+" : warning NU1605: ")
	held, visible := stackGuardOutput(output, project, meta, false)
	if len(held) != 1 || len(visible) != 0 {
		t.Fatalf("held=%v visible=%v", held, visible)
	}
	meta.Items.Present = []stackGuardItem{{Identity: "Acme.Other"}}
	held, visible = stackGuardOutput(output, project, meta, false)
	if len(held) != 0 || len(visible) == 0 {
		t.Fatalf("unrelated package vanished: %v %v", held, visible)
	}
	meta.Items.Present = []stackGuardItem{{Identity: "Acme.Core"}}
	if e := os.Remove(meta.Properties.ProjectAssetsFile); e != nil {
		t.Fatal(e)
	}
	held, _ = stackGuardOutput(output, project, meta, false)
	if len(held) != 0 {
		t.Fatal("missing evidence accepted")
	}
}

func TestStackCIGuardAssemblyE2E(t *testing.T) {
	if os.Getenv("RIG_STACK_E2E") != "1" {
		t.Skip("set RIG_STACK_E2E=1 for real assembly conflict")
	}
	root := t.TempDir()
	write := func(path, body string) {
		t.Helper()
		path = filepath.Join(root, path)
		if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(path, []byte(body), 0644); e != nil {
			t.Fatal(e)
		}
	}
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	blob := make([]byte, 160)
	binary.LittleEndian.PutUint32(blob[0:], 0x2400)
	binary.LittleEndian.PutUint32(blob[4:], 0x8004)
	binary.LittleEndian.PutUint32(blob[8:], 148)
	blob[12] = 6
	blob[13] = 2
	binary.LittleEndian.PutUint32(blob[16:], 0x2400)
	binary.LittleEndian.PutUint32(blob[20:], 0x31415352)
	binary.LittleEndian.PutUint32(blob[24:], 1024)
	binary.LittleEndian.PutUint32(blob[28:], uint32(key.E))
	modulus := key.N.Bytes()
	for i, v := range modulus {
		blob[32+len(modulus)-1-i] = v
	}
	write("public.snk", string(blob))
	write("NuGet.Config", `<configuration><packageSources><clear/></packageSources></configuration>`)
	build := func(path string) {
		t.Helper()
		cmd := exec.Command("dotnet", "build", filepath.Join(root, path), "-tl:off")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", path, err, out)
		}
	}
	for _, version := range []string{"1", "2"} {
		write("core"+version+"/Core.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><AssemblyName>Acme.Core</AssemblyName><SignAssembly>true</SignAssembly><PublicSign>true</PublicSign><AssemblyOriginatorKeyFile>`+filepath.Join(root, "public.snk")+`</AssemblyOriginatorKeyFile><PackageId>Acme.Core</PackageId><Version>`+version+`.0.0</Version></PropertyGroup></Project>`)
		write("core"+version+"/Core.cs", `public class Core { public static int Value => `+version+`; }`)
		build("core" + version + "/Core.csproj")
	}
	write("middle/Middle.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework></PropertyGroup><ItemGroup><Reference Include="Acme.Core"><HintPath>`+filepath.Join(root, "core2/bin/Debug/net10.0/Acme.Core.dll")+`</HintPath></Reference></ItemGroup></Project>`)
	write("middle/Middle.cs", `public class Middle { public static int Value => Core.Value; }`)
	build("middle/Middle.csproj")
	write("app/App.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><AutoUnifyAssemblyReferences>false</AutoUnifyAssemblyReferences><AutoGenerateBindingRedirects>false</AutoGenerateBindingRedirects></PropertyGroup><ItemGroup><PackageReference Include="Acme.Core" Version="1.0.0"/><Reference Include="Middle"><HintPath>`+filepath.Join(root, "middle/bin/Debug/net10.0/Middle.dll")+`</HintPath></Reference></ItemGroup></Project>`)
	write("app/App.cs", `public class App {}`)
	_, err = dotnet.New().LocalOverlay(context.Background(), plugin.LocalOverlayRequest{Root: root, Write: true, Redirects: []plugin.Redirect{{Package: "Acme.Core", Path: "core1/Core.csproj"}}})
	if err != nil {
		t.Fatal(err)
	}
	c := newStackCIGuardCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs([]string{filepath.Join(root, "app/App.csproj")})
	err = c.Execute()
	t.Log(out.String())
	if err == nil || !strings.Contains(out.String(), "MSB3277") || !strings.Contains(out.String(), "0 stack-only warnings held back") {
		t.Fatalf("assembly conflict was not rejected: %v\n%s", err, out.String())
	}
}

func TestStackCIGuardReviewE2E(t *testing.T) {
	if os.Getenv("RIG_STACK_E2E") != "1" {
		t.Skip("set RIG_STACK_E2E=1 for warning-policy regression builds")
	}
	for _, mode := range []string{"plain-tolerance", "sibling"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			write := func(path, body string) {
				t.Helper()
				path = filepath.Join(root, path)
				if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
					t.Fatal(e)
				}
				if e := os.WriteFile(path, []byte(body), 0644); e != nil {
					t.Fatal(e)
				}
			}
			write("NuGet.Config", `<configuration><packageSources><clear/></packageSources></configuration>`)
			project := filepath.Join(root, "app/App.csproj")
			invoke := func(want bool, strict bool) string {
				t.Helper()
				c := newStackCIGuardCmd()
				var out bytes.Buffer
				c.SetOut(&out)
				c.SetErr(&out)
				args := []string{project}
				if strict {
					args = append(args, "--strict")
				}
				c.SetArgs(args)
				err := c.Execute()
				if (err == nil) != want {
					t.Fatalf("success=%v: %v\n%s", want, err, out.String())
				}
				return out.String()
			}
			if mode == "plain-tolerance" {
				write("app/App.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><WarningsNotAsErrors>CS0168</WarningsNotAsErrors></PropertyGroup><ItemGroup><StackOnlyWarning Include="CS1030"/></ItemGroup></Project>`)
				write("app/App.cs", "#warning source only\npublic class App { void M() { int unused; } }")
				out := invoke(true, false)
				if !strings.Contains(out, "CS0168") {
					t.Fatal("tolerated warning hidden")
				}
				write("app/App.cs", "#warning source only\npublic class App { void M() { int unused; int assigned = 1; } }")
				invoke(false, false)
				return
			}
			write("lib/Lib.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><PackageId>Acme.Core</PackageId><TreatWarningsAsErrors>true</TreatWarningsAsErrors><WarningsAsErrors>CS0168</WarningsAsErrors></PropertyGroup></Project>`)
			write("lib/Core.cs", `public class Core { void M() { int unused; } }`)
			write("app/App.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><WarningsNotAsErrors>CS0219</WarningsNotAsErrors></PropertyGroup><ItemGroup><PackageReference Include="Acme.Core" Version="1.0.0"/><ProjectReference Include="support/Support.csproj"/><Compile Remove="support/**/*.cs"/></ItemGroup></Project>`)
			write("app/App.cs", `public class App { void M() { int assigned = 1; } }`)
			write("app/support/Support.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><WarningsNotAsErrors>CS0168</WarningsNotAsErrors></PropertyGroup></Project>`)
			write("app/support/Support.cs", `public class Support { void M() { int unused; } }`)
			write("rig.stack.jsonc", `{"repos":{"app":{"upstream":"github.com/acme/app"},"lib":{"upstream":"github.com/acme/lib"}}}`)
			_, err := dotnet.New().LocalOverlay(context.Background(), plugin.LocalOverlayRequest{Root: root, Write: true, Redirects: []plugin.Redirect{{Package: "Acme.Core", Path: "lib/Lib.csproj"}}})
			if err != nil {
				t.Fatal(err)
			}
			out := invoke(true, false)
			if !strings.Contains(out, "CS0168") || !strings.Contains(out, "1 sibling warnings observed") {
				t.Fatalf("sibling warning not visible and counted:\n%s", out)
			}
			invoke(false, true)
			write("app/App.cs", `public class App { void M() { int unused; } }`)
			invoke(false, false)
		})
	}
}

func TestStackCIGuardCRLFAndCodes(t *testing.T) {
	project := filepath.Join(t.TempDir(), "App.csproj")
	for _, code := range []string{"ACME0001", "xUnit1004", "Acme2Rule100"} {
		t.Run(code, func(t *testing.T) {
			if !stackGuardCode.MatchString(code) {
				t.Fatalf("valid diagnostic id rejected: %s", code)
			}
			var meta stackGuardMetadata
			meta.Items.StackOnlyWarning = []stackGuardItem{{Identity: code}}
			line := fmt.Sprintf("a.cs(1,1): warning %s: source only [%s]", code, project)
			output := line + "\r\n" + fmt.Sprintf("a.cs(2,1): warning CS0168: real warning [%s]\r\n", project)
			held, visible := stackGuardOutput(output, project, meta, false)
			if len(held) != 1 || held[0] != line || len(visible) != 1 || strings.Contains(visible[0], "\r") {
				t.Fatalf("held=%q visible=%q", held, visible)
			}
			meta.Properties.WarningsNotAsErrors = code
			_, tolerated, fatal := stackGuardVisiblePolicy([]string{line + "\r"}, project, meta, false)
			if fatal || len(tolerated) != 1 {
				t.Fatal("mixed-case or CRLF tolerance not attributed")
			}
		})
	}
	for _, code := range []string{"CS*", "CS1;CS2", "CS", "1CS", "xUnit-1004"} {
		if stackGuardCode.MatchString(code) {
			t.Fatalf("invalid diagnostic id accepted: %q", code)
		}
	}
}

func TestStackCIGuardPlainReferencesE2E(t *testing.T) {
	if os.Getenv("RIG_STACK_E2E") != "1" {
		t.Skip("set RIG_STACK_E2E=1 for plain reference policy builds")
	}
	root := t.TempDir()
	write := func(path, body string) {
		t.Helper()
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("NuGet.Config", `<configuration><packageSources><clear/></packageSources></configuration>`)
	write("app/App.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><CustomAfterMicrosoftCommonTargets>$(MSBuildThisFileDirectory)entry.targets</CustomAfterMicrosoftCommonTargets><WarningsAsErrors>CS1030</WarningsAsErrors><WarningsNotAsErrors>CS0219</WarningsNotAsErrors></PropertyGroup><ItemGroup><StackOnlyWarning Include="CS1030"/><ProjectReference Include="../lib/Lib.csproj"/></ItemGroup><Target Name="RequireEntryHook" BeforeTargets="CoreCompile"><Error Condition="'$(EntryHookPresent)' != 'true'" Text="entry hook lost"/></Target></Project>`)
	write("lib/Lib.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><WarningsNotAsErrors>CS0168</WarningsNotAsErrors><CustomAfterMicrosoftCommonTargets>$(MSBuildThisFileDirectory)reference.targets</CustomAfterMicrosoftCommonTargets></PropertyGroup><Target Name="RequireReferenceHook" BeforeTargets="CoreCompile"><Error Condition="'$(ReferenceHookPresent)' != 'true'" Text="reference hook lost"/></Target></Project>`)
	write("app/entry.targets", `<Project><PropertyGroup><EntryHookPresent>true</EntryHookPresent></PropertyGroup></Project>`)
	write("lib/reference.targets", `<Project><PropertyGroup><ReferenceHookPresent>true</ReferenceHookPresent></PropertyGroup></Project>`)
	write("app/App.cs", "#warning package build only\npublic class App { void M() { int assigned = 1; } }")
	write("lib/Lib.cs", `public class Lib { void M() { int unused; } }`)
	invoke := func(want bool) string {
		t.Helper()
		c := newStackCIGuardCmd()
		var output bytes.Buffer
		c.SetOut(&output)
		c.SetErr(&output)
		c.SetArgs([]string{filepath.Join(root, "app/App.csproj")})
		err := c.Execute()
		if (err == nil) != want {
			t.Fatalf("success=%v: %v\n%s", want, err, output.String())
		}
		return output.String()
	}
	out := invoke(true)
	if !strings.Contains(out, "CS0168") || !strings.Contains(out, "1 stack-only warnings held back") {
		t.Fatalf("reference policy or entry declaration lost:\n%s", out)
	}
	write("lib/Lib.cs", `public class Lib { void M() { int unused; int assigned = 1; } }`)
	out = invoke(false)
	if !strings.Contains(out, ": error CS0219:") {
		t.Fatalf("root tolerance reached reference:\n%s", out)
	}
	write("lib/Lib.cs", `#warning not declared for this project
public class Lib {}`)
	out = invoke(false)
	if !strings.Contains(out, ": error CS1030:") {
		t.Fatalf("root stack-only declaration reached reference:\n%s", out)
	}
	reference, err := os.ReadFile(filepath.Join(root, "lib/Lib.csproj"))
	if err != nil {
		t.Fatal(err)
	}
	write("lib/Lib.csproj", strings.Replace(string(reference), "<WarningsNotAsErrors>CS0168</WarningsNotAsErrors>", "<WarningsAsErrors>CS0168</WarningsAsErrors><WarningsNotAsErrors>CS0168</WarningsNotAsErrors>", 1))
	write("lib/Lib.cs", `public class Lib { void M() { int unused; } }`)
	out = invoke(false)
	if !strings.Contains(out, ": error CS0168:") {
		t.Fatalf("reference's explicit WarningsAsErrors was overwritten:\n%s", out)
	}

}

func TestStackCIGuardMetadataStreams(t *testing.T) {
	if mode := os.Getenv("RIG_STACK_GUARD_METADATA_HELPER"); mode != "" {
		if os.Getenv("DOTNET_NOLOGO") != "1" {
			os.Exit(8)
		}
		fmt.Fprintln(os.Stderr, "fixture evaluation diagnostic")
		fmt.Fprint(os.Stdout, `{"Properties":{"WarningsNotAsErrors":"xUnit1004"}}`)
		if mode == "error" {
			os.Exit(7)
		}
		os.Exit(0)
	}
	invoke := func(mode string) ([]byte, error) {
		command := stackGuardCommand(context.Background())
		command.Path = os.Args[0]
		command.Args = []string{os.Args[0], "-test.run=^TestStackCIGuardMetadataStreams$"}
		command.Env = append(command.Env, "RIG_STACK_GUARD_METADATA_HELPER="+mode)
		return stackGuardEvaluate(command)
	}
	output, err := invoke("success")
	if err != nil {
		t.Fatal(err)
	}
	var meta stackGuardMetadata
	if err := json.Unmarshal(output, &meta); err != nil || meta.Properties.WarningsNotAsErrors != "xUnit1004" {
		t.Fatalf("JSON polluted by stderr: %v %s", err, output)
	}
	output, err = invoke("error")
	if err == nil || !strings.Contains(err.Error(), "fixture evaluation diagnostic") || !json.Valid(output) {
		t.Fatalf("evaluation errors lost diagnostics: %v %s", err, output)
	}
}

func TestStackCIGuardSiblingAnalyzerCRLF(t *testing.T) {
	root, member, sibling := stackGuardPathFixture(t)
	var meta stackGuardMetadata
	meta.Properties.RigStackspaceRoot = root
	meta.MemberRoot = filepath.Dir(member)
	for _, code := range []string{"xUnit1004", "Acme2Rule100"} {
		output := fmt.Sprintf("a.cs(1,1): warning %s: analyzer [%s]\r\nBuild succeeded.\r\n", code, sibling)
		_, visible := stackGuardOutput(output, member, meta, false)
		siblings, _, fatal := stackGuardVisiblePolicy(visible, member, meta, false)
		if fatal || len(siblings) != 1 {
			t.Fatalf("analyzer not attributed: %s %v %v", code, siblings, fatal)
		}
		_, _, fatal = stackGuardVisiblePolicy(visible, member, meta, true)
		if !fatal {
			t.Fatal("strict analyzer warning was ignored")
		}
	}
	_, _, fatal := stackGuardVisiblePolicy([]string{fmt.Sprintf("a.cs: warning MSB3277: assembly conflict [%s]\r", sibling)}, member, meta, false)
	if !fatal {
		t.Fatal("unattributed assembly conflict exempted")
	}
}
