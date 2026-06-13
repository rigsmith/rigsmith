package config

import "testing"

func TestVersioningSourceDefaults(t *testing.T) {
	// Absent versioning block → changeset mode.
	cfg := Default()
	if cfg.CommitSource() != SourceChangesets {
		t.Errorf("default CommitSource = %q, want changesets", cfg.CommitSource())
	}
	if cfg.UsesCommits() {
		t.Error("default must not use commits")
	}
	if !cfg.UsesChangesets() {
		t.Error("default must use changesets")
	}
}

func TestVersioningSourceParsing(t *testing.T) {
	cases := []struct {
		json           string
		want           VersioningSource
		commits, files bool
	}{
		{`{}`, SourceChangesets, false, true},
		{`{"versioning":{"source":"changesets"}}`, SourceChangesets, false, true},
		{`{"versioning":{"source":"commits"}}`, SourceCommits, true, false},
		{`{"versioning":{"source":"both"}}`, SourceBoth, true, true},
		{`{"versioning":{"source":"bogus"}}`, SourceChangesets, false, true}, // unknown → safe default
	}
	for _, c := range cases {
		cfg, err := Parse([]byte(c.json))
		if err != nil {
			t.Fatalf("Parse(%s): %v", c.json, err)
		}
		if got := cfg.CommitSource(); got != c.want {
			t.Errorf("%s: CommitSource = %q, want %q", c.json, got, c.want)
		}
		if cfg.UsesCommits() != c.commits {
			t.Errorf("%s: UsesCommits = %v, want %v", c.json, cfg.UsesCommits(), c.commits)
		}
		if cfg.UsesChangesets() != c.files {
			t.Errorf("%s: UsesChangesets = %v, want %v", c.json, cfg.UsesChangesets(), c.files)
		}
	}
}

func TestVersioningScopesParsed(t *testing.T) {
	cfg, err := Parse([]byte(`{"versioning":{"source":"commits","scopes":{"core":"github.com/x/core"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Versioning.Scopes["core"] != "github.com/x/core" {
		t.Errorf("scopes = %v", cfg.Versioning.Scopes)
	}
}
