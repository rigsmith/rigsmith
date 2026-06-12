package cli

import (
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/cli/internal/config"
	"github.com/spf13/cobra"
)

// ---- persisted "remember" choice ---------------------------------------

func TestPersistRGMode_RoundTrips(t *testing.T) {
	root := t.TempDir()
	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	persistRGMode(cmd, root, "download")

	cfg, _ := config.LoadMerged(root)
	if cfg.Coverage == nil || cfg.Coverage.ReportGenerator != "download" {
		t.Fatalf("expected coverage.reportGenerator=download, got %+v", cfg.Coverage)
	}
}

// ---- rgMode / buildReportGeneratorArgs ---------------------------------

func TestRGMode(t *testing.T) {
	cases := map[string]string{"": "auto", "auto": "auto", "AUTO": "auto", "off": "off", "Off": "off", "download": "download", "weird": "auto"}
	for in, want := range cases {
		if got := rgMode(&config.Coverage{ReportGenerator: in}); got != want {
			t.Errorf("rgMode(%q) = %q, want %q", in, got, want)
		}
	}
	if got := rgMode(nil); got != "auto" {
		t.Errorf("rgMode(nil) = %q, want auto", got)
	}
}

func TestBuildReportGeneratorArgs(t *testing.T) {
	got := buildReportGeneratorArgs([]string{"a.xml", "b.info"}, "/out", "", "")
	eqSlice(t, got, []string{"-reports:a.xml;b.info", "-targetdir:/out", "-reporttypes:Html"})

	got = buildReportGeneratorArgs([]string{"a.xml"}, "/out", "Html;Badges", "KEY-123")
	eqSlice(t, got, []string{"-reports:a.xml", "-targetdir:/out", "-reporttypes:Html;Badges", "-license:KEY-123"})
}

// ---- manifest detection ------------------------------------------------

func TestManifestHasReportGenerator(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, ".config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if manifestHasReportGenerator(root) {
		t.Fatalf("expected no RG before a manifest exists")
	}
	manifest := `{"version":1,"isRoot":true,"tools":{"dotnet-reportgenerator-globaltool":{"version":"5.3.0","commands":["reportgenerator"]}}}`
	if err := os.WriteFile(filepath.Join(cfgDir, "dotnet-tools.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if !manifestHasReportGenerator(root) {
		t.Errorf("expected RG detected from tool manifest")
	}
	// A nested dir should still find the ancestor manifest.
	nested := filepath.Join(root, "src", "proj")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if !manifestHasReportGenerator(nested) {
		t.Errorf("expected RG detected from ancestor manifest")
	}
}

// ---- go coverprofile → cobertura ---------------------------------------

func TestGoProfileToCobertura(t *testing.T) {
	profile := `mode: atomic
example.com/m/foo.go:10.2,12.16 2 3
example.com/m/foo.go:12.16,14.3 1 0
example.com/m/bar.go:5.1,5.10 1 7
`
	doc, err := goProfileToCobertura(profile)
	if err != nil {
		t.Fatal(err)
	}
	// foo.go: lines 10,11,12 hit (3); 13,14 miss → 3/5 = 0.6000
	for _, want := range []string{
		`filename="example.com/m/foo.go"`,
		`line-rate="0.6000"`,
		`<line number="10" hits="3"/>`,
		`<line number="13" hits="0"/>`,
		`filename="example.com/m/bar.go"`,
		`<line number="5" hits="7"/>`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("cobertura missing %q\n%s", want, doc)
		}
	}
	// overall 4/6 covered
	if !strings.Contains(doc, `<coverage line-rate="0.6667"`) {
		t.Errorf("overall line-rate wrong:\n%s", doc)
	}
	// Must be well-formed Cobertura that round-trips through rig's own parser
	// (the same shape ReportGenerator consumes): 2 classes, foo.go has 5 lines.
	var parsed coberturaDoc
	if err := xml.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("generated cobertura is not valid XML: %v", err)
	}
	classes := 0
	for _, p := range parsed.Packages {
		classes += len(p.Classes)
		for _, c := range p.Classes {
			if c.Filename == "example.com/m/foo.go" && len(c.Lines) != 5 {
				t.Errorf("foo.go: got %d line elements, want 5", len(c.Lines))
			}
		}
	}
	if classes != 2 {
		t.Errorf("got %d classes, want 2", classes)
	}
}

func TestGoProfileToCobertura_EmptyIsValid(t *testing.T) {
	doc, err := goProfileToCobertura("mode: set\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc, `line-rate="1"`) {
		t.Errorf("empty profile should be rate 1, got:\n%s", doc)
	}
}

// ---- withGoCoverProfile ------------------------------------------------

func TestWithGoCoverProfile(t *testing.T) {
	got := withGoCoverProfile([]string{"go", "test", "-cover", "./..."}, "/tmp/c.out")
	eqSlice(t, got, []string{"go", "test", "-coverprofile=/tmp/c.out", "-covermode=atomic", "-cover", "./..."})
}

// ---- node reporter injection -------------------------------------------

func writePackageJSON(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAugmentNodeCoverageArgs_Vitest(t *testing.T) {
	root := t.TempDir()
	writePackageJSON(t, root, `{"devDependencies":{"vitest":"^2.0.0"}}`)
	base := []string{"pnpm", "run", "coverage"}

	got := augmentNodeCoverageArgs(base, root, true, false, nil)
	eqSlice(t, got, []string{"pnpm", "run", "coverage", "--", "--coverage",
		"--coverage.reporter=lcov", "--coverage.reporter=html", "--coverage.reporter=json-summary"})

	// Neither open nor min → untouched.
	eqSlice(t, augmentNodeCoverageArgs(base, root, false, false, nil), base)
}

func TestAugmentNodeCoverageArgs_NonVitestUntouched(t *testing.T) {
	root := t.TempDir()
	writePackageJSON(t, root, `{"devDependencies":{"jest":"^29.0.0"}}`)
	base := []string{"npm", "run", "coverage"}
	eqSlice(t, augmentNodeCoverageArgs(base, root, true, true, nil), base)
}

func TestNodeUsesVitest(t *testing.T) {
	root := t.TempDir()
	if nodeUsesVitest(root) {
		t.Fatal("empty dir should not be vitest")
	}
	if err := os.WriteFile(filepath.Join(root, "vitest.config.ts"), []byte("export default {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !nodeUsesVitest(root) {
		t.Error("vitest.config.ts should signal vitest")
	}
}

// ---- per-line Cobertura renderer (native .NET fallback) ----------------

func TestRenderCoberturaHTML_LineHighlighting(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "Calc.cs")
	if err := os.WriteFile(src, []byte("line one\nline two\nline three\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cobertura := `<?xml version="1.0"?>
<coverage line-rate="0.5" branch-rate="0">
  <sources><source>` + root + `</source></sources>
  <packages><package name="P">
    <classes><class name="Calc" filename="Calc.cs" line-rate="0.5">
      <lines><line number="1" hits="4"/><line number="2" hits="0"/></lines>
    </class></classes>
  </package></packages>
</coverage>`
	cobFile := filepath.Join(root, "cov.cobertura.xml")
	if err := os.WriteFile(cobFile, []byte(cobertura), 0o644); err != nil {
		t.Fatal(err)
	}
	index, err := renderCoberturaHTML(cobFile, filepath.Join(root, "report"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	h := string(out)
	for _, want := range []string{`class="hit"`, `class="miss"`, "line one", "line two", "Calc.cs"} {
		if !strings.Contains(h, want) {
			t.Errorf("rendered report missing %q", want)
		}
	}
}
