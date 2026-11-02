package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/ecosystem/dotnet"
	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/core/plugin"
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

		links, _, _, _ := stackRedirects(ctx, root, []string{"app", "core", "lib"}, nil)
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

		_, orphans, _, _ := stackRedirects(ctx, root, []string{"app", "terminal"}, nil)
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

		_, orphans, _, _ := stackRedirects(ctx, root, []string{"app", "lib"}, nil)
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

		_, orphans, _, _ := stackRedirects(ctx, root, []string{"app", "lib"}, nil)
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

// A fork republished under its own id is still the same code: a consumer
// referencing the republished id has to resolve from the member producing the
// original, keyed on the id the consumer wrote.
func TestStackRedirectsRepublishedIds(t *testing.T) {
	ctx := context.Background()

	t.Run("publishesAs redirects the republished id to the producer", func(t *testing.T) {
		root := t.TempDir()
		csproj(t, root, "foo", "Foo")
		csproj(t, root, "app", "Term.App", "Acme.Foo")
		pub := map[string]stackPublishing{"foo": {As: map[string]string{"Foo": "Acme.Foo"}}}
		links, orphans, notes, _ := stackRedirects(ctx, root, []string{"app", "foo"}, pub)
		if len(links["dotnet"]) != 1 {
			t.Fatalf("links = %+v", links)
		}
		l := links["dotnet"][0]
		if l.Package != "Acme.Foo" || l.Via != "Foo" || l.To != "foo" || !strings.HasSuffix(l.Path, "foo.csproj") {
			t.Fatalf("link = %+v", l)
		}
		if !strings.Contains(l.describe(), "Acme.Foo (Foo, republished)") {
			t.Errorf("describe = %q", l.describe())
		}
		for _, o := range orphans {
			if o.Member == "foo" {
				t.Error("foo reported as an orphan though the app consumes it under its republished id")
			}
		}
		if len(notes) != 0 {
			t.Errorf("notes = %v", notes)
		}
	})

	t.Run("publishPrefix covers every id the member produces", func(t *testing.T) {
		root := t.TempDir()
		csproj(t, root, "foo", "Foo")
		csproj(t, root, "foo/native", "Foo.Native")
		csproj(t, root, "app", "Term.App", "Acme.Foo", "Acme.Foo.Native")
		pub := map[string]stackPublishing{"foo": {Prefix: "Acme."}}
		links, _, _, _ := stackRedirects(ctx, root, []string{"app", "foo"}, pub)
		var got []string
		for _, l := range links["dotnet"] {
			got = append(got, l.Package+"<-"+l.Via)
		}
		if strings.Join(got, " ") != "Acme.Foo<-Foo Acme.Foo.Native<-Foo.Native" {
			t.Fatalf("links = %v", got)
		}
	})

	t.Run("a rule for a package the member does not produce is noted", func(t *testing.T) {
		root := t.TempDir()
		csproj(t, root, "foo", "Foo")
		csproj(t, root, "app", "Term.App", "Acme.Fooo")
		pub := map[string]stackPublishing{"foo": {As: map[string]string{"Fooo": "Acme.Fooo"}}}
		links, _, notes, _ := stackRedirects(ctx, root, []string{"app", "foo"}, pub)
		if len(links["dotnet"]) != 0 {
			t.Fatalf("a typo'd rule produced a link: %+v", links)
		}
		if len(notes) != 1 || !strings.Contains(notes[0], "produces no package called Fooo") {
			t.Fatalf("notes = %v", notes)
		}
	})

	t.Run("one republished id claimed by two packages redirects neither", func(t *testing.T) {
		root := t.TempDir()
		csproj(t, root, "foo", "Foo")
		csproj(t, root, "bar", "Bar")
		csproj(t, root, "app", "Term.App", "Acme.Lib")
		pub := map[string]stackPublishing{
			"foo": {As: map[string]string{"Foo": "Acme.Lib"}},
			"bar": {As: map[string]string{"Bar": "Acme.Lib"}},
		}
		links, _, notes, _ := stackRedirects(ctx, root, []string{"app", "bar", "foo"}, pub)
		if len(links["dotnet"]) != 0 {
			t.Fatalf("an ambiguous id was redirected: %+v", links)
		}
		if len(notes) == 0 || !strings.Contains(notes[0], "both") {
			t.Fatalf("clash not reported: %v", notes)
		}
	})

	t.Run("a republished id resolves within the member that declared it", func(t *testing.T) {
		// Two members build a Foo; only one republishes it. The republished
		// id has to reach that member's project, whichever Foo was seen last.
		root := t.TempDir()
		csproj(t, root, "bar", "Foo")
		csproj(t, root, "foo", "Foo")
		csproj(t, root, "app", "Term.App", "Acme.Foo")
		pub := map[string]stackPublishing{"foo": {As: map[string]string{"Foo": "Acme.Foo"}}}
		links, _, _, _ := stackRedirects(ctx, root, []string{"app", "bar", "foo"}, pub)
		if l := links["dotnet"]; len(l) != 1 || l[0].To != "foo" || !strings.HasSuffix(l[0].Path, "foo.csproj") {
			t.Fatalf("links = %+v, want Acme.Foo redirected into foo", l)
		}
	})

	t.Run("an id two members produce is redirected to neither", func(t *testing.T) {
		root := t.TempDir()
		csproj(t, root, "bar", "Foo")
		csproj(t, root, "foo", "Foo")
		csproj(t, root, "app", "Term.App", "Foo")
		links, _, notes, _ := stackRedirects(ctx, root, []string{"app", "bar", "foo"}, nil)
		if len(links["dotnet"]) != 0 {
			t.Fatalf("an ambiguous producer was redirected: %+v", links)
		}
		if len(notes) != 1 || !strings.Contains(notes[0], "more than one member") {
			t.Fatalf("notes = %v", notes)
		}
	})

	t.Run("a project outside every member does not contest an id", func(t *testing.T) {
		// The root's own build of Foo (a stale copy, a sample) is never a
		// redirect target, and must not make the member's Foo look ambiguous.
		root := t.TempDir()
		csproj(t, root, "foo", "Foo")
		csproj(t, root, "samples/Foo", "Foo")
		csproj(t, root, "app", "Term.App", "Foo")
		links, _, notes, _ := stackRedirects(ctx, root, []string{"app", "foo"}, nil)
		if l := links["dotnet"]; len(l) != 1 || l[0].To != "foo" {
			t.Fatalf("links = %+v", l)
		}
		if len(notes) != 0 {
			t.Fatalf("notes = %v", notes)
		}
	})

	t.Run("an id one member builds and another republishes under redirects neither", func(t *testing.T) {
		// bar really produces Acme.Foo; foo says its Foo is published as
		// Acme.Foo. The app's reference cannot be wired to one of them
		// without silently ignoring the other.
		root := t.TempDir()
		csproj(t, root, "foo", "Foo")
		csproj(t, root, "bar", "Acme.Foo")
		csproj(t, root, "app", "Term.App", "Acme.Foo")
		pub := map[string]stackPublishing{"foo": {As: map[string]string{"Foo": "Acme.Foo"}}}
		links, _, notes, _ := stackRedirects(ctx, root, []string{"app", "bar", "foo"}, pub)
		if len(links["dotnet"]) != 0 {
			t.Fatalf("a contested id was redirected: %+v", links)
		}
		if len(notes) != 1 || !strings.Contains(notes[0], "produced by bar") || !strings.Contains(notes[0], "republished id of Foo (foo)") {
			t.Fatalf("notes = %v", notes)
		}
	})

	t.Run("a declared id still wins over a republished one", func(t *testing.T) {
		// The app references Foo directly: nothing about republishing applies.
		root := t.TempDir()
		csproj(t, root, "foo", "Foo")
		csproj(t, root, "app", "Term.App", "Foo")
		pub := map[string]stackPublishing{"foo": {As: map[string]string{"Foo": "Acme.Foo"}}}
		links, _, _, _ := stackRedirects(ctx, root, []string{"app", "foo"}, pub)
		if l := links["dotnet"]; len(l) != 1 || l[0].Package != "Foo" || l[0].Via != "" {
			t.Fatalf("links = %+v", l)
		}
	})
}

// An overlay rig wrote is reported once nothing crosses any more — which is
// exactly when the ecosystem has no links and would otherwise not be asked.
func TestStackCheckOverlay_ReportsAnOverlayLeftOver(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	csproj(t, root, "app/src/App", "Acme.App", "Acme.Lib")
	csproj(t, root, "lib/src/Lib", "Acme.Lib")

	reports, _, _, failed := stackCheckOverlay(ctx, root, owned("app", "lib"))
	if len(failed) != 0 {
		t.Fatalf("scan failed: %v", failed)
	}
	// With a link, the .NET adapter reports the overlay as not yet written.
	rep := dotnetReport(reports)
	if rep == nil || len(rep.Links) != 1 || len(rep.Resp.Problems) != 1 || !strings.Contains(rep.Resp.Problems[0].Message, "not written") {
		t.Fatalf("with a link: %+v, want one link and a not-written report", reports)
	}

	// Write the overlay the link asks for, then take the link away.
	if _, err := dotnet.New().LocalOverlay(ctx, plugin.LocalOverlayRequest{
		Root: root, Redirects: redirectsOf(rep.Links), Write: true, Writable: []string{"app", "lib"},
	}); err != nil {
		t.Fatal(err)
	}
	// Doctor must report stale early facts independently of the targets file.
	propsPath := filepath.Join(root, "Directory.Build.props")
	data, err := os.ReadFile(propsPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(propsPath, []byte(strings.Replace(string(data), "RigStackAvailable_Acme_Lib", "RigStackAvailable_Old", -1)), 0644); err != nil {
		t.Fatal(err)
	}
	staleReports, _, _, _ := stackCheckOverlay(ctx, root, owned("app", "lib"))
	stale := dotnetReport(staleReports)
	if stale == nil || len(stale.Resp.Problems) != 0 || len(stale.Resp.Notes) != 1 || !strings.Contains(stale.Resp.Notes[0], "Directory.Build.props: out of date") {
		t.Fatalf("stale props: %+v", stale)
	}
	csproj(t, root, "app/src/App", "Acme.App")

	reports, _, _, failed = stackCheckOverlay(ctx, root, owned("app", "lib"))
	if len(failed) != 0 {
		t.Fatalf("scan failed: %v", failed)
	}
	rep = dotnetReport(reports)
	if rep == nil || len(rep.Links) != 0 {
		t.Fatalf("nothing crossing: %+v, want the .NET adapter still asked, with no links", reports)
	}
	if len(rep.Resp.Problems) != 1 || !strings.Contains(rep.Resp.Problems[0].Message, "left over") {
		t.Fatalf("nothing crossing: %+v, want the overlay reported as left over", rep.Resp.Problems)
	}
}

func dotnetReport(reports []stackOverlayReport) *stackOverlayReport {
	for i := range reports {
		if reports[i].Eco == "dotnet" {
			return &reports[i]
		}
	}
	return nil
}

// owned is a manifest whose members are all the user's own, so every one of
// their build files may be patched.
func owned(names ...string) *stackManifest {
	m := &stackManifest{Repos: map[string]*stackRepo{}}
	for _, n := range names {
		m.Repos[n] = &stackRepo{Owned: true}
	}
	return m
}

func TestStackSourceDoctorReport(t *testing.T) {
	root := t.TempDir()
	csproj(t, root, "app", "Acme.App", "Acme.Core")
	csproj(t, root, "lib", "Acme.Core")
	var out strings.Builder
	reports, _, _, failed := stackCheckOverlay(context.Background(), root, owned("app", "lib"))
	if len(failed) > 0 {
		t.Fatal(failed)
	}
	rep := dotnetReport(reports)
	if rep == nil || len(rep.Packages) != 2 {
		t.Fatalf("missing cached discovery: %+v", rep)
	}
	stackReportSourceSignals(&out, root, rep.Links, rep.Packages)
	for _, want := range []string{"app/app.csproj", "lib/lib.csproj", "RigStackspace=true", "RigStackspaceRoot=" + filepath.ToSlash(root), "RIG_STACK", "RigStackAvailable_Acme_Core=true (stack-wide availability during props evaluation", "RigStackSource_Acme_Core=true; STACK_ACME_CORE", "when PackageReference is active"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %s", want, out.String())
		}
	}
	if strings.Contains(out.String(), `\`) {
		t.Fatalf("doctor paths should use forward slashes consistently:\n%s", out.String())
	}
	if strings.Count(out.String(), "RigStackSource_Acme_Core=true") != 1 {
		t.Fatal(out.String())
	}
}

func TestStackWireLegacyImportIsAdvisory(t *testing.T) {
	root := t.TempDir()
	csproj(t, root, "app", "Acme.App", "Acme.Core")
	csproj(t, root, "lib", "Acme.Core")
	legacy := `<Project><!-- Added by rig. historical explanation -->
 <PropertyGroup><StackParentTargets>$([MSBuild]::GetPathOfFileAbove('Directory.Build.targets', '$(MSBuildThisFileDirectory)../'))</StackParentTargets></PropertyGroup>
 <Import Project="$(StackParentTargets)" Condition="'$(StackParentTargets)' != ''" />
 </Project>`
	if err := os.WriteFile(filepath.Join(root, "app/Directory.Build.targets"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	m := owned("app", "lib")
	m.Repos["app"].Owned = false
	var out strings.Builder
	// Strict mode must also accept the advisory; this member is not writable.
	if _, err := stackWire(context.Background(), &out, m, &gitrepo.Repo{Dir: root}, "", true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "legacy rig import still works") || strings.Contains(out.String(), "ends MSBuild's search") {
		t.Fatal(out.String())
	}
	reports, _, _, failed := stackCheckOverlay(context.Background(), root, m)
	if len(failed) > 0 {
		t.Fatal(failed)
	}
	rep := dotnetReport(reports)
	if rep == nil || len(rep.Resp.Problems) != 0 || len(rep.Resp.Notes) != 1 {
		t.Fatalf("doctor: %+v", rep)
	}
}

func TestStackOptionalPropsCompatibility(t *testing.T) {
	for _, rootProps := range []bool{false, true} {
		t.Run(map[bool]string{false: "member props", true: "root props"}[rootProps], func(t *testing.T) {
			root := t.TempDir()
			csproj(t, root, "app", "Acme.App", "Acme.Core")
			csproj(t, root, "lib", "Acme.Core")
			const props = `<Project><PropertyGroup><OwnedByYou>true</OwnedByYou></PropertyGroup></Project>`
			path := filepath.Join(root, "app/Directory.Build.props")
			if rootProps {
				path = filepath.Join(root, "Directory.Build.props")
			}
			if err := os.WriteFile(path, []byte(props), 0644); err != nil {
				t.Fatal(err)
			}
			m := owned("app", "lib")
			m.Repos["app"].Owned = false
			var out strings.Builder
			if _, err := stackWire(context.Background(), &out, m, &gitrepo.Repo{Dir: root}, "", true); err != nil {
				t.Fatalf("wire: %v", err)
			}
			data, _ := os.ReadFile(path)
			if string(data) != props {
				t.Fatal("hand owned props changed")
			}
			reports, _, _, failed := stackCheckOverlay(context.Background(), root, m)
			if len(failed) != 0 {
				t.Fatal(failed)
			}
			rep := dotnetReport(reports)
			if rep == nil || len(rep.Resp.Problems) != 0 || len(rep.Resp.Notes) == 0 {
				t.Fatalf("doctor: %+v", rep)
			}
			if err := stackPackRequireOverlay(context.Background(), root, m); err != nil {
				t.Fatalf("pack: %v", err)
			}
		})
	}
}

func TestStackMissingPropsDoesNotBlockPack(t *testing.T) {
	root := t.TempDir()
	csproj(t, root, "app", "Acme.App", "Acme.Core")
	csproj(t, root, "lib", "Acme.Core")
	m := owned("app", "lib")
	var out strings.Builder
	if _, err := stackWire(context.Background(), &out, m, &gitrepo.Repo{Dir: root}, "", true); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "Directory.Build.props")); err != nil {
		t.Fatal(err)
	}
	reports, _, _, _ := stackCheckOverlay(context.Background(), root, m)
	rep := dotnetReport(reports)
	if rep == nil || len(rep.Resp.Problems) != 0 || len(rep.Resp.Notes) != 1 || !strings.Contains(rep.Resp.Notes[0], "Directory.Build.props: not written") {
		t.Fatalf("missing props: %+v", rep)
	}
	if err := stackPackRequireOverlay(context.Background(), root, m); err != nil {
		t.Fatalf("pack: %v", err)
	}
}

func TestStackReachableSourceSignals(t *testing.T) {
	root := t.TempDir()
	csproj(t, root, "app", "Acme.App", "Acme.Control")
	csproj(t, root, "control", "Acme.Control", "Acme.Left", "Acme.Right")
	csproj(t, root, "left", "Acme.Left", "Acme.Core")
	csproj(t, root, "right", "Acme.Right", "Acme.Core")
	csproj(t, root, "core", "Acme.Core", "Acme.Control")
	csproj(t, root, "unused", "Acme.Unused")
	members := []string{"app", "control", "left", "right", "core", "unused"}
	links, _, _, failed := stackRedirects(context.Background(), root, members, nil)
	if len(failed) > 0 {
		t.Fatal(failed)
	}
	var control plugin.Redirect
	for _, l := range links["dotnet"] {
		if l.Package == "Acme.Control" {
			control = l.Redirect
		}
	}
	if strings.Join(control.Reachable, ",") != "Acme.Core,Acme.Left,Acme.Right" {
		t.Fatalf("closure: %+v", control)
	}
	req := localOverlayRequest(root, links["dotnet"], nil)
	if _, err := dotnet.New().LocalOverlay(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	reports, _, _, _ := stackCheckOverlay(context.Background(), root, owned(members...))
	rep := dotnetReport(reports)
	if rep == nil {
		t.Fatal("no report")
	}
	var out strings.Builder
	stackReportSourceSignals(&out, root, rep.Links, rep.Packages)
	app := strings.Split(strings.Split(out.String(), "app/app.csproj:")[1], "\n    control/")[0]
	if !strings.Contains(app, "STACK_ACME_CORE") || strings.Contains(app, "STACK_ACME_UNUSED") {
		t.Fatal(app)
	}
	// Changing only a deeper edge must invalidate the root overlay.
	csproj(t, root, "right", "Acme.Right", "Acme.Unused")
	reports, _, _, _ = stackCheckOverlay(context.Background(), root, owned(members...))
	rep = dotnetReport(reports)
	found := false
	for _, p := range rep.Resp.Problems {
		if strings.Contains(p.Message, "out of date") {
			found = true
		}
	}
	if !found {
		t.Fatalf("changed closure was not stale: %+v", rep.Resp)
	}
}

func TestStackDoctorProjectReferenceSignals(t *testing.T) {
	root := t.TempDir()
	csproj(t, root, "app", "Acme.App", "Acme.Control")
	csproj(t, root, "control", "Acme.Control", "Acme.Core")
	csproj(t, root, "core", "Acme.Core")
	csproj(t, root, "empty", "Acme.Empty")
	csproj(t, root, "tests", "Acme.Tests")
	// Backslashes occur in project files on every host; the path, rather than
	// the producer's package ID or filename alone, identifies the actual project.
	path := filepath.Join(root, "tests/tests.csproj")
	data, _ := os.ReadFile(path)
	data = []byte(strings.Replace(string(data), "</Project>", `<ItemGroup><ProjectReference Include="..\app\app.csproj"/></ItemGroup></Project>`, 1))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	reports, _, _, failed := stackCheckOverlay(context.Background(), root, owned("app", "control", "core", "empty", "tests"))
	if len(failed) > 0 {
		t.Fatal(failed)
	}
	rep := dotnetReport(reports)
	var out strings.Builder
	stackReportSourceSignals(&out, root, rep.Links, rep.Packages)
	tests := strings.Split(out.String(), "tests/tests.csproj:")[1]
	for _, id := range []string{"STACK_ACME_CONTROL", "STACK_ACME_CORE"} {
		if !strings.Contains(tests, id) {
			t.Fatal(tests)
		}
	}
	empty := strings.Split(strings.Split(out.String(), "empty/empty.csproj:")[1], "\n    tests/")[0]
	if strings.Contains(empty, "STACK_ACME_") {
		t.Fatal(empty)
	}
}
