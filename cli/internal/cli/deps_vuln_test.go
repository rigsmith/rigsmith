package cli

import (
	"reflect"
	"testing"
)

func TestSeverityRankAndMax(t *testing.T) {
	if severityRank("Critical") <= severityRank("high") {
		t.Errorf("critical should outrank high")
	}
	if severityRank("moderate") != severityRank("medium") {
		t.Errorf("moderate and medium should rank equal")
	}
	if severityRank("bogus") != -1 {
		t.Errorf("unknown severity should rank -1")
	}
	if got := maxSeverity("low", "High"); got != "High" {
		t.Errorf("maxSeverity(low, High) = %q, want High", got)
	}
	if got := maxSeverity("Critical", "moderate"); got != "Critical" {
		t.Errorf("maxSeverity(Critical, moderate) = %q, want Critical", got)
	}
	if got := maxSeverity("", "low"); got != "low" {
		t.Errorf("maxSeverity(\"\", low) = %q, want low", got)
	}
}

func TestParseDotnetVulnerable(t *testing.T) {
	// Two advisories on one package → the highest severity wins; keyed per project.
	js := `{"projects":[{"path":"/p/App.csproj","frameworks":[{"topLevelPackages":[
	  {"id":"Newtonsoft.Json","vulnerabilities":[
	    {"severity":"Moderate","advisoryurl":"u1"},
	    {"severity":"High","advisoryurl":"u2"}]},
	  {"id":"Safe","vulnerabilities":[]}]}]}]}`
	got := parseDotnetVulnerable(js)
	want := map[string]string{"/p/App.csproj\x00Newtonsoft.Json": "High"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestParseNpmAudit(t *testing.T) {
	js := `{"auditReportVersion":2,"vulnerabilities":{
	  "lodash":{"name":"lodash","severity":"high","isDirect":true},
	  "minimist":{"name":"minimist","severity":"low","isDirect":false}}}`
	got := parseNpmAudit(js)
	want := map[string]string{"lodash": "high", "minimist": "low"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestParsePnpmAudit(t *testing.T) {
	// npm-v6 advisories shape, keyed by advisory id; two advisories on lodash.
	js := `{"advisories":{
	  "1":{"module_name":"lodash","severity":"moderate"},
	  "2":{"module_name":"lodash","severity":"critical"},
	  "3":{"module_name":"minimist","severity":"low"}},"metadata":{}}`
	got := parsePnpmAudit(js)
	want := map[string]string{"lodash": "critical", "minimist": "low"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestParseBunAudit(t *testing.T) {
	// bun: keyed by package name → array of advisories.
	js := `{"lodash":[{"severity":"high"},{"severity":"moderate"}],"is-odd":[{"severity":"low"}]}`
	got := parseBunAudit(js)
	want := map[string]string{"lodash": "high", "is-odd": "low"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestParseYarnClassicAudit(t *testing.T) {
	// yarn v1 NDJSON: auditAdvisory rows carry data.advisory.{module_name,severity}.
	text := `{"type":"auditAdvisory","data":{"advisory":{"module_name":"lodash","severity":"high"}}}
{"type":"auditSummary","data":{"vulnerabilities":{"high":1}}}`
	got := parseYarnClassicAudit(text)
	want := map[string]string{"lodash": "high"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestApplyVulnerabilities(t *testing.T) {
	rows := []outdatedDep{
		{name: "lodash", current: "4.17.20"},
		{name: "safe", current: "1.0.0"},
	}
	sev := map[string]string{"lodash": "high"}
	n := applyVulnerabilities(rows, sev, false)
	if n != 1 {
		t.Fatalf("applied %d, want 1", n)
	}
	if rows[0].vuln != "high" || rows[1].vuln != "" {
		t.Fatalf("vuln overlay wrong: %+v", rows)
	}
}

func TestApplyVulnerabilitiesByProject(t *testing.T) {
	// Same name in two projects; only the keyed one is annotated.
	rows := []outdatedDep{
		{name: "Newtonsoft.Json", current: "9.0.1", project: "/a.csproj"},
		{name: "Newtonsoft.Json", current: "13.0.4", project: "/b.csproj"},
	}
	sev := map[string]string{"/a.csproj\x00Newtonsoft.Json": "High"}
	n := applyVulnerabilities(rows, sev, true)
	if n != 1 || rows[0].vuln != "High" || rows[1].vuln != "" {
		t.Fatalf("by-project overlay wrong (n=%d): %+v", n, rows)
	}
}
