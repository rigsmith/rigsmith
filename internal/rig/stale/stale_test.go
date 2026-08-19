package stale

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// touch writes root/rel (slash-separated, parents created) and stamps its
// mtime, so a test can build an artifact tree whose ages are exactly known.
func touch(t *testing.T, root, rel string, age time.Duration) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	return path
}

// ---- glob translation ----

func TestGlobMatching(t *testing.T) {
	cases := []struct {
		glob, path string
		want       bool
	}{
		// ** spans directories, and matches zero of them.
		{"**/*.cc", "foo.cc", true},
		{"**/*.cc", "src/a/foo.cc", true},
		{"**/*.cc", "src/a/foo.h", false},
		// * stays inside a segment.
		{"src/*.go", "src/main.go", true},
		{"src/*.go", "src/a/main.go", false},
		{"?.txt", "a.txt", true},
		{"?.txt", "ab.txt", false},
		// A bare name is anchored: it doesn't match at depth without **.
		{"go.mod", "go.mod", true},
		{"go.mod", "sub/go.mod", false},
		{"**/go.mod", "sub/go.mod", true},
		// Case-insensitive, and a leading ./ is tolerated.
		{"**/*.CS", "src/App.cs", true},
		{"./src/*.rs", "src/lib.rs", true},
		// Regex metacharacters in the pattern are literal.
		{"a+b.txt", "a+b.txt", true},
		{"a+b.txt", "aab.txt", false},
	}
	for _, c := range cases {
		if got := newMatcher([]string{c.glob}).match(c.path); got != c.want {
			t.Errorf("match(%q, %q) = %v, want %v", c.glob, c.path, got, c.want)
		}
	}
}

// ---- declared artifacts ----

// The case that prompted the feature: a binary older than the sources it was
// supposedly built from, with every verb still reporting success.
func TestCheckArtifact_StaleBinaryNamesTheInput(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "out/unit_tests", 2*time.Hour)
	touch(t, root, "src/renderer.cc", time.Minute)

	f := CheckArtifact(root, Artifact{Name: "unit-tests", Path: "out/unit_tests", Inputs: []string{"**/*.cc"}})
	if f.Status != Stale {
		t.Fatalf("status = %v, want Stale (%+v)", f.Status, f)
	}
	if f.Newest != "src/renderer.cc" {
		t.Errorf("newest input = %q, want src/renderer.cc", f.Newest)
	}
	if f.Oldest != "out/unit_tests" {
		t.Errorf("oldest artifact file = %q, want out/unit_tests", f.Oldest)
	}
	if d := f.Detail(); !strings.Contains(d, "src/renderer.cc") || !strings.Contains(d, "out/unit_tests") {
		t.Errorf("detail = %q, want both paths named", d)
	}
}

func TestCheckArtifact_FreshBinaryIsOK(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "src/renderer.cc", 2*time.Hour)
	touch(t, root, "out/unit_tests", time.Minute)

	f := CheckArtifact(root, Artifact{Name: "unit-tests", Path: "out/unit_tests", Inputs: []string{"**/*.cc"}})
	if f.Status != OK {
		t.Fatalf("status = %v, want OK (%+v)", f.Status, f)
	}
}

// A bundle is stale when ANY file inside it is older than the newest input —
// the .pak case: a fresh library beside a resource the build never refreshed.
// Taking the newest file in the directory would call this bundle fine.
func TestCheckArtifact_DirectoryUsesItsOldestFile(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "out/App.app/Contents/MacOS/App", time.Minute)        // relinked
	touch(t, root, "out/App.app/Contents/Resources/en.pak", 3*time.Hour) // not
	touch(t, root, "out/App.app/Contents/Resources/fr.pak", 3*time.Hour)
	touch(t, root, "src/strings.grd", time.Hour)

	f := CheckArtifact(root, Artifact{Name: "browser", Path: "out/App.app", Inputs: []string{"**/*.grd"}})
	if f.Status != Stale {
		t.Fatalf("status = %v, want Stale (%+v)", f.Status, f)
	}
	if !strings.HasSuffix(f.Oldest, ".pak") {
		t.Errorf("oldest = %q, want one of the .pak files", f.Oldest)
	}
	if f.AlsoOld != 1 {
		t.Errorf("AlsoOld = %d, want 1 (the second .pak)", f.AlsoOld)
	}
	if d := f.Detail(); !strings.Contains(d, "1 more file") {
		t.Errorf("detail = %q, want the extra out-of-date file counted", d)
	}
}

// Every reason a check can't run is reported as skipped — never as a pass.
func TestCheckArtifact_SkipReasons(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "src/a.cc", time.Minute)
	touch(t, root, "out/bin", time.Minute)

	cases := []struct {
		name string
		art  Artifact
		want string
	}{
		{"missing path", Artifact{Name: "a", Path: "out/nope", Inputs: []string{"**/*.cc"}}, "does not exist"},
		{"no path", Artifact{Name: "b", Inputs: []string{"**/*.cc"}}, `no "path"`},
		{"no inputs", Artifact{Name: "c", Path: "out/bin"}, `no "inputs"`},
		{"inputs match nothing", Artifact{Name: "d", Path: "out/bin", Inputs: []string{"**/*.zig"}}, "no files match"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := CheckArtifact(root, c.art)
			if f.Status != Skipped {
				t.Fatalf("status = %v, want Skipped (%+v)", f.Status, f)
			}
			if !strings.Contains(f.Reason, c.want) {
				t.Errorf("reason = %q, want it to mention %q", f.Reason, c.want)
			}
		})
	}
}

// An artifact outside the repo (the out/ tree beside a Chromium checkout) is
// resolved relative to the root and displayed as written, not as ../../..
func TestCheckArtifact_PathOutsideTheRepo(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "src")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	touch(t, parent, "out/Release/unit_tests", 2*time.Hour)
	touch(t, root, "a.cc", time.Minute)

	f := CheckArtifact(root, Artifact{Name: "t", Path: "../out/Release/unit_tests", Inputs: []string{"**/*.cc"}})
	if f.Status != Stale {
		t.Fatalf("status = %v, want Stale (%+v)", f.Status, f)
	}
	if !strings.HasPrefix(f.Target, "../out/") {
		t.Errorf("target = %q, want it displayed as ../out/…", f.Target)
	}
}

// ---- the generic build-output check ----

func TestCheckOutput_SourceNewerThanOutput(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "bin/app", time.Hour)
	touch(t, root, "main.go", time.Minute)

	f := CheckOutput(root, "go")
	if f.Status != Stale {
		t.Fatalf("status = %v, want Stale (%+v)", f.Status, f)
	}
	if f.Newest != "main.go" {
		t.Errorf("newest = %q, want main.go", f.Newest)
	}
}

func TestCheckOutput_OutputNewerThanSource(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "main.go", time.Hour)
	touch(t, root, "bin/app", time.Minute)

	if f := CheckOutput(root, "go"); f.Status != OK {
		t.Fatalf("status = %v, want OK (%+v)", f.Status, f)
	}
}

// Docs and fixtures are not build inputs: editing a README must not read as
// "you edited and didn't rebuild", or the check gets ignored.
func TestCheckOutput_NonSourceEditsDoNotCount(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "main.go", time.Hour)
	touch(t, root, "bin/app", 30*time.Minute)
	touch(t, root, "README.md", time.Minute)

	if f := CheckOutput(root, "go"); f.Status != OK {
		t.Fatalf("status = %v, want OK — a README edit is not a rebuild trigger (%+v)", f.Status, f)
	}
}

func TestCheckOutput_NothingBuiltIsSkipped(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "main.go", time.Minute)

	f := CheckOutput(root, "go")
	if f.Status != Skipped {
		t.Fatalf("status = %v, want Skipped (%+v)", f.Status, f)
	}
	if !strings.Contains(f.Reason, "nothing built yet") {
		t.Errorf("reason = %q, want it to say nothing was built", f.Reason)
	}
}

func TestCheckOutput_UnknownEcosystemIsSkipped(t *testing.T) {
	f := CheckOutput(t.TempDir(), "brainfuck")
	if f.Status != Skipped {
		t.Fatalf("status = %v, want Skipped (%+v)", f.Status, f)
	}
}

// .NET output lives in per-project bin/ trees, so there is no single directory
// to stat — they're discovered.
func TestCheckOutput_DotnetFindsPerProjectBin(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "src/App/bin/Debug/net8.0/App.dll", time.Hour)
	touch(t, root, "src/App/Program.cs", time.Minute)

	f := CheckOutput(root, "dotnet")
	if f.Status != Stale {
		t.Fatalf("status = %v, want Stale (%+v)", f.Status, f)
	}
	if !strings.Contains(f.Target, "bin") {
		t.Errorf("target = %q, want the discovered bin directory", f.Target)
	}
}

// ---- node_modules vs the lockfile ----

func TestCheckDeps_LockfileNewerThanInstall(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "node_modules/.package-lock.json", time.Hour)
	touch(t, root, "package-lock.json", time.Minute)

	f, ok := CheckDeps(root, "node")
	if !ok {
		t.Fatal("CheckDeps did not apply to a node repo with a lockfile")
	}
	if f.Status != Stale {
		t.Fatalf("status = %v, want Stale (%+v)", f.Status, f)
	}
}

func TestCheckDeps_InstalledAfterTheLockfile(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "package-lock.json", time.Hour)
	touch(t, root, "node_modules/.package-lock.json", time.Minute)

	f, _ := CheckDeps(root, "node")
	if f.Status != OK {
		t.Fatalf("status = %v, want OK (%+v)", f.Status, f)
	}
}

func TestCheckDeps_MissingNodeModulesIsSkipped(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "package-lock.json", time.Minute)

	f, _ := CheckDeps(root, "node")
	if f.Status != Skipped || !strings.Contains(f.Reason, "node_modules is missing") {
		t.Fatalf("f = %+v, want a skipped check naming the missing node_modules", f)
	}
}

func TestCheckDeps_OnlyAppliesToNode(t *testing.T) {
	if _, ok := CheckDeps(t.TempDir(), "go"); ok {
		t.Fatal("CheckDeps should not apply to Go — it has no installed dependency tree to go stale")
	}
}

// ---- composition ----

// A repo with no artifacts block is not an error: it gets the generic checks,
// and every check that could not run says so.
func TestCheck_NoConfigStillReports(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "main.go", time.Minute)

	findings := Check(root, "go", nil)
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1 (the generic check)", len(findings))
	}
	if findings[0].Status != Skipped {
		t.Errorf("finding = %+v, want the unbuilt repo reported as skipped", findings[0])
	}
	if AnyStale(findings) {
		t.Error("a skipped check must not count as stale")
	}
}

// The report is stable run to run: declared artifacts come back sorted.
func TestCheckArtifacts_SortedByName(t *testing.T) {
	root := t.TempDir()
	findings := CheckArtifacts(root, []Artifact{{Name: "zebra"}, {Name: "alpha"}, {Name: "middle"}})
	got := []string{findings[0].Name, findings[1].Name, findings[2].Name}
	want := []string{"alpha", "middle", "zebra"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestAnyStale(t *testing.T) {
	if AnyStale([]Finding{{Status: OK}, {Status: Skipped}}) {
		t.Error("AnyStale = true with nothing stale")
	}
	if !AnyStale([]Finding{{Status: OK}, {Status: Stale}}) {
		t.Error("AnyStale = false with a stale finding")
	}
}

func TestRoughly(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{90 * time.Second, "1m"},
		{2 * time.Hour, "2h"},
		{50 * time.Hour, "2d"},
	}
	for _, c := range cases {
		if got := roughly(c.d); got != c.want {
			t.Errorf("roughly(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}

// ---- partial reads and self-comparison (the "never pass on incomplete
// evidence" rule) ----

// lockDir makes a directory unreadable for the rest of the test, or skips when
// the platform/user makes that impossible.
func lockDir(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions don't gate traversal the same way on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads unreadable directories anyway")
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
}

// A bundle with an unreadable subdirectory must not come back OK: that
// directory can hold exactly the stale resource the check exists to find, so
// "up to date" would be a claim about files never seen.
func TestCheckArtifact_UnreadableSubtreeCannotPass(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "src/a.cc", 2*time.Hour)
	touch(t, root, "out/App.app/Contents/MacOS/App", time.Minute)
	hidden := filepath.Join(root, "out/App.app/Contents/Resources")
	touch(t, root, "out/App.app/Contents/Resources/en.pak", time.Minute)
	lockDir(t, hidden)

	f := CheckArtifact(root, Artifact{Name: "browser", Path: "out/App.app", Inputs: []string{"**/*.cc"}})
	if f.Status != Skipped {
		t.Fatalf("status = %v, want Skipped — a partial read must not pass (%+v)", f.Status, f)
	}
	if !strings.Contains(f.Reason, "cannot vouch") {
		t.Errorf("reason = %q, want it to say the comparison can't be vouched for", f.Reason)
	}
}

// Evidence of staleness stands even over a partial read: a file that IS older
// than its input is older whatever else was hidden. The report says what it
// couldn't read so the count isn't mistaken for the whole story.
func TestCheckArtifact_StaleVerdictSurvivesAPartialRead(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "src/a.cc", time.Minute)
	touch(t, root, "out/App.app/old.pak", 3*time.Hour)
	hidden := filepath.Join(root, "out/App.app/Locked")
	if err := os.MkdirAll(hidden, 0o755); err != nil {
		t.Fatal(err)
	}
	lockDir(t, hidden)

	f := CheckArtifact(root, Artifact{Name: "browser", Path: "out/App.app", Inputs: []string{"**/*.cc"}})
	if f.Status != Stale {
		t.Fatalf("status = %v, want Stale — the proof is still proof (%+v)", f.Status, f)
	}
	if f.Unreadable == "" || !strings.Contains(f.Detail(), "could not read") {
		t.Errorf("detail = %q, want the unreadable directory disclosed", f.Detail())
	}
}

// An artifact is never its own input. A bundle full of generated files would
// otherwise be compared against itself and report stale forever.
func TestCheckArtifact_OwnTreeIsNotAnInput(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "src/app.js", 2*time.Hour)
	touch(t, root, "out/bundle/vendor.js", time.Hour) // oldest file in the artifact
	touch(t, root, "out/bundle/app.js", time.Minute)  // newest .js in the repo

	f := CheckArtifact(root, Artifact{Name: "bundle", Path: "out/bundle", Inputs: []string{"**/*.js"}})
	if f.Status != OK {
		t.Fatalf("status = %v, want OK — the bundle's own files are not its inputs (%+v)", f.Status, f)
	}
}

// walkutil prunes `dist` and `.next` by default but not `build`, `out`,
// `.output` or `.svelte-kit`. Without pruning them explicitly, a bundled .js
// counts as a source newer than the output it came from — a permanent false
// stale for any Node repo that builds to one of those.
func TestCheckOutput_NodeOutputIsNotSource(t *testing.T) {
	for _, dir := range []string{"build", "out", ".output", ".svelte-kit", "dist"} {
		t.Run(dir, func(t *testing.T) {
			root := t.TempDir()
			touch(t, root, "package.json", 3*time.Hour)
			touch(t, root, "src/index.ts", 2*time.Hour)
			touch(t, root, dir+"/assets/index.js", time.Minute) // generated, newest file

			f := CheckOutput(root, "node")
			if f.Status != OK {
				t.Fatalf("status = %v, want OK — %s/ is output, not source (%+v)", f.Status, dir, f)
			}
		})
	}
}

// A real source edit after the build is still caught with the pruning in place.
func TestCheckOutput_NodeSourceEditStillCaught(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "package.json", 3*time.Hour)
	touch(t, root, "build/assets/index.js", time.Hour)
	touch(t, root, "src/index.ts", time.Minute) // edited after the build

	f := CheckOutput(root, "node")
	if f.Status != Stale {
		t.Fatalf("status = %v, want Stale (%+v)", f.Status, f)
	}
	if f.Newest != "src/index.ts" {
		t.Errorf("newest = %q, want src/index.ts", f.Newest)
	}
}

// An unreadable source subtree could hide the newest file, so a clean generic
// verdict can't be drawn over it either.
func TestCheckOutput_UnreadableSourceSubtreeCannotPass(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "main.go", 2*time.Hour)
	touch(t, root, "bin/app", time.Minute)
	hidden := filepath.Join(root, "internal")
	if err := os.MkdirAll(hidden, 0o755); err != nil {
		t.Fatal(err)
	}
	lockDir(t, hidden)

	f := CheckOutput(root, "go")
	if f.Status != Skipped {
		t.Fatalf("status = %v, want Skipped over a partial source read (%+v)", f.Status, f)
	}
}
