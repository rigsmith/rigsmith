package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/cli/internal/config"
)

func TestParseLinePct(t *testing.T) {
	tests := []struct {
		name string
		json string
		want float64
		ok   bool
	}{
		{"valid", `{"total":{"lines":{"pct":83.5}}}`, 83.5, true},
		{"zero", `{"total":{"lines":{"pct":0}}}`, 0, true},
		{"hundred", `{"total":{"lines":{"pct":100}}}`, 100, true},
		{"missing pct", `{"total":{"lines":{}}}`, 0, false},
		{"missing lines", `{"total":{}}`, 0, false},
		{"missing total", `{}`, 0, false},
		{"garbage", `not json`, 0, false},
		{"empty", ``, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseLinePct([]byte(tt.json))
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Fatalf("pct = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseGoCoverage(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   float64
		ok     bool
	}{
		{
			"single",
			"ok  \tgithub.com/x/y\t0.012s\tcoverage: 76.4% of statements\n",
			76.4, true,
		},
		{
			"takes max across packages",
			"coverage: 50.0% of statements\ncoverage: 91.2% of statements\ncoverage: 0.0% of statements\n",
			91.2, true,
		},
		{"integer pct", "coverage: 100% of statements\n", 100, true},
		{"no coverage line", "ok\tgithub.com/x/y\t0.01s\n", 0, false},
		{"empty", "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseGoCoverage(tt.output)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Fatalf("pct = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---- ports of the .NET rig's CoverageTests ----

func fptr(f float64) *float64 { return &f }

func TestMeetsMinimum_GatesLineCoverage(t *testing.T) {
	tests := []struct {
		name string
		rate *float64
		min  *float64
		want bool
	}{
		{"no gate", fptr(0.75), nil, true},
		{"above", fptr(0.75), fptr(70), true},
		{"below", fptr(0.75), fptr(80), false},
		{"boundary", fptr(0.80), fptr(80), true},
		{"unreadable cannot meet", nil, fptr(80), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := meetsMinimum(tt.rate, tt.min); got != tt.want {
				t.Fatalf("meetsMinimum = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveCoverageOptions_CliFlagsWinOverConfigDefaults(t *testing.T) {
	bptr := func(b bool) *bool { return &b }
	cfg := &config.Coverage{Open: bptr(true), Full: bptr(true), Min: fptr(70)}

	// config supplies the defaults when the CLI doesn't pass the flag
	full, open, min := resolveCoverageOptions(false, false, nil, cfg)
	if !full || !open || min == nil || *min != 70 {
		t.Fatalf("got (%v, %v, %v), want (true, true, 70)", full, open, min)
	}

	// an explicit --min overrides the config default; bool flags only add
	full, open, min = resolveCoverageOptions(false, false, fptr(90), cfg)
	if !full || !open || min == nil || *min != 90 {
		t.Fatalf("got (%v, %v, %v), want (true, true, 90)", full, open, min)
	}

	// no config → CLI values pass through untouched
	full, open, min = resolveCoverageOptions(true, false, nil, nil)
	if !full || open || min != nil {
		t.Fatalf("got (%v, %v, %v), want (true, false, nil)", full, open, min)
	}
}

func TestRunner_RespectsExplicitConfig(t *testing.T) {
	root := t.TempDir() // hermetic: no global.json anywhere relevant
	if got := detectDotnetTestRunner(root, "mtp"); got != mtpRunner {
		t.Fatalf("mtp = %v, want mtpRunner", got)
	}
	if got := detectDotnetTestRunner(root, "xplat"); got != vsTestRunner {
		t.Fatalf("xplat = %v, want vsTestRunner", got)
	}
	if got := detectDotnetTestRunner(root, "vstest"); got != vsTestRunner {
		t.Fatalf("vstest = %v, want vsTestRunner", got)
	}
}

func TestRunner_AutoDetectsMtpFromGlobalJson(t *testing.T) {
	// The CLI grammar is selected SOLELY by global.json's test.runner — csproj
	// MTP props do not switch it.
	write := func(t *testing.T, dir, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "global.json"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mtp := t.TempDir()
	write(t, mtp, `{ "test": { "runner": "Microsoft.Testing.Platform" } }`)
	if got := detectDotnetTestRunner(mtp, ""); got != mtpRunner {
		t.Fatalf("mtp global.json = %v, want mtpRunner", got)
	}

	vstest := t.TempDir()
	write(t, vstest, `{ "sdk": { "version": "10.0.100" } }`)
	if got := detectDotnetTestRunner(vstest, ""); got != vsTestRunner {
		t.Fatalf("sdk-only global.json = %v, want vsTestRunner", got)
	}

	// A nested dir finds the nearest ancestor's global.json.
	nested := filepath.Join(mtp, "src", "App")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := detectDotnetTestRunner(nested, ""); got != mtpRunner {
		t.Fatalf("nested under mtp = %v, want mtpRunner", got)
	}
}

func TestCollectArgs_DifferByRunner(t *testing.T) {
	// MTP: --project + coverage requested after the `--` boundary.
	mtp := buildCollectArgs(mtpRunner, "/r/T/T.csproj", "/r/res", "", "")
	wantConsecutive(t, mtp, "test", "--project", "/r/T/T.csproj")
	wantConsecutive(t, mtp, "--coverage", "--coverage-output-format", "cobertura")

	// VSTest: positional project (no --project) + the XPlat collector.
	vstest := buildCollectArgs(vsTestRunner, "/r/T/T.csproj", "/r/res", "cov.runsettings", "")
	wantConsecutive(t, vstest, "test", "/r/T/T.csproj")
	if indexOfArg(vstest, "--project") >= 0 {
		t.Fatalf("vstest args %v must not contain --project", vstest)
	}
	if indexOfArg(vstest, `--collect:"XPlat Code Coverage"`) < 0 {
		t.Fatalf("vstest args %v must request the XPlat collector", vstest)
	}
	wantConsecutive(t, vstest, "--settings", "cov.runsettings")
}

func TestCollectArgs_IncludeFilterWhenScoped(t *testing.T) {
	args := buildCollectArgs(mtpRunner, "/r/T/T.csproj", "/r/res", "", "FullyQualifiedName~Foo")
	wantConsecutive(t, args, "--filter", "FullyQualifiedName~Foo")
}

func TestReadRates_ParsesLineAndBranchFromTheCoberturaRoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.cobertura.xml")
	xmlSrc := `<?xml version="1.0"?><coverage line-rate="0.42" branch-rate="0.25" version="1.9" timestamp="0"><packages/></coverage>`
	if err := os.WriteFile(path, []byte(xmlSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	line, branch := readCoberturaRates(path)
	if line == nil || *line != 0.42 {
		t.Fatalf("line = %v, want 0.42", line)
	}
	if branch == nil || *branch != 0.25 {
		t.Fatalf("branch = %v, want 0.25", branch)
	}

	if l, b := readCoberturaRates(filepath.Join(t.TempDir(), "missing.xml")); l != nil || b != nil {
		t.Fatalf("missing file: got (%v, %v), want (nil, nil)", l, b)
	}
}

func TestRendersHtmlFromCoberturaInProcess(t *testing.T) {
	dir := t.TempDir()
	cobertura := filepath.Join(dir, "coverage.cobertura.xml")
	xmlSrc := `<?xml version="1.0"?>
<coverage line-rate="0.5" branch-rate="0" version="1.9" timestamp="0"
          lines-covered="1" lines-valid="2" branches-covered="0" branches-valid="0">
  <sources><source>/src</source></sources>
  <packages>
    <package name="P" line-rate="0.5" branch-rate="0" complexity="0">
      <classes>
        <class name="C" filename="C.cs" line-rate="0.5" branch-rate="0" complexity="0">
          <methods/>
          <lines>
            <line number="1" hits="1"/>
            <line number="2" hits="0"/>
          </lines>
        </class>
      </classes>
    </package>
  </packages>
</coverage>`
	if err := os.WriteFile(cobertura, []byte(xmlSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "report")

	index, err := renderCoberturaHTML(cobertura, outDir)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	data, err := os.ReadFile(index)
	if err != nil {
		t.Fatalf("the rendered index should exist: %v", err)
	}
	if !strings.Contains(string(data), "<html") {
		t.Fatal("the rendered report should be HTML")
	}
	if !strings.Contains(string(data), "C.cs") {
		t.Fatal("the rendered report should list the class's file")
	}
}
