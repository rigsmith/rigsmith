package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestStackReleasedE2E(t *testing.T) {
	if os.Getenv("RIG_STACK_E2E") != "1" {
		t.Skip("set RIG_STACK_E2E=1 for the local NuGet build fixture")
	}
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("NuGet.Config", `<configuration><packageSources><clear /><add key="local" value="feed" /></packageSources></configuration>`)
	write("lib/Lib.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><PackageId>Acme.Lib</PackageId><Version>2.0.0</Version></PropertyGroup></Project>`)
	write("lib/Api.cs", `public static class Api { public static int Released => 42; }`)
	write("app/App.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="Acme.Lib" Version="1.0.0" /><StackOnlyWarning Include="ACME0001" /></ItemGroup></Project>`)
	write("app/Consumer.cs", "public static class Consumer { public static int Read() {\n#if STACK_ACME_LIB\nreturn Api.Released;\n#else\nreturn 0;\n#endif\n} }\n")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	run := func(args ...string) {
		t.Helper()
		c := exec.CommandContext(ctx, "dotnet", args...)
		c.Dir = root
		c.Env = append(os.Environ(), "DOTNET_NOLOGO=1", "NUGET_PACKAGES="+filepath.Join(root, "cache"))
		b, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("dotnet %v: %v\n%s", args, err, b)
		}
	}
	run("pack", "lib/Lib.csproj", "-o", filepath.Join(root, "feed"), "--nologo")
	edits, _, err := stackReleaseEdits(filepath.Join(root, "app"), "Acme.Lib", "2.0.0", "STACK_ACME_LIB", "ACME0001")
	if err != nil {
		t.Fatal(err)
	}
	if err = stackReleaseWrite(edits); err != nil {
		t.Fatal(err)
	}
	run("build", "app/App.csproj", "-p:UseStackSources=false", "--nologo")
}
