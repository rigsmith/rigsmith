package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// csproj writes a project producing pkg and referencing each of refs as a
// package — the shape that makes a link crossable in the first place.
func csproj(t *testing.T, root, dir, pkg string, refs ...string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("<Project Sdk=\"Microsoft.NET.Sdk\">\n  <PropertyGroup>\n")
	b.WriteString("    <Version>1.0.0</Version>\n    <PackageId>" + pkg + "</PackageId>\n")
	b.WriteString("  </PropertyGroup>\n  <ItemGroup>\n")
	for _, r := range refs {
		b.WriteString("    <PackageReference Include=\"" + r + "\" Version=\"1.0.0\" />\n")
	}
	b.WriteString("  </ItemGroup>\n</Project>\n")
	full := filepath.Join(root, dir, filepath.Base(dir)+".csproj")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStackRedirectsAndOrphans(t *testing.T) {
	ctx := context.Background()

	t.Run("a link between two libraries is found, not just app to library", func(t *testing.T) {
		// The pair people forget: the app is not involved at all.
		root := t.TempDir()
		csproj(t, root, "lib", "Acme.Lib", "Acme.Core")
		csproj(t, root, "core", "Acme.Core")
		csproj(t, root, "app", "Term.App", "Acme.Lib")

		links, _ := stackRedirects(ctx, root, []string{"app", "core", "lib"})
		var got []string
		for _, l := range links["dotnet"] {
			got = append(got, l.describe())
		}
		joined := strings.Join(got, " | ")
		if !strings.Contains(joined, "Acme.Core  lib → core") {
			t.Errorf("library-to-library link missing: %s", joined)
		}
		if !strings.Contains(joined, "Acme.Lib  app → lib") {
			t.Errorf("app-to-library link missing: %s", joined)
		}
	})

	t.Run("a member nothing consumes is reported", func(t *testing.T) {
		// The trap that bit twice: the right repo fused, but the consumer had
		// moved to a fork that renamed the package, so by identity there is no
		// link at all — and everything imports, wires and builds regardless.
		root := t.TempDir()
		csproj(t, root, "terminal", "Iciclecreek.Avalonia.Terminal")
		csproj(t, root, "app", "Term.App", "Avalloy.Terminal")

		_, orphans := stackRedirects(ctx, root, []string{"app", "terminal"})
		var found bool
		for _, o := range orphans {
			if o.Member == "terminal" {
				found = true
				if !strings.Contains(o.describe(), "Iciclecreek.Avalonia.Terminal") {
					t.Errorf("does not name what it produces: %s", o.describe())
				}
			}
		}
		if !found {
			t.Fatalf("orphan not reported: %+v", orphans)
		}
	})

	t.Run("a consumed member is not reported", func(t *testing.T) {
		root := t.TempDir()
		csproj(t, root, "lib", "Acme.Lib")
		csproj(t, root, "app", "Term.App", "Acme.Lib")

		_, orphans := stackRedirects(ctx, root, []string{"app", "lib"})
		for _, o := range orphans {
			if o.Member == "lib" {
				t.Fatalf("reported a member that is consumed: %+v", o)
			}
		}
	})

	t.Run("the app is an orphan by nature, and the caller filters it", func(t *testing.T) {
		// stackRedirects reports it; stackReportOrphans skips it because the
		// manifest says it is yours. Splitting it that way keeps the graph
		// honest and the output quiet.
		root := t.TempDir()
		csproj(t, root, "lib", "Acme.Lib")
		csproj(t, root, "app", "Term.App", "Acme.Lib")

		_, orphans := stackRedirects(ctx, root, []string{"app", "lib"})
		var sawApp bool
		for _, o := range orphans {
			if o.Member == "app" {
				sawApp = true
			}
		}
		if !sawApp {
			t.Fatal("app should be in the raw orphan list")
		}
		m := &stackManifest{Repos: map[string]*stackRepo{
			"app": {Upstream: "h/you/app", Fork: "h/you/app", Owned: true},
			"lib": {Upstream: "h/acme/lib", Fork: "h/you/lib"},
		}}
		var out strings.Builder
		stackReportOrphans(&out, m, orphans)
		if strings.Contains(out.String(), "app produces") {
			t.Fatalf("reported the owned app:\n%s", out.String())
		}
	})
}
