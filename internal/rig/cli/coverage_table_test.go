package cli

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestCoberturaFileCov_MergesClassesByFile(t *testing.T) {
	src := `<?xml version="1.0"?>
<coverage line-rate="0.5">
  <packages><package name="P">
    <classes>
      <class name="A" filename="a.cs"><lines>
        <line number="1" hits="1"/><line number="2" hits="0"/>
      </lines></class>
      <class name="A2" filename="a.cs"><lines>
        <line number="3" hits="1"/>
      </lines></class>
      <class name="B" filename="b.cs"><lines>
        <line number="1" hits="0"/><line number="2" hits="0"/>
      </lines></class>
    </classes>
  </package></packages>
</coverage>`
	var doc coberturaDoc
	if err := xml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatal(err)
	}
	got := coberturaFileCov(doc)
	// a.cs merges both classes: 2 of 3 covered; b.cs: 0 of 2.
	want := map[string]fileCov{
		"a.cs": {name: "a.cs", covered: 2, total: 3},
		"b.cs": {name: "b.cs", covered: 0, total: 2},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d files, want %d: %+v", len(got), len(want), got)
	}
	for _, f := range got {
		if w := want[f.name]; f != w {
			t.Fatalf("%s = %+v, want %+v", f.name, f, w)
		}
	}
}

func TestCoberturaFileCov_DedupesRepeatedLineNumberAcrossClasses(t *testing.T) {
	// Two classes share one filename and BOTH report line 2 — class A as a miss,
	// class B as a hit. The line must be counted once (max hits wins, so covered),
	// not once per <class>. Before the dedupe fix total would be 4 (line 2 double).
	// Class A: {1:miss, 2:miss}; class B: {2:hit, 3:hit}.
	src := `<?xml version="1.0"?>
<coverage>
  <packages><package name="P">
    <classes>
      <class name="A" filename="dup.cs"><lines>
        <line number="1" hits="0"/><line number="2" hits="0"/>
      </lines></class>
      <class name="B" filename="dup.cs"><lines>
        <line number="2" hits="1"/><line number="3" hits="1"/>
      </lines></class>
    </classes>
  </package></packages>
</coverage>`
	var doc coberturaDoc
	if err := xml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatal(err)
	}

	got := coberturaFileCov(doc)
	if len(got) != 1 {
		t.Fatalf("got %d files, want 1 (both classes share dup.cs): %+v", len(got), got)
	}
	// Lines 1,2,3 counted once each → total 3 (not 4). Line 2 is hit in class B
	// (max hits > 0) and line 3 is hit → covered 2.
	want := fileCov{name: "dup.cs", covered: 2, total: 3}
	if got[0] != want {
		t.Fatalf("dup.cs = %+v, want %+v", got[0], want)
	}

	// coberturaDetail dedupes by the same rule; its ledger must agree.
	detail := coberturaDetail(doc)
	if len(detail) != 1 || len(detail[0].lines) != 3 {
		t.Fatalf("coberturaDetail lines = %+v, want 3 distinct line numbers", detail)
	}
	if h := detail[0].lines[2]; h != 1 {
		t.Fatalf("coberturaDetail line 2 hits = %d, want 1 (max across classes)", h)
	}
}

func TestGoProfileFileCov(t *testing.T) {
	// Two files; flatten blocks to lines, a line is covered if any block ran.
	profile := strings.Join([]string{
		"mode: atomic",
		"github.com/me/mod/pkg/a.go:1.10,3.2 2 1", // lines 1-3 covered
		"github.com/me/mod/pkg/a.go:5.1,5.20 1 0", // line 5 not covered
		"github.com/me/mod/pkg/b.go:1.1,1.10 1 0", // line 1 not covered
		"",
	}, "\n")

	got := goProfileFileCov(profile, t.TempDir()) // no go.mod → two-segment names
	by := map[string]fileCov{}
	for _, f := range got {
		by[f.name] = f
	}
	if a := by["pkg/a.go"]; a.covered != 3 || a.total != 4 {
		t.Fatalf("a.go = %+v, want covered 3 total 4", a)
	}
	if b := by["pkg/b.go"]; b.covered != 0 || b.total != 1 {
		t.Fatalf("b.go = %+v, want covered 0 total 1", b)
	}
}

func TestNodeSummaryFileCov_SkipsTotalAndRelativizes(t *testing.T) {
	root := "/repo"
	data := []byte(`{
      "total": {"lines": {"total": 10, "covered": 8}},
      "/repo/src/a.js": {"lines": {"total": 4, "covered": 4}},
      "/repo/src/b.js": {"lines": {"total": 6, "covered": 3}}
    }`)
	got := nodeSummaryFileCov(data, root)
	if len(got) != 2 {
		t.Fatalf("got %d files (the 'total' key must be skipped): %+v", len(got), got)
	}
	by := map[string]fileCov{}
	for _, f := range got {
		by[f.name] = f
	}
	if a, ok := by["src/a.js"]; !ok || a.covered != 4 || a.total != 4 {
		t.Fatalf("a.js = %+v, want covered 4 total 4 at src/a.js", a)
	}
	if b, ok := by["src/b.js"]; !ok || b.pct() != 50 {
		t.Fatalf("b.js = %+v, want 50%%", b)
	}
}

func TestFileCovPct_NoLinesIsHundred(t *testing.T) {
	if p := (fileCov{total: 0}).pct(); p != 100 {
		t.Fatalf("empty file pct = %v, want 100", p)
	}
	if p := (fileCov{covered: 1, total: 4}).pct(); p != 25 {
		t.Fatalf("pct = %v, want 25", p)
	}
}

func TestCoverageBar(t *testing.T) {
	tests := []struct {
		pct  float64
		want string
	}{
		{0, "░░░░░░░░░░"},
		{100, "██████████"},
		{50, "█████░░░░░"},
		{45, "█████░░░░░"}, // rounds to nearest
		{-5, "░░░░░░░░░░"}, // clamped
		{150, "██████████"},
	}
	for _, tt := range tests {
		if got := coverageBar(tt.pct, 10); got != tt.want {
			t.Fatalf("bar(%v) = %q, want %q", tt.pct, got, tt.want)
		}
	}
}

func TestEllipsizeLeft(t *testing.T) {
	// Short enough → untouched; keeps the tail when trimming.
	if got := ellipsizeLeft("a/b.go", 10); got != "a/b.go" {
		t.Fatalf("short = %q", got)
	}
	if got := ellipsizeLeft("internal/cli/coverage.go", 12); got != "…coverage.go" {
		t.Fatalf("long = %q, want tail-preserving", got)
	}
}

func TestRenderCoverageTable_SortsWorstFirstWithTotal(t *testing.T) {
	files := []fileCov{
		{name: "good.go", covered: 9, total: 10}, // 90%
		{name: "bad.go", covered: 1, total: 10},  // 10%
		{name: "mid.go", covered: 5, total: 10},  // 50%
	}
	out := renderCoverageTable(files)
	// Worst coverage listed first.
	if strings.Index(out, "bad.go") > strings.Index(out, "mid.go") ||
		strings.Index(out, "mid.go") > strings.Index(out, "good.go") {
		t.Fatalf("rows not worst-first:\n%s", out)
	}
	// Overall TOTAL is 15/30 = 50.0%, and the file count is shown.
	if !strings.Contains(out, "TOTAL") || !strings.Contains(out, "(3 files)") {
		t.Fatalf("missing TOTAL/file count:\n%s", out)
	}
	if !strings.Contains(out, " 50.0%") {
		t.Fatalf("missing overall 50.0%%:\n%s", out)
	}
}

func TestRenderCoverageTable_CollapsesLongLists(t *testing.T) {
	var files []fileCov
	for i := 0; i < coverageSummaryRows+5; i++ {
		files = append(files, fileCov{name: "f", covered: i, total: 100})
	}
	out := renderCoverageTable(files)
	if !strings.Contains(out, "… 5 more file(s)") {
		t.Fatalf("expected a collapse note for the extra rows:\n%s", out)
	}
}
