package dotnet

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

func TestLocalOverlay(t *testing.T) {
	ctx := context.Background()
	a := New()

	newRoot := func(t *testing.T) string {
		t.Helper()
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "lib/src/Pty.Core/Pty.Core.csproj"), "<Project/>")
		writeFile(t, filepath.Join(root, "app/src/App/App.csproj"), "<Project/>")
		return root
	}
	redirects := []plugin.Redirect{{Package: "Pty.Core", Path: "lib/src/Pty.Core/Pty.Core.csproj"}}

	t.Run("nothing to redirect is skipped, not an empty file", func(t *testing.T) {
		got, err := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{Root: newRoot(t)})
		if err != nil {
			t.Fatal(err)
		}
		if !got.Skipped || len(got.Files) != 0 {
			t.Fatalf("%+v, want skipped with no files", got)
		}
	})

	t.Run("an overlay that cannot be read is not healthy", func(t *testing.T) {
		// A directory where the file should be: present, unreadable, and no
		// use to MSBuild either. Silence here would call it in effect.
		root := newRoot(t)
		writeFile(t, filepath.Join(root, "Directory.Build.props"), renderStackProps(redirects))
		if err := os.MkdirAll(filepath.Join(root, overlayFile), 0o755); err != nil {
			t.Fatal(err)
		}
		got, err := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{Root: root, Redirects: redirects})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Problems) != 1 || !strings.Contains(got.Problems[0].Message, "cannot be read") || got.Problems[0].Fixable {
			t.Fatalf("unreadable overlay: %+v, want one unfixable problem", got.Problems)
		}
	})

	t.Run("an overlay that cannot be read is not healthy even with nothing to redirect", func(t *testing.T) {
		root := newRoot(t)
		if err := os.MkdirAll(filepath.Join(root, overlayFile), 0o755); err != nil {
			t.Fatal(err)
		}
		got, err := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{Root: root})
		if err != nil {
			t.Fatal(err)
		}
		if got.Skipped || len(got.Problems) != 1 || !strings.Contains(got.Problems[0].Message, "cannot be read") || got.Problems[0].Fixable {
			t.Fatalf("unreadable overlay, no redirects: skipped=%v %+v, want one unfixable problem", got.Skipped, got.Problems)
		}
	})

	t.Run("describes without writing", func(t *testing.T) {
		root := newRoot(t)
		got, err := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{Root: root, Redirects: redirects})
		if err != nil {
			t.Fatal(err)
		}
		if body := got.Files[overlayFile]; !strings.Contains(body, `Include="Pty.Core"`) {
			t.Fatalf("overlay does not name the package:\n%s", body)
		}
		if _, err := os.Stat(filepath.Join(root, overlayFile)); !os.IsNotExist(err) {
			t.Fatal("wrote a file without being asked to")
		}
	})

	t.Run("declares each package once", func(t *testing.T) {
		// The point of the set-arithmetic form: no condition per redirect, so
		// nothing is written against %(Filename), which MSBuild splits at the
		// last dot and would match a differently-named sibling.
		got, _ := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{
			Root: newRoot(t),
			Redirects: []plugin.Redirect{
				{Package: "Pty.Core", Path: "lib/src/Pty.Core/Pty.Core.csproj"},
				{Package: "Pty.Core.Native", Path: "lib/src/Pty.Core/Pty.Core.csproj"},
			},
		})
		body := got.Files[overlayFile]
		if n := strings.Count(body, `StackSource Include="Pty.Core.Native"`); n != 1 {
			t.Errorf("Pty.Core.Native appears %d times, want once", n)
		}
		if strings.Contains(body, "Filename") {
			t.Error("overlay matches on Filename, which splits at the last dot")
		}
	})

	t.Run("a nearer build file is reported, because it silently wins", func(t *testing.T) {
		root := newRoot(t)
		writeFile(t, filepath.Join(root, "lib", overlayFile), "<Project>\n</Project>\n")
		got, err := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{Root: root, Redirects: redirects})
		if err != nil {
			t.Fatal(err)
		}
		var found bool
		for _, p := range got.Problems {
			if strings.HasPrefix(p.Path, "lib") && strings.Contains(p.Message, "ends MSBuild's search") {
				found = true
			}
		}
		if !found {
			t.Fatalf("shadowing file not reported: %+v", got.Problems)
		}
	})

	t.Run("a nearer build file that continues the walk-up is fine", func(t *testing.T) {
		root := newRoot(t)
		writeFile(t, filepath.Join(root, "lib", overlayFile),
			"<Project>\n<Import Project=\"$([MSBuild]::GetPathOfFileAbove('Directory.Build.targets','$(MSBuildThisFileDirectory)../'))\" />\n</Project>\n")
		got, _ := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{Root: root, Redirects: redirects})
		for _, p := range got.Problems {
			if strings.Contains(p.Message, "ends MSBuild's search") {
				t.Fatalf("reported a file that imports the one above it: %+v", p)
			}
		}
	})

	t.Run("a redirect pointing nowhere is reported", func(t *testing.T) {
		got, _ := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{
			Root:      newRoot(t),
			Redirects: []plugin.Redirect{{Package: "Typo.Core", Path: "lib/src/Nope/Nope.csproj"}},
		})
		if len(got.Problems) == 0 || !strings.Contains(got.Problems[0].Message, "Typo.Core") {
			t.Fatalf("missing redirect not reported: %+v", got.Problems)
		}
	})

	t.Run("writing, then rewriting its own file", func(t *testing.T) {
		root := newRoot(t)
		if _, err := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{Root: root, Redirects: redirects, Write: true}); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(filepath.Join(root, overlayFile))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), overlayMarker) {
			t.Fatal("written file carries no marker, so a rewrite could not tell it apart from a hand-written one")
		}
		if _, err := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{Root: root, Redirects: redirects, Write: true}); err != nil {
			t.Fatalf("refused to rewrite its own file: %v", err)
		}
	})

	t.Run("refuses to clobber a file it did not write", func(t *testing.T) {
		root := newRoot(t)
		writeFile(t, filepath.Join(root, overlayFile), "<Project>\n  <!-- mine -->\n</Project>\n")
		_, err := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{Root: root, Redirects: redirects, Write: true})
		if err == nil || !strings.Contains(err.Error(), "rig did not write") {
			t.Fatalf("got %v, want a refusal naming the existing file", err)
		}
		body, _ := os.ReadFile(filepath.Join(root, overlayFile))
		if !strings.Contains(string(body), "mine") {
			t.Fatal("clobbered it anyway")
		}
	})
}

// TestLocalOverlayShadowAboveConsumers is the case a redirect-path scan misses:
// the file that shadows the overlay sits above the *consumer*, and a redirect
// only says where a package is produced.
func TestLocalOverlayShadowAboveConsumers(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "lib/src/Pty.Core/Pty.Core.csproj"), "<Project/>")
	writeFile(t, filepath.Join(root, "app/src/App/App.csproj"), "<Project/>")
	// Nothing under lib/ is wrong; app/ is where the consumer lives.
	writeFile(t, filepath.Join(root, "app", overlayFile), "<Project>\n  <!-- unrelated build tweak -->\n</Project>\n")

	got, err := New().LocalOverlay(ctx, plugin.LocalOverlayRequest{
		Root:      root,
		Redirects: []plugin.Redirect{{Package: "Pty.Core", Path: "lib/src/Pty.Core/Pty.Core.csproj"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, p := range got.Problems {
		if strings.HasPrefix(p.Path, "app") {
			found = true
		}
	}
	if !found {
		t.Fatalf("shadowing file above the consumer not reported: %+v", got.Problems)
	}
}

func TestContinuesWalkUp(t *testing.T) {
	live := "<Project>\n<PropertyGroup><P>$([MSBuild]::GetPathOfFileAbove('Directory.Build.targets','x'))</P></PropertyGroup>\n<Import Project=\"$(P)\" />\n</Project>"
	if !continuesWalkUp(live) {
		t.Error("a real import was not recognised")
	}
	// The trap: the function name is resolved into a property on the line above,
	// so a commented-out import leaves the name in the file. Matching the name
	// alone reports this as fine while the build silently is not.
	commented := "<Project>\n<PropertyGroup><P>$([MSBuild]::GetPathOfFileAbove('Directory.Build.targets','x'))</P></PropertyGroup>\n<!-- <Import Project=\"$(P)\" /> -->\n</Project>"
	if continuesWalkUp(commented) {
		t.Error("a commented-out import was treated as live")
	}
	if continuesWalkUp("<Project>\n</Project>") {
		t.Error("a file with no import at all was treated as continuing the walk-up")
	}
}

func TestLocalOverlayPatchesShadowing(t *testing.T) {
	ctx := context.Background()
	a := New()
	redirects := []plugin.Redirect{{Package: "Pty.Core", Path: "lib/src/Pty.Core/Pty.Core.csproj"}}

	newRoot := func(t *testing.T) string {
		t.Helper()
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "lib/src/Pty.Core/Pty.Core.csproj"), "<Project/>")
		writeFile(t, filepath.Join(root, "app/src/App/App.csproj"), "<Project/>")
		writeFile(t, filepath.Join(root, "app", overlayFile), "<Project>\n  <!-- mine -->\n</Project>\n")
		return root
	}

	t.Run("patched where the caller allows it", func(t *testing.T) {
		root := newRoot(t)
		got, err := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{
			Root: root, Redirects: redirects, Write: true, Writable: []string{"app"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Fixed) != 1 || got.Fixed[0] != "app/"+overlayFile {
			t.Fatalf("Fixed = %v, want the shadowing file", got.Fixed)
		}
		for _, p := range got.Problems {
			if strings.HasPrefix(p.Path, "app") {
				t.Fatalf("still reported after patching: %+v", p)
			}
		}
		body, _ := os.ReadFile(filepath.Join(root, "app", overlayFile))
		if !continuesWalkUp(string(body)) {
			t.Fatalf("patch did not make it continue the walk-up:\n%s", body)
		}
		// What was already in the file is not the caller's to lose.
		if !strings.Contains(string(body), "<!-- mine -->") {
			t.Fatal("patch discarded the file's own content")
		}
	})

	t.Run("reported, never edited, where it is not allowed", func(t *testing.T) {
		// A fork you contribute to: this line would ride back into somebody
		// else's pull request as rig-specific plumbing.
		root := newRoot(t)
		before, _ := os.ReadFile(filepath.Join(root, "app", overlayFile))
		got, err := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{
			Root: root, Redirects: redirects, Write: true, Writable: []string{"lib"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Fixed) != 0 {
			t.Fatalf("Fixed = %v, want nothing touched", got.Fixed)
		}
		var reported bool
		for _, p := range got.Problems {
			if strings.HasPrefix(p.Path, "app") {
				reported = true
				if p.Fixable {
					t.Error("marked fixable when the caller did not allow it")
				}
			}
		}
		if !reported {
			t.Fatalf("not reported either: %+v", got.Problems)
		}
		after, _ := os.ReadFile(filepath.Join(root, "app", overlayFile))
		if string(before) != string(after) {
			t.Fatal("edited a file the caller did not offer")
		}
	})

	t.Run("a check says it could be fixed without fixing it", func(t *testing.T) {
		root := newRoot(t)
		before, _ := os.ReadFile(filepath.Join(root, "app", overlayFile))
		got, _ := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{
			Root: root, Redirects: redirects, Writable: []string{"app"},
		})
		var fixable bool
		for _, p := range got.Problems {
			if strings.HasPrefix(p.Path, "app") && p.Fixable {
				fixable = true
			}
		}
		if !fixable {
			t.Fatalf("not offered as fixable: %+v", got.Problems)
		}
		after, _ := os.ReadFile(filepath.Join(root, "app", overlayFile))
		if string(before) != string(after) {
			t.Fatal("a check wrote to a file")
		}
	})

	t.Run("patching twice does not patch twice", func(t *testing.T) {
		root := newRoot(t)
		req := plugin.LocalOverlayRequest{Root: root, Redirects: redirects, Write: true, Writable: []string{"app"}}
		if _, err := a.LocalOverlay(ctx, req); err != nil {
			t.Fatal(err)
		}
		got, err := a.LocalOverlay(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Fixed) != 0 {
			t.Fatalf("patched an already-patched file: %v", got.Fixed)
		}
		body, _ := os.ReadFile(filepath.Join(root, "app", overlayFile))
		// Counting the element, not the identifier: one patch mentions
		// StackParentTargets four times over the property and the import.
		if n := strings.Count(string(body), "<Import Project=\"$(StackParentTargets)\""); n != 1 {
			t.Fatalf("the import appears %d times, want once", n)
		}
	})

	t.Run("a file with no Project element is left alone", func(t *testing.T) {
		root := newRoot(t)
		writeFile(t, filepath.Join(root, "app", overlayFile), "not xml at all\n")
		got, err := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{
			Root: root, Redirects: redirects, Write: true, Writable: []string{"app"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Fixed) != 0 {
			t.Fatalf("patched something it could not parse: %v", got.Fixed)
		}
		body, _ := os.ReadFile(filepath.Join(root, "app", overlayFile))
		if string(body) != "not xml at all\n" {
			t.Fatalf("changed it anyway:\n%s", body)
		}
	})
}

// After the last cross-member reference leaves, a Write with nothing to
// redirect removes rig's own overlay — and only rig's own.
func TestLocalOverlayRemovesStaleGeneratedOverlay(t *testing.T) {
	ctx := context.Background()
	a := New()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "app/src/App/App.csproj"), "<Project/>")

	t.Run("a generated overlay is removed", func(t *testing.T) {
		writeFile(t, filepath.Join(root, overlayFile), overlayMarker+"\n<Project/>")
		got, err := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{Root: root, Write: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Removed) != 1 || got.Removed[0] != overlayFile {
			t.Fatalf("Removed = %v", got.Removed)
		}
		if _, err := os.Stat(filepath.Join(root, overlayFile)); !os.IsNotExist(err) {
			t.Error("stale overlay still present")
		}
	})
	t.Run("a hand-written one is not", func(t *testing.T) {
		writeFile(t, filepath.Join(root, overlayFile), "<Project><!-- mine --></Project>")
		got, err := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{Root: root, Write: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Removed) != 0 {
			t.Fatalf("Removed = %v", got.Removed)
		}
		if _, err := os.Stat(filepath.Join(root, overlayFile)); err != nil {
			t.Error("hand-written overlay deleted")
		}
	})
	t.Run("without Write nothing is touched", func(t *testing.T) {
		writeFile(t, filepath.Join(root, overlayFile), overlayMarker+"\n<Project/>")
		if _, err := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{Root: root}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(root, overlayFile)); err != nil {
			t.Error("a check removed the overlay")
		}
	})
}

// The swap breaks publicizers unless reference assemblies are off, and the
// overlay is the one place that can say so without editing a member.
func TestLocalOverlayDisablesReferenceAssemblies(t *testing.T) {
	ctx := context.Background()
	a := New()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "lib/src/Pty.Core/Pty.Core.csproj"), "<Project/>")
	redirects := []plugin.Redirect{{Package: "Pty.Core", Path: "lib/src/Pty.Core/Pty.Core.csproj"}}
	got, err := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{Root: root, Redirects: redirects})
	if err != nil {
		t.Fatal(err)
	}
	body := got.Files[overlayFile]
	if !strings.Contains(body, "<ProduceReferenceAssembly>false</ProduceReferenceAssembly>") {
		t.Fatalf("overlay leaves reference assemblies on:\n%s", body)
	}
	// Behind the same switch as the swaps, so the pristine build stays pristine.
	at := strings.Index(body, "<ProduceReferenceAssembly>")
	if pg := strings.LastIndex(body[:at], "<PropertyGroup"); pg < 0 || !strings.Contains(body[pg:at], "UseStackSources") {
		t.Errorf("the property is not conditioned on UseStackSources:\n%s", body)
	}

	// Before anything is written, a check says the overlay is missing: an
	// unwired stackspace builds fine against the registry and must not
	// look healthy.
	var missing bool
	for _, p := range got.Problems {
		if p.Path == overlayFile && strings.Contains(p.Message, "not written") && p.Fixable {
			missing = true
		}
	}
	if !missing {
		t.Fatalf("missing overlay not reported: %+v", got.Problems)
	}

	// An overlay from before the property existed is reported as stale by a
	// check, and rewritten by a write.
	old := strings.Replace(body, overlayNoRefAssemblies, "", 1)
	writeFile(t, filepath.Join(root, overlayFile), old)
	got, err = a.LocalOverlay(ctx, plugin.LocalOverlayRequest{Root: root, Redirects: redirects})
	if err != nil {
		t.Fatal(err)
	}
	var stale bool
	for _, p := range got.Problems {
		if p.Path == overlayFile && strings.Contains(p.Message, "out of date") {
			stale = true
		}
	}
	if !stale {
		t.Fatalf("stale overlay not reported: %+v", got.Problems)
	}
	if _, err := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{Root: root, Redirects: redirects, Write: true}); err != nil {
		t.Fatal(err)
	}
	now, _ := os.ReadFile(filepath.Join(root, overlayFile))
	if string(now) != body {
		t.Error("write did not bring the overlay up to date")
	}
	got, _ = a.LocalOverlay(ctx, plugin.LocalOverlayRequest{Root: root, Redirects: redirects})
	for _, p := range got.Problems {
		if strings.Contains(p.Message, "out of date") || strings.Contains(p.Message, "not written") {
			t.Errorf("still reported after a rewrite: %s", p.Message)
		}
	}
	// The same overlay with CRLF endings — what an autocrlf checkout hands
	// back — is not stale.
	writeFile(t, filepath.Join(root, overlayFile), strings.ReplaceAll(body, "\n", "\r\n"))
	got, _ = a.LocalOverlay(ctx, plugin.LocalOverlayRequest{Root: root, Redirects: redirects})
	for _, p := range got.Problems {
		if strings.Contains(p.Message, "out of date") {
			t.Error("CRLF line endings reported as staleness")
		}
	}
}

// Once nothing crosses between members, an overlay rig wrote earlier is
// reported by a check rather than passed over as "nothing to redirect"; a
// hand-written one is not rig's to call left over.
func TestLocalOverlayLeftOverWhenNothingCrosses(t *testing.T) {
	ctx := context.Background()
	a := New()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, overlayFile), overlayMarker+"\n<Project/>\n")
	got, err := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if got.Skipped || len(got.Problems) != 1 || !strings.Contains(got.Problems[0].Message, "left over") {
		t.Fatalf("leftover overlay: skipped=%v %+v, want it reported", got.Skipped, got.Problems)
	}
	writeFile(t, filepath.Join(root, overlayFile), "<Project><!-- mine --></Project>\n")
	if got, _ := a.LocalOverlay(ctx, plugin.LocalOverlayRequest{Root: root}); !got.Skipped || len(got.Problems) != 0 {
		t.Fatalf("hand-written overlay with no redirects: skipped=%v %+v", got.Skipped, got.Problems)
	}
}

func TestStackSourceNames(t *testing.T) {
	for id, want := range map[string]string{"Acme.Core": "Acme_Core", "9-a_b": "9_a_b", "é.a": "__a"} {
		if got := StackSourceName(id); got != want {
			t.Errorf("%q: %q != %q", id, got, want)
		}
	}
	body := renderOverlay([]plugin.Redirect{{Package: "Acme.Core", Path: "lib/Lib.csproj"}})
	for _, want := range []string{"<RigStackspace>true", "<RigStackspaceRoot>$(MSBuildThisFileDirectory)", "BeforeTargets=\"PrepareForBuild;CoreCompile\"", "@(_StackPresent->AnyHaveMetadataValue('Identity', 'Acme.Core'))", "<RigStackSource_Acme_Core>true", ";STACK_ACME_CORE", ";RIG_STACK"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
}

func TestStackImportUpgrade(t *testing.T) {
	old := `<Project><!-- Added by rig. historical explanation -->
 <PropertyGroup><StackParentTargets>$([MSBuild]::GetPathOfFileAbove('Directory.Build.targets', '$(MSBuildThisFileDirectory)../'))</StackParentTargets></PropertyGroup>
 <Import Project="$(StackParentTargets)" Condition="'$(StackParentTargets)' != ''" />
 <!-- member content --></Project>`
	if !legacyWalkUp(old) {
		t.Fatal("old block not detected")
	}
	got, ok := patchWalkUp(old)
	if !ok || legacyWalkUp(got) || strings.Count(got, "<Import ") != 1 || !strings.Contains(got, "'$(_RigStackParent)' == 'true'") || !strings.Contains(got, "member content") {
		t.Fatal(got)
	}
}

func TestStackSourceSignalsE2E(t *testing.T) {
	if os.Getenv("RIG_STACK_E2E") != "1" {
		t.Skip("set RIG_STACK_E2E=1 for real dotnet builds")
	}
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet unavailable")
	}
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("dotnet", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("dotnet %v: %v\n%s", args, err, out)
		}
	}
	writeFile(t, filepath.Join(root, "NuGet.Config"), `<configuration><packageSources><clear/><add key="fixture" value="./feed" /></packageSources></configuration>`)
	lib := filepath.Join(root, "lib/Lib.csproj")
	writeFile(t, lib, `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><PackageId>Acme.Core</PackageId><Version>1.0.0</Version></PropertyGroup></Project>`)
	writeFile(t, filepath.Join(root, "lib/Core.cs"), `public class Core { public static int Released => 1; }`)
	run("pack", lib, "-o", filepath.Join(root, "feed"))
	writeFile(t, filepath.Join(root, "lib/Core.cs"), `public class Core { public static int SourceOnly => 1; }`)
	overlay := renderOverlay([]plugin.Redirect{{Package: "Acme.Core", Path: "lib/Lib.csproj"}, {Package: "Acme.Unused", Path: "unused/Unused.csproj"}})
	writeFile(t, filepath.Join(root, overlayFile), overlay)
	writeFile(t, filepath.Join(root, "rig.stack.jsonc"), `{}`)
	member, _ := patchWalkUp(`<Project>
 <PropertyGroup><EvaluationSawSource Condition="'$(RigStackSource_Acme_Core)' == 'true'">true</EvaluationSawSource></PropertyGroup>
 <Target Name="CheckSignals" AfterTargets="RigStackSourceSignals" Condition="'$(UseStackSources)' != 'false'">
 <Error Condition="'$(RigStackspace)' != 'true' Or '$(RigStackspaceRoot)' == ''" Text="missing root signals" />
 <Error Condition="'$(RigStackSource_Acme_Core)' != 'true'" Text="missing source property" />
 <Error Condition="'$(RigStackSource_Acme_Unused)' != ''" Text="unused source property" />
 <Error Condition="'$(EvaluationSawSource)' != ''" Text="unexpected evaluation signal" />
 <PropertyGroup><TargetSawSource Condition="'$(RigStackSource_Acme_Core)' == 'true'">true</TargetSawSource></PropertyGroup>
 <Error Condition="'$(TargetSawSource)' != 'true'" Text="target property condition failed" />
 </Target>
 <Target Name="CheckDisabledSignals" BeforeTargets="CoreCompile" Condition="('$(UseStackSources)' == 'false' Or '$(_RigStackParent)' != 'true') And '$(LateOptOut)' != 'true'">
 <Error Condition="'$(RigStackspace)' != '' Or '$(RigStackspaceRoot)' != '' Or '$(RigStackSource_Acme_Core)' != ''" Text="disabled properties leaked" />
 </Target></Project>`)
	for _, mode := range []string{"stack", "stack-json", "stack-inline", "hand-owned", "optout", "late-optout", "plain", "plain-config"} {
		dir := "app"
		if strings.HasPrefix(mode, "plain") {
			dir = "plain/app"
			writeFile(t, filepath.Join(root, "plain", overlayFile), `<Project><Target Name="Unrelated" BeforeTargets="Build"><Error Text="unrelated ancestor imported" /></Target></Project>`)
		}
		for _, name := range []string{"rig.stack.jsonc", "rig.stack.json", ".rig.json"} {
			if err := os.Remove(filepath.Join(root, name)); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
		switch mode {
		case "stack", "hand-owned", "optout", "late-optout":
			writeFile(t, filepath.Join(root, "rig.stack.jsonc"), `{}`)
		case "stack-json":
			writeFile(t, filepath.Join(root, "rig.stack.json"), `{}`)
		case "stack-inline":
			writeFile(t, filepath.Join(root, ".rig.json"), `{"stack":{"repos":{}}}`)
		case "plain-config":
			writeFile(t, filepath.Join(root, "plain/.rig.json"), `{"primary":"dotnet"}`)
		}

		body := overlay
		if mode == "hand-owned" {
			body = strings.Replace(body, overlayMarker, "", 1)
		}
		writeFile(t, filepath.Join(root, overlayFile), body)
		t.Logf("building mode %s", mode)
		writeFile(t, filepath.Join(root, dir, overlayFile), member)
		writeFile(t, filepath.Join(root, dir, "Directory.Build.props"), `<Project><PropertyGroup><MemberSawStack>$(RigStackspace)</MemberSawStack></PropertyGroup></Project>`)
		if !strings.HasPrefix(mode, "plain") {
			writeFile(t, filepath.Join(root, overlayFile), overlay)
			writeFile(t, filepath.Join(root, "Directory.Build.props"), renderStackProps([]plugin.Redirect{{Package: "Acme.Core", Path: "lib/Lib.csproj"}, {Package: "Acme.Unused", Path: "unused/Unused.csproj"}}))
			_, err := New().LocalOverlay(context.Background(), plugin.LocalOverlayRequest{Root: root, Redirects: []plugin.Redirect{{Package: "Acme.Core", Path: "lib/Lib.csproj"}, {Package: "Acme.Unused", Path: "unused/Unused.csproj"}}, Write: true, Writable: []string{dir}})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "hand-owned" {
				for _, file := range []string{overlayFile, "Directory.Build.props"} {
					data, err := os.ReadFile(filepath.Join(root, file))
					if err != nil {
						t.Fatal(err)
					}
					writeFile(t, filepath.Join(root, file), strings.Replace(string(data), overlayMarker, "", 1))
				}
			}
		} else {
			patched, _ := patchWalkUpFile(`<Project></Project>`, "Directory.Build.props")
			writeFile(t, filepath.Join(root, dir, "Directory.Build.props"), patched)
			writeFile(t, filepath.Join(root, "plain/Directory.Build.props"), `<Project><PropertyGroup><RigStackspace>unrelated</RigStackspace></PropertyGroup></Project>`)
		}
		writeFile(t, filepath.Join(root, dir, "App.csproj"), `<Project Sdk="Microsoft.NET.Sdk">
<PropertyGroup><TargetFramework>net10.0</TargetFramework></PropertyGroup>
<PropertyGroup Condition="'$(RigStackspace)' == 'true'"><BodySawStack>true</BodySawStack></PropertyGroup>
<ItemGroup Condition="'$(RigStackAvailable_Acme_Core)' == 'true'"><EarlySource Include="Acme.Core" /></ItemGroup>
<ItemGroup><PackageReference Include="Acme.Core" Version="1.0.0" /><PackageReference Include="Acme.Unused" Version="1.0.0" Condition="'$(Configuration)' == 'Release'" /></ItemGroup>
<Target Name="CheckEarly" BeforeTargets="CoreCompile">
<Error Condition="'$(RigStackspace)' == 'true' And ('$(BodySawStack)' != 'true' Or '@(EarlySource)' != 'Acme.Core' Or '$(MemberSawStack)' != 'true')" Text="early conditions failed" />
<Error Condition="'$(RigStackspace)' != 'true' And ('$(BodySawStack)' != '' Or '@(EarlySource)' != '' Or '$(RigStackAvailable_Acme_Core)' != '')" Text="early signals leaked" />
</Target></Project>`)
		if mode == "late-optout" {
			path := filepath.Join(root, dir, "App.csproj")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			body := strings.Replace(string(data), "<Target Name=\"CheckEarly\"", `<PropertyGroup><UseStackSources>false</UseStackSources><LateOptOut>true</LateOptOut></PropertyGroup>
<Target Name="CheckLate" BeforeTargets="CoreCompile"><Error Condition="'$(RigStackAvailable_Acme_Core)' != 'true' Or '$(RigStackSource_Acme_Core)' != ''" Text="late optout semantics failed" /></Target>
<Target Name="CheckEarly"`, 1)
			writeFile(t, path, body)
		}
		writeFile(t, filepath.Join(root, dir, "App.cs"), `#if STACK_ACME_UNUSED
#error unused sibling constant
#endif
#if STACK_ACME_CORE
#if !RIG_STACK
#error missing stack constant
#endif
public class App { int value = Core.SourceOnly; }
#else
#if RIG_STACK
#error unexpected stack constant
#endif
public class App { int value = Core.Released; }
#endif
`)
		args := []string{"build", filepath.Join(root, dir, "App.csproj"), "--no-incremental", "-p:RestorePackagesPath=" + filepath.Join(root, "packages")}
		if mode == "optout" {
			args = append(args, "-p:UseStackSources=false")
		}
		run(args...)
	}
}

func TestLocalOverlayLegacyImport(t *testing.T) {
	const legacy = `<Project><!-- Added by rig. historical explanation -->
 <PropertyGroup><StackParentTargets>$([MSBuild]::GetPathOfFileAbove('Directory.Build.targets', '$(MSBuildThisFileDirectory)../'))</StackParentTargets></PropertyGroup>
 <Import Project="$(StackParentTargets)" Condition="'$(StackParentTargets)' != ''" />
 <!-- member content --></Project>`
	for _, tc := range []struct {
		name            string
		write, writable bool
	}{
		{"wire upgrades writable member", true, true},
		{"wire leaves non-writable member working", true, false},
		{"doctor advises without editing", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			redirects := []plugin.Redirect{{Package: "Acme.Core", Path: "lib/Lib.csproj"}}
			writeFile(t, filepath.Join(root, "lib/Lib.csproj"), "<Project/>")
			path := filepath.Join(root, "app", overlayFile)
			writeFile(t, path, legacy)
			writeFile(t, filepath.Join(root, overlayFile), renderOverlay(redirects))
			writeFile(t, filepath.Join(root, "Directory.Build.props"), renderStackProps(redirects))
			req := plugin.LocalOverlayRequest{Root: root, Redirects: redirects, Write: tc.write}
			if tc.writable {
				req.Writable = []string{"app"}
			}
			got, err := New().LocalOverlay(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Problems) != 0 {
				t.Fatalf("working import reported as blocking: %+v", got.Problems)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if tc.write && tc.writable {
				if len(got.Fixed) != 1 || len(got.Notes) != 0 || legacyWalkUp(string(after)) || !strings.Contains(string(after), "'$(_RigStackParent)' == 'true'") || !strings.Contains(string(after), "member content") {
					t.Fatalf("upgrade: %+v\n%s", got, after)
				}
				again, err := New().LocalOverlay(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				twice, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if len(again.Fixed) != 0 || len(again.Problems) != 0 || len(again.Notes) != 0 || string(after) != string(twice) {
					t.Fatalf("second wire changed the import: %+v", again)
				}
			} else {
				if len(got.Fixed) != 0 || string(after) != legacy || len(got.Notes) != 1 {
					t.Fatalf("advisory changed member: %+v\n%s", got, after)
				}
				for _, want := range []string{"app/Directory.Build.targets", "still works", "not gated", "plain clone", "unrelated Directory.Build.targets"} {
					if !strings.Contains(got.Notes[0], want) {
						t.Errorf("missing %q: %s", want, got.Notes[0])
					}
				}
				advice := "not writable"
				if tc.writable {
					advice = "re-run `rig stack wire`"
				}
				if !strings.Contains(got.Notes[0], advice) {
					t.Fatal(got.Notes)
				}
			}
		})
	}
}

func TestLocalOverlayUnrecognizedLegacyImport(t *testing.T) {
	root := t.TempDir()
	// The original block has been reformatted: it works, but rig cannot safely
	// recognize its exact boundaries for replacement.
	legacy := `<Project><!-- Added by rig. historical explanation -->
 <PropertyGroup><StackParentTargets>$([MSBuild]::GetPathOfFileAbove('Directory.Build.targets', '$(MSBuildThisFileDirectory)../'))</StackParentTargets></PropertyGroup>
 <Import Condition="'$(StackParentTargets)' != ''" Project="$(StackParentTargets)" />
 </Project>`
	path := filepath.Join(root, "app", overlayFile)
	writeFile(t, path, legacy)
	writeFile(t, filepath.Join(root, "lib/Lib.csproj"), `<Project/>`)
	req := plugin.LocalOverlayRequest{Root: root, Redirects: []plugin.Redirect{{Package: "Acme.Core", Path: "lib/Lib.csproj"}}, Write: true, Writable: []string{"app"}}
	for i := 0; i < 2; i++ {
		got, err := New().LocalOverlay(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != legacy || len(got.Fixed) != 0 || len(got.Notes) != 1 || len(got.Problems) != 0 {
			t.Fatalf("unsafe upgrade: %+v\n%s", got, after)
		}
	}
}

func TestLocalOverlayUpgradesManifestGate(t *testing.T) {
	root := t.TempDir()
	old := `<Project><!-- rig:stack-import -->
 <PropertyGroup><StackManifest>$([MSBuild]::GetPathOfFileAbove('rig.stack.jsonc', '$(MSBuildThisFileDirectory)../'))</StackManifest>
 <StackParentTargets>$([MSBuild]::GetPathOfFileAbove('Directory.Build.targets', '$(MSBuildThisFileDirectory)../'))</StackParentTargets></PropertyGroup>
 <Import Project="$(StackParentTargets)" Condition="Exists('$(StackManifest)')" />
 <!-- /rig:stack-import --><!-- mine --></Project>`
	path := filepath.Join(root, "app", overlayFile)
	writeFile(t, path, old)
	writeFile(t, filepath.Join(root, "lib/Lib.csproj"), `<Project/>`)
	req := plugin.LocalOverlayRequest{Root: root, Redirects: []plugin.Redirect{{Package: "Acme.Core", Path: "lib/Lib.csproj"}}, Write: true, Writable: []string{"app"}}
	got, err := New().LocalOverlay(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Fixed) != 1 || len(got.Problems) != 0 || strings.Contains(string(after), "<StackManifest>") || strings.Count(string(after), "<Import ") != 1 || !strings.Contains(string(after), "<!-- mine -->") {
		t.Fatalf("upgrade failed: %+v\n%s", got, after)
	}
	got, err = New().LocalOverlay(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Fixed) != 0 {
		t.Fatal(got)
	}
}

func TestLocalOverlayPropsLifecycle(t *testing.T) {
	root := t.TempDir()
	req := plugin.LocalOverlayRequest{Root: root, Redirects: []plugin.Redirect{{Package: "Acme.Core", Path: "lib/Lib.csproj"}}, Write: true, Writable: []string{"app"}}
	writeFile(t, filepath.Join(root, "lib/Lib.csproj"), "<Project/>")
	for _, file := range []string{overlayFile, "Directory.Build.props"} {
		writeFile(t, filepath.Join(root, "app", file), "<Project><!-- member --></Project>")
		writeFile(t, filepath.Join(root, "readonly", file), "<Project><!-- member --></Project>")
	}
	a := New()
	got, err := a.LocalOverlay(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Fixed) != 2 {
		t.Fatalf("patches: %+v", got)
	}
	for _, file := range []string{overlayFile, "Directory.Build.props"} {
		data, _ := os.ReadFile(filepath.Join(root, "app", file))
		if strings.Count(string(data), "<!-- rig:stack-import -->") != 1 || !strings.Contains(string(data), "<!-- member -->") {
			t.Fatalf("bad patch: %s", data)
		}
		data, _ = os.ReadFile(filepath.Join(root, "readonly", file))
		if strings.Contains(string(data), "rig:stack-import") {
			t.Fatal("patched readonly member")
		}
	}
	got, err = a.LocalOverlay(context.Background(), req)
	if err != nil || len(got.Fixed) != 0 {
		t.Fatalf("not idempotent: %+v %v", got, err)
	}
	writeFile(t, filepath.Join(root, "Directory.Build.props"), renderStackProps(nil))
	req.Write = false
	got, err = a.LocalOverlay(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	stale := false
	for _, note := range got.Notes {
		if strings.Contains(note, "Directory.Build.props: out of date") {
			stale = true
		}
	}
	if !stale {
		t.Fatalf("stale props not reported: %+v", got)
	}
	req.Write = true
	if _, err = a.LocalOverlay(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	req.Redirects = nil
	got, err = a.LocalOverlay(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Removed) != 2 || len(got.Fixed) != 2 {
		t.Fatalf("cleanup: %+v", got)
	}
	for _, file := range []string{overlayFile, "Directory.Build.props"} {
		if _, err := os.Stat(filepath.Join(root, file)); !os.IsNotExist(err) {
			t.Fatalf("root file remains: %s", file)
		}
		data, _ := os.ReadFile(filepath.Join(root, "app", file))
		if strings.Contains(string(data), "rig:stack-import") || !strings.Contains(string(data), "<!-- member -->") {
			t.Fatalf("cleanup member: %s", data)
		}
		writeFile(t, filepath.Join(root, file), "<Project><!-- hand owned --></Project>")
	}
	got, err = a.LocalOverlay(context.Background(), req)
	if err != nil || len(got.Removed) != 0 {
		t.Fatalf("hand owned removed: %+v %v", got, err)
	}
}

func TestStackSourcePropsText(t *testing.T) {
	body := renderStackProps([]plugin.Redirect{{Package: "Acme.Core", Path: "lib/Lib.csproj"}})
	for _, want := range []string{overlayMarker, "<RigStackspace>true</RigStackspace>", "<RigStackspaceRoot>$(MSBuildThisFileDirectory)</RigStackspaceRoot>", "<RigStackAvailable_Acme_Core>true</RigStackAvailable_Acme_Core>", "'$(UseStackSources)' != 'false'"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q: %s", want, body)
		}
	}
	for _, bad := range []string{"<RigStackSource_", "<ItemGroup", "<Target ", "DefineConstants"} {
		if strings.Contains(body, bad) {
			t.Errorf("late signal leaked: %s", body)
		}
	}
	old := `<Project><!-- rig:stack-import --><Import Project="old" /><!-- /rig:stack-import --><!-- member --></Project>`
	patched, ok := patchWalkUpFile(old, "Directory.Build.props")
	if !ok || strings.Contains(patched, `Project="old"`) || !strings.Contains(patched, "ReadAllText") || !strings.Contains(patched, "Directory.Build.props") || !strings.Contains(patched, "<!-- member -->") {
		t.Fatal(patched)
	}
	again, ok := patchWalkUpFile(patched, "Directory.Build.props")
	if !ok || strings.Count(again, "<!-- rig:stack-import -->") != 1 {
		t.Fatal(again)
	}
}

func TestLocalOverlayHandOwnedPropsAreAdvisory(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "lib/Lib.csproj"), "<Project/>")
	const handOwned = "<Project><!-- maintained by you --></Project>"
	writeFile(t, filepath.Join(root, "Directory.Build.props"), handOwned)
	got, err := New().LocalOverlay(context.Background(), plugin.LocalOverlayRequest{Root: root, Write: true, Redirects: []plugin.Redirect{{Package: "Acme.Core", Path: "lib/Lib.csproj"}}})
	if err != nil {
		t.Fatalf("optional props blocked wiring: %v", err)
	}
	if len(got.Notes) == 0 || len(got.Problems) != 0 {
		t.Fatalf("want advisory: %+v", got)
	}
	data, _ := os.ReadFile(filepath.Join(root, "Directory.Build.props"))
	if string(data) != handOwned {
		t.Fatal("hand-owned props changed")
	}
}

func TestStackPropsImportExact(t *testing.T) {
	got, ok := patchWalkUpFile("<Project><!-- member --></Project>", "Directory.Build.props")
	const want = `<Project>

  <!-- rig:stack-import -->
  <PropertyGroup>
    <StackParentTargets>$([MSBuild]::GetPathOfFileAbove('Directory.Build.props', '$(MSBuildThisFileDirectory)../'))</StackParentTargets>
    <_RigStackParent>false</_RigStackParent>
    <_RigStackParent Condition="Exists('$(StackParentTargets)')">$([System.IO.File]::ReadAllText('$(StackParentTargets)').Contains('&lt;!-- generated by rig: package references redirected to sources in this tree --&gt;'))</_RigStackParent>
    <_RigStackParent Condition="Exists('$(StackParentTargets)') And '$(_RigStackParent)' != 'true'">$([System.IO.File]::ReadAllText('$(StackParentTargets)').Contains('&lt;!-- rig:stack-import --&gt;'))</_RigStackParent>
    <_RigStackParent Condition="Exists('$(StackParentTargets)') And '$(_RigStackParent)' != 'true'">$([System.IO.File]::ReadAllText('$(StackParentTargets)').Contains('&lt;RigStackspace&gt;true&lt;/RigStackspace&gt;'))</_RigStackParent>
  </PropertyGroup>
  <Import Project="$(StackParentTargets)" Condition="'$(_RigStackParent)' == 'true'" />
  <!-- /rig:stack-import -->
<!-- member --></Project>`
	if !ok || got != want {
		t.Fatalf("props import differs:\n%s", got)
	}
}

func TestLocalOverlayTargetsDiagnosticsRemainStrong(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "lib/Lib.csproj"), "<Project/>")
	writeFile(t, filepath.Join(root, "app", overlayFile), "<Project></Project>")
	req := plugin.LocalOverlayRequest{Root: root, Redirects: []plugin.Redirect{{Package: "Acme.Core", Path: "lib/Lib.csproj"}}}
	got, err := New().LocalOverlay(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var missing, shadow bool
	for _, p := range got.Problems {
		if p.Path == overlayFile && strings.Contains(p.Message, "not written — every project here still resolves these packages from the registry") {
			missing = true
		}
		if p.Path == "app/"+overlayFile && strings.Contains(p.Message, "keep resolving packages from the registry") {
			shadow = true
		}
	}
	if !missing || !shadow {
		t.Fatalf("weak targets diagnostics: %+v", got)
	}
	writeFile(t, filepath.Join(root, overlayFile), "<Project><!-- yours --></Project>")
	req.Write = true
	if _, err := New().LocalOverlay(context.Background(), req); err == nil {
		t.Fatal("foreign targets accepted")
	}
}

func TestLocalOverlayPreservesCustomizedLegacyBlock(t *testing.T) {
	const comment = `<!-- Added by rig. historical explanation -->`
	const property = `<PropertyGroup><StackParentTargets>$([MSBuild]::GetPathOfFileAbove('Directory.Build.targets', '$(MSBuildThisFileDirectory)../'))</StackParentTargets></PropertyGroup>`
	const importElement = `<Import Project="$(StackParentTargets)" Condition="'$(StackParentTargets)' != ''" />`
	for _, custom := range []string{
		`<PropertyGroup><MemberProperty>keep me</MemberProperty></PropertyGroup>`,
		`<Target Name="MemberTarget"><Message Text="keep me" /></Target>`,
		`<!-- member comment: keep me -->`,
	} {
		for _, position := range []string{"before property", "before import", "inside property"} {
			t.Run(position+"/"+custom, func(t *testing.T) {
				block := comment + "\n" + property + "\n" + importElement
				switch position {
				case "before property":
					block = comment + custom + property + importElement
				case "before import":
					block = comment + property + custom + importElement
				case "inside property":
					block = comment + strings.Replace(property, "</PropertyGroup>", custom+"</PropertyGroup>", 1) + importElement
				}
				body := "<Project>" + block + "</Project>"
				root := t.TempDir()
				path := filepath.Join(root, "app", overlayFile)
				writeFile(t, path, body)
				writeFile(t, filepath.Join(root, "lib/Lib.csproj"), `<Project/>`)
				req := plugin.LocalOverlayRequest{Root: root, Redirects: []plugin.Redirect{{Package: "Acme.Core", Path: "lib/Lib.csproj"}}, Write: true, Writable: []string{"app"}}
				for i := 0; i < 2; i++ {
					got, err := New().LocalOverlay(context.Background(), req)
					if err != nil {
						t.Fatal(err)
					}
					after, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					if string(after) != body || len(got.Fixed) != 0 || len(got.Notes) != 1 || len(got.Problems) != 0 {
						t.Fatalf("member content changed: %+v\n%s", got, after)
					}
				}
			})
		}
	}
}

func TestLocalOverlayPreservesCustomizedLegacyPropsBlock(t *testing.T) {
	const comment = `<!-- Added by rig. historical explanation -->`
	const property = `<PropertyGroup><StackParentTargets>$([MSBuild]::GetPathOfFileAbove('Directory.Build.props', '$(MSBuildThisFileDirectory)../'))</StackParentTargets></PropertyGroup>`
	const importElement = `<Import Project="$(StackParentTargets)" Condition="'$(StackParentTargets)' != ''" />`
	for _, custom := range []string{
		`<PropertyGroup><MemberProperty>keep me</MemberProperty></PropertyGroup>`,
		`<Target Name="MemberTarget"><Message Text="keep me" /></Target>`,
		`<!-- member comment: keep me -->`,
	} {
		for _, position := range []string{"before property", "before import", "inside property"} {
			t.Run(position+"/"+custom, func(t *testing.T) {
				block := comment + "\n" + property + "\n" + importElement
				switch position {
				case "before property":
					block = comment + custom + property + importElement
				case "before import":
					block = comment + property + custom + importElement
				case "inside property":
					block = comment + strings.Replace(property, "</PropertyGroup>", custom+"</PropertyGroup>", 1) + importElement
				}
				body := "<Project>" + block + "</Project>"
				root := t.TempDir()
				path := filepath.Join(root, "app", "Directory.Build.props")
				writeFile(t, path, body)
				writeFile(t, filepath.Join(root, "lib/Lib.csproj"), `<Project/>`)
				req := plugin.LocalOverlayRequest{Root: root, Redirects: []plugin.Redirect{{Package: "Acme.Core", Path: "lib/Lib.csproj"}}, Write: true, Writable: []string{"app"}}
				for i := 0; i < 2; i++ {
					got, err := New().LocalOverlay(context.Background(), req)
					if err != nil {
						t.Fatal(err)
					}
					after, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					if string(after) != body || len(got.Fixed) != 0 || len(got.Notes) != 1 || len(got.Problems) != 0 {
						t.Fatalf("member content changed: %+v\n%s", got, after)
					}
				}
			})
		}
	}
}

func TestLocalOverlayCIGuard(t *testing.T) {
	props := renderStackProps([]plugin.Redirect{{Package: "Acme.Core", Path: "lib/Lib.csproj"}})
	if strings.Contains(props, "RigStackCI") || strings.Contains(props, "WarningsNotAsErrors") {
		t.Fatal("guard policy belongs in targets, not evaluation props")
	}
	body := renderOverlay([]plugin.Redirect{{Package: "Acme.Core", Path: "lib/Lib.csproj"}})
	for _, want := range []string{"BeforeTargets=\"_GenerateRestoreProjectSpec;CoreCompile\"", "'$(RigStackCIGuard)' == 'true' And '@(_StackPresent)' != ''", "<WarningsNotAsErrors>$(WarningsNotAsErrors);NU1603;NU1608;NU1605</WarningsNotAsErrors>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Contains(body, "<NoWarn>") || strings.Contains(body, "MSB3277") {
		t.Fatal("broad suppression emitted")
	}
}
