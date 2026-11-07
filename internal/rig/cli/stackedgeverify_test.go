package cli

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func edgeTestNupkg(t *testing.T, dir, file, id, version, dependencies, assembly string) {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, file))
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	stream, err := z.Create("package.nuspec")
	if err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintf(stream, `<package><metadata><id>%s</id><version>%s</version><dependencies>%s</dependencies></metadata></package>`, id, version, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if assembly != "" {
		stream, err = z.Create("lib/net10.0/" + assembly + ".dll")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Write([]byte("assembly fixture")); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func edgeTestExpectations() stackEdgeVerifyRequest {
	return stackEdgeVerifyRequest{
		Version: "1.2.4-edge.7", MaxSize: 100_000_000,
		MemberIDs:      map[string]string{"Lib": "You.Lib", "Lib.Controls": "You.Lib.Controls", "Lib.Samples": ""},
		OtherMemberIDs: []string{"Sibling", "You.Sibling"},
		Packages: []stackEdgeExpectedPackage{
			{OriginalID: "Lib", ID: "You.Lib", AssemblyBefore: "Lib", AssemblyAfter: "Lib"},
			{OriginalID: "Lib.Controls", ID: "You.Lib.Controls", AssemblyBefore: "Lib.Controls", AssemblyAfter: "Lib.Controls", Dependencies: []stackEdgeDependency{
				{ID: "You.Lib", Version: "1.2.4-edge.7"}, {ID: "ThirdParty", Version: "[3.2.1]"},
			}},
		},
	}
}

func TestStackEdgeVerify(t *testing.T) {
	const dependencies = `<group targetFramework="net10.0"><dependency id="You.Lib" version="1.2.4-edge.7"/><dependency id="ThirdParty" version="[3.2.1]"/></group>`
	for _, tc := range []struct {
		name, wantError string
		change          func(*stackEdgeVerifyRequest, *string, *string, *string, *string)
	}{
		{name: "passing"},
		{name: "check-1-original", wantError: "not a republished", change: func(_ *stackEdgeVerifyRequest, id, _, _, _ *string) { *id = "Lib.Controls" }},
		{name: "check-1-excluded", wantError: "not a republished", change: func(_ *stackEdgeVerifyRequest, id, _, _, _ *string) { *id = "You.Lib.Samples" }},
		{name: "check-1-unexpected", wantError: "not a republished", change: func(_ *stackEdgeVerifyRequest, id, _, _, _ *string) { *id = "Unrelated" }},
		{name: "check-2-original-dependency", wantError: "member dependency Lib@", change: func(_ *stackEdgeVerifyRequest, _, deps, _, _ *string) {
			*deps = strings.ReplaceAll(*deps, `id="You.Lib"`, `id="Lib"`)
		}},
		{name: "check-2-wrong-version", wantError: "must name a non-excluded republished id", change: func(_ *stackEdgeVerifyRequest, _, deps, _, _ *string) {
			*deps = strings.ReplaceAll(*deps, "1.2.4-edge.7", "1.2.4-edge.6")
		}},
		{name: "check-2-edge-over-discovery", wantError: "must name a non-excluded republished id", change: func(w *stackEdgeVerifyRequest, _, deps, _, _ *string) {
			*deps = strings.ReplaceAll(*deps, "1.2.4-edge.7", "1.2.4-edge.6")
			w.Packages[1].Dependencies[0].Version = "1.2.4-edge.6"
		}},
		{name: "check-2-excluded-dependency", wantError: "member dependency Lib.Samples@", change: func(_ *stackEdgeVerifyRequest, _, deps, _, _ *string) {
			*deps = strings.ReplaceAll(*deps, `id="You.Lib"`, `id="Lib.Samples"`)
		}},
		{name: "check-3-other-member", wantError: "another member's package You.Sibling", change: func(_ *stackEdgeVerifyRequest, _, deps, _, _ *string) {
			*deps += `<dependency id="You.Sibling" version="1.2.4-edge.7"/>`
		}},
		{name: "check-4-changed-pin", wantError: "differs from discovery", change: func(_ *stackEdgeVerifyRequest, _, deps, _, _ *string) {
			*deps = strings.ReplaceAll(*deps, "[3.2.1]", "3.2.1")
		}},
		{name: "check-4-new-dependency", wantError: "differs from discovery", change: func(_ *stackEdgeVerifyRequest, _, deps, _, _ *string) {
			*deps += `<dependency id="Unexpected" version="1.0.0"/>`
		}},
		{name: "check-4-missing-dependency", wantError: "from discovery is missing", change: func(_ *stackEdgeVerifyRequest, _, deps, _, _ *string) {
			*deps = strings.ReplaceAll(*deps, `<dependency id="ThirdParty" version="[3.2.1]"/>`, "")
		}},
		{name: "check-4-moved-framework", wantError: "differs from discovery", change: func(w *stackEdgeVerifyRequest, _, _, _, _ *string) {
			w.Packages[1].Dependencies[1].Framework = "net9.0"
		}},
		{name: "check-5-evaluated-assembly", wantError: "AssemblyName changed", change: func(w *stackEdgeVerifyRequest, _, _, _, _ *string) { w.Packages[1].AssemblyAfter = "You.Lib.Controls" }},
		{name: "check-5-packed-assembly", wantError: "expected assembly Lib.Controls.dll is missing", change: func(_ *stackEdgeVerifyRequest, _, _, assembly, _ *string) { *assembly = "You.Lib.Controls" }},
		{name: "check-6-size", wantError: "exceeds maxSize", change: func(w *stackEdgeVerifyRequest, _, _, _, _ *string) { w.MaxSize = 1 }},
		{name: "package-version", wantError: "differs from edge version", change: func(_ *stackEdgeVerifyRequest, _, _, _, version *string) { *version = "1.2.4" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := edgeTestExpectations()
			id, deps, assembly, version := "You.Lib.Controls", dependencies, "Lib.Controls", want.Version
			if tc.change != nil {
				tc.change(&want, &id, &deps, &assembly, &version)
			}
			dir := t.TempDir()
			edgeTestNupkg(t, dir, "first.nupkg", "You.Lib", want.Version, "", "Lib")
			edgeTestNupkg(t, dir, "second.nupkg", id, version, deps, assembly)
			reports, err := stackEdgeVerify(dir, want)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error = %v; want %q", err, tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(reports) != 2 || reports[0].Size == 0 || reports[1].Size == 0 {
				t.Fatalf("reports = %+v", reports)
			}
		})
	}
}

func TestStackEdgeVerifyMissingAndDuplicate(t *testing.T) {
	want := edgeTestExpectations()
	dir := t.TempDir()
	if _, err := stackEdgeVerify(dir, want); err == nil || !strings.Contains(err.Error(), "no observed nupkg") {
		t.Fatalf("missing outputs: %v", err)
	}
	edgeTestNupkg(t, dir, "one.nupkg", "You.Lib", want.Version, "", "Lib")
	edgeTestNupkg(t, dir, "two.nupkg", "You.Lib", want.Version, "", "Lib")
	if _, err := stackEdgeVerify(dir, want); err == nil || !strings.Contains(err.Error(), "duplicate output") {
		t.Fatalf("duplicate: %v", err)
	}
}

func TestStackEdgeVerifyRejectsExtraOriginal(t *testing.T) {
	want := edgeTestExpectations()
	dir := t.TempDir()
	edgeTestNupkg(t, dir, "extra.nupkg", "Lib", want.Version, "", "Lib")
	edgeTestNupkg(t, dir, "lib.nupkg", "You.Lib", want.Version, "", "Lib")
	edgeTestNupkg(t, dir, "controls.nupkg", "You.Lib.Controls", want.Version, `<dependency id="You.Lib" version="1.2.4-edge.7"/><dependency id="ThirdParty" version="[3.2.1]"/>`, "Lib.Controls")
	if _, err := stackEdgeVerify(dir, want); err == nil || !strings.Contains(err.Error(), "not a republished") {
		t.Fatalf("extra original: %v", err)
	}
}

func TestStackEdgeVerifyPlatformFrameworks(t *testing.T) {
	for _, tc := range []struct {
		name, expected, packed string
		matches                bool
	}{
		{name: "SDK-fills-platform-version", expected: "net10.0-windows", packed: "net10.0-windows7.0", matches: true},
		{name: "nuspec-omits-platform-version", expected: "net10.0-windows7.0", packed: "net10.0-windows", matches: true},
		{name: "same-explicit-version", expected: "net10.0-windows7.0", packed: "net10.0-windows7.0", matches: true},
		{name: "different-explicit-versions", expected: "net10.0-windows7.0", packed: "net10.0-windows10.0.19041.0"},
		{name: "different-platform", expected: "net10.0-windows", packed: "net10.0-android34.0"},
		{name: "different-base", expected: "net9.0-windows", packed: "net10.0-windows7.0"},
		{name: "platform-absent", expected: "net10.0", packed: "net10.0-windows7.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := edgeTestExpectations()
			want.Packages[1].Dependencies[1].Framework = tc.expected
			dir := t.TempDir()
			edgeTestNupkg(t, dir, "lib.nupkg", "You.Lib", want.Version, "", "Lib")
			deps := fmt.Sprintf(`<group targetFramework="%s"><dependency id="You.Lib" version="1.2.4-edge.7"/><dependency id="ThirdParty" version="[3.2.1]"/></group>`, tc.packed)
			edgeTestNupkg(t, dir, "controls.nupkg", "You.Lib.Controls", want.Version, deps, "Lib.Controls")
			_, err := stackEdgeVerify(dir, want)
			if tc.matches {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "differs from discovery") {
				t.Fatalf("framework %s vs %s: %v", tc.expected, tc.packed, err)
			}
		})
	}
}

func TestStackEdgeVerifyMultiTargetDependencies(t *testing.T) {
	for _, tc := range []struct {
		name             string
		expected, packed []string
		wantError        string
	}{
		{name: "omitted-and-explicit", expected: []string{"net10.0-windows", "net10.0-windows10.0.19041.0"}, packed: []string{"net10.0-windows7.0", "net10.0-windows10.0.19041.0"}},
		{name: "preserve-later-exact-match", expected: []string{"net10.0-windows7.0", "net10.0-windows10.0.19041.0"}, packed: []string{"net10.0-windows", "net10.0-windows7.0"}},
		{name: "move-broad-fallback", expected: []string{"", "net10.0-windows", "net10.0-windows7.0"}, packed: []string{"net10.0-windows10.0.19041.0", "net10.0-windows7.0", "net10.0-android34.0"}},
		{name: "missing-group", expected: []string{"net10.0-windows", "net10.0-windows10.0.19041.0"}, packed: []string{"net10.0-windows7.0"}, wantError: "from discovery is missing"},
		{name: "extra-duplicate", expected: []string{"net10.0-windows"}, packed: []string{"net10.0-windows7.0", "net10.0-windows7.0"}, wantError: "differs from discovery"},
		{name: "wrong-explicit-version", expected: []string{"net10.0-windows7.0", "net10.0-windows10.0.19041.0"}, packed: []string{"net10.0-windows7.0", "net10.0-windows11.0"}, wantError: "differs from discovery"},
	} {
		for _, reverseExpected := range []bool{false, true} {
			for _, reversePacked := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/reverse-expected-%t/reverse-packed-%t", tc.name, reverseExpected, reversePacked), func(t *testing.T) {
					expected, packed := slices.Clone(tc.expected), slices.Clone(tc.packed)
					if reverseExpected {
						slices.Reverse(expected)
					}
					if reversePacked {
						slices.Reverse(packed)
					}
					want := edgeTestExpectations()
					want.Packages[1].Dependencies = []stackEdgeDependency{{ID: "You.Lib", Version: want.Version}}
					for _, framework := range expected {
						want.Packages[1].Dependencies = append(want.Packages[1].Dependencies, stackEdgeDependency{ID: "ThirdParty", Version: "[3.2.1]", Framework: framework})
					}
					deps := `<dependency id="You.Lib" version="1.2.4-edge.7"/>`
					for _, framework := range packed {
						deps += fmt.Sprintf(`<group targetFramework="%s"><dependency id="ThirdParty" version="[3.2.1]"/></group>`, framework)
					}
					dir := t.TempDir()
					edgeTestNupkg(t, dir, "lib.nupkg", "You.Lib", want.Version, "", "Lib")
					edgeTestNupkg(t, dir, "controls.nupkg", "You.Lib.Controls", want.Version, deps, "Lib.Controls")
					_, err := stackEdgeVerify(dir, want)
					if tc.wantError == "" {
						if err != nil {
							t.Fatal(err)
						}
					} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
						t.Fatalf("error = %v; want %q", err, tc.wantError)
					}
				})
			}
		}
	}
}

func TestStackEdgeRange(t *testing.T) {
	for a, b := range map[string]string{"1.2": "1.2.0", "[1.2, )": "1.2.0", "[1.2]": "[1.2.0]", "(1.0, 2.0]": "(1.0.0,2.0.0]"} {
		if got := stackEdgeRange(a); got != b {
			t.Errorf("range %s = %s, want %s", a, got, b)
		}
	}
}
