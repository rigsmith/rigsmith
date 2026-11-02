package cli

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStackReleasedE2E(t *testing.T) {
	if os.Getenv("RIG_STACK_E2E") != "1" {
		t.Skip("set RIG_STACK_E2E=1 for the local NuGet build fixture")
	}
	for _, transitive := range []bool{false, true} {
		t.Run(fmt.Sprint(transitive), func(t *testing.T) {
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
			write("lib/Lib.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><PackageId>Acme.Core</PackageId><Version>2.0.0</Version></PropertyGroup></Project>`)
			write("lib/Api.cs", `public static class Api { public static int Released => 42; }`)
			reference := "Acme.Core"
			if transitive {
				reference = "Acme.Control"
			}
			write("app/App.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="`+reference+`" Version="1.0.0" /><StackOnlyWarning Include="ACME0001" /></ItemGroup></Project>`)
			write("app/Consumer.cs", "public static class Consumer { public static int Read() {\n#if STACK_ACME_CORE\nreturn Api.Released;\n#else\nreturn 0;\n#endif\n} }\n")
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
			pin, pinVersion := "Acme.Core", "2.0.0"
			if transitive {
				write("control/Control.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><PackageId>Acme.Control</PackageId><Version>2.0.0</Version></PropertyGroup><ItemGroup><PackageReference Include="Acme.Core" Version="2.0.0" /></ItemGroup></Project>`)
				write("control/Control.cs", `public static class Control { public static int Read() => Api.Released; }`)
				run("pack", "control/Control.csproj", "-o", filepath.Join(root, "feed"), "--nologo")
				z, err := zip.OpenReader(filepath.Join(root, "feed", "Acme.Control.2.0.0.nupkg"))
				if err != nil {
					t.Fatal(err)
				}
				defer z.Close()
				var nuspec []byte
				for _, f := range z.File {
					if strings.HasSuffix(f.Name, ".nuspec") {
						r, err := f.Open()
						if err != nil {
							t.Fatal(err)
						}
						nuspec, err = io.ReadAll(r)
						r.Close()
						if err != nil {
							t.Fatal(err)
						}
					}
				}
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(nuspec) }))
				defer srv.Close()
				pin, pinVersion, _, err = stackReleaseDelivery(ctx, stackDepsAPI{client: srv.Client()}, stackReleaseChain(), "app", "core", "Acme.Core", "2.0.0", "Acme.Control", "2.0.0", srv.URL)
				if err != nil {
					t.Fatal(err)
				}
			}
			edits, _, err := stackReleaseEdits(filepath.Join(root, "app"), pin, pinVersion, "STACK_ACME_CORE", "ACME0001")
			if err != nil {
				t.Fatal(err)
			}
			if err = stackReleaseWrite(edits); err != nil {
				t.Fatal(err)
			}
			run("build", "app/App.csproj", "-p:UseStackSources=false", "--nologo")

		})
	}
}
