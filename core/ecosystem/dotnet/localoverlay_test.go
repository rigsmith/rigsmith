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
	if !ok || legacyWalkUp(got) || strings.Count(got, "<Import ") != 1 || !strings.Contains(got, "Exists('$(StackManifest)')") || !strings.Contains(got, "member content") {
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
 <Target Name="CheckDisabledSignals" BeforeTargets="CoreCompile" Condition="'$(UseStackSources)' == 'false' Or '$(StackManifest)' == ''">
 <Error Condition="'$(RigStackspace)' != '' Or '$(RigStackspaceRoot)' != '' Or '$(RigStackSource_Acme_Core)' != ''" Text="disabled properties leaked" />
 </Target></Project>`)
	for _, mode := range []string{"stack", "optout", "plain"} {
		dir := "app"
		if mode == "plain" {
			dir = "plain/app"
			writeFile(t, filepath.Join(root, "plain", overlayFile), `<Project><Target Name="Unrelated" BeforeTargets="Build"><Error Text="unrelated ancestor imported" /></Target></Project>`)
		}
		// A plain clone has the rig-owned import, but no stack manifest above it.
		if mode == "plain" {
			if err := os.Remove(filepath.Join(root, "rig.stack.jsonc")); err != nil {
				t.Fatal(err)
			}
		}
		writeFile(t, filepath.Join(root, dir, overlayFile), member)
		writeFile(t, filepath.Join(root, dir, "App.csproj"), `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="Acme.Core" Version="1.0.0" /></ItemGroup></Project>`)
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
				if len(got.Fixed) != 1 || len(got.Notes) != 0 || legacyWalkUp(string(after)) || !strings.Contains(string(after), "Exists('$(StackManifest)')") || !strings.Contains(string(after), "member content") {
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
