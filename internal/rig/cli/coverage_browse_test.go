package cli

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoberturaDetail_MergesLinesAndResolvesSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "C.cs")
	if err := os.WriteFile(src, []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := coberturaDoc{Sources: []string{dir}}
	xmlSrc := `<coverage>
      <packages><package name="P"><classes>
        <class name="A" filename="C.cs"><lines>
          <line number="1" hits="0"/><line number="2" hits="3"/>
        </lines></class>
        <class name="A2" filename="C.cs"><lines>
          <line number="2" hits="0"/><line number="3" hits="1"/>
        </lines></class>
      </classes></package></packages>
    </coverage>`
	if err := xml.Unmarshal([]byte(xmlSrc), &doc); err != nil {
		t.Fatal(err)
	}

	got := coberturaDetail(doc)
	if len(got) != 1 {
		t.Fatalf("want 1 file, got %d", len(got))
	}
	f := got[0]
	// line 2 appears in both classes (hits 3 and 0) → max hits wins (3).
	if f.lines[1] != 0 || f.lines[2] != 3 || f.lines[3] != 1 {
		t.Fatalf("merged lines = %v, want {1:0,2:3,3:1}", f.lines)
	}
	if f.covered() != 2 || f.total() != 3 {
		t.Fatalf("covered/total = %d/%d, want 2/3", f.covered(), f.total())
	}
	if f.path != src {
		t.Fatalf("path = %q, want %q", f.path, src)
	}
}

func TestParseLcov(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "src", "a.ts")
	if err := os.MkdirAll(filepath.Dir(a), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a, []byte("x\ny\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lcov := strings.Join([]string{
		"TN:",
		"SF:" + a,
		"DA:1,5",
		"DA:2,0",
		"DA:2,4", // repeat → higher count wins
		"end_of_record",
		"SF:" + filepath.Join(dir, "src", "gone.ts"), // unresolved source
		"DA:1,0",
		"end_of_record",
	}, "\n")

	got := parseLcov([]byte(lcov), dir)
	if len(got) != 2 {
		t.Fatalf("want 2 files, got %d", len(got))
	}
	if got[0].name != "src/a.ts" {
		t.Fatalf("name = %q, want src/a.ts", got[0].name)
	}
	if got[0].lines[1] != 5 || got[0].lines[2] != 4 {
		t.Fatalf("lines = %v, want {1:5,2:4}", got[0].lines)
	}
	if got[0].path != a {
		t.Fatalf("path = %q, want %q", got[0].path, a)
	}
	if got[1].path != "" {
		t.Fatalf("missing source should resolve to empty path, got %q", got[1].path)
	}
}

func TestParseLcov_ToleratesMissingFinalEndOfRecord(t *testing.T) {
	got := parseLcov([]byte("SF:/x/y.js\nDA:1,1\n"), "/x")
	if len(got) != 1 || got[0].lines[1] != 1 {
		t.Fatalf("got %+v, want one file with line 1 covered", got)
	}
}

func TestGoResolveSource(t *testing.T) {
	root := t.TempDir()
	// Two modules: root (mod "a") and a nested one (mod "a/sub" → dir sub).
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(filepath.Join(sub, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	rootFile := filepath.Join(root, "main.go")
	subFile := filepath.Join(sub, "pkg", "f.go")
	for _, p := range []string{rootFile, subFile} {
		if err := os.WriteFile(p, []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mods := map[string]string{"a": root, "a/sub": sub}

	// Longest matching module path wins: "a/sub/pkg/f.go" → the sub module.
	if got := goResolveSource("a/sub/pkg/f.go", mods); got != subFile {
		t.Fatalf("sub file = %q, want %q", got, subFile)
	}
	if got := goResolveSource("a/main.go", mods); got != rootFile {
		t.Fatalf("root file = %q, want %q", got, rootFile)
	}
	// A path under no known module, or not on disk, resolves to "".
	if got := goResolveSource("other/x.go", mods); got != "" {
		t.Fatalf("unknown module = %q, want empty", got)
	}
	if got := goResolveSource("a/missing.go", mods); got != "" {
		t.Fatalf("missing file = %q, want empty", got)
	}
}

func TestRenderSourceDetail_AnnotatesAndFallsBack(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "f.go")
	if err := os.WriteFile(src, []byte("line1\nline2\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := covFile{name: "f.go", path: src, lines: map[int]int{1: 2, 2: 0}}
	out := renderSourceDetail(f)
	for _, want := range []string{"line1", "line2", "line3", "✗"} {
		if !strings.Contains(out, want) {
			t.Fatalf("detail missing %q:\n%s", want, out)
		}
	}

	// No resolvable source → the ledger fallback.
	led := renderSourceDetail(covFile{name: "x", path: "", lines: map[int]int{3: 0}})
	if !strings.Contains(led, "source not found") || !strings.Contains(led, "not covered") {
		t.Fatalf("ledger fallback unexpected:\n%s", led)
	}
}

func TestNewCovBrowser_SortsWorstFirst(t *testing.T) {
	m := newCovBrowser([]covFile{
		{name: "good", lines: map[int]int{1: 1, 2: 1}}, // 100%
		{name: "bad", lines: map[int]int{1: 0, 2: 0}},  // 0%
		{name: "mid", lines: map[int]int{1: 1, 2: 0}},  // 50%
	})
	if m.files[0].name != "bad" || m.files[2].name != "good" {
		t.Fatalf("order = %s,%s,%s, want bad,mid,good",
			m.files[0].name, m.files[1].name, m.files[2].name)
	}
}
