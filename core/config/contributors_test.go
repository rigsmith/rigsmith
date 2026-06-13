package config

import "testing"

func TestContributorsParsing(t *testing.T) {
	cfg, err := Parse([]byte(`{
		"contributors": {
			"enabled": true,
			"exclude": ["octocat", "me@example.com"],
			"section": "👏 Thanks To"
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Contributors.Enabled {
		t.Error("Enabled should be true")
	}
	if cfg.Contributors.SectionHeading() != "👏 Thanks To" {
		t.Errorf("SectionHeading = %q", cfg.Contributors.SectionHeading())
	}
	// excludeBots defaults to true when unset.
	if !cfg.Contributors.ExcludesBots() {
		t.Error("ExcludesBots should default true")
	}
}

func TestContributorsDefaultSection(t *testing.T) {
	c := Contributors{}
	if c.SectionHeading() != DefaultContributorsSection {
		t.Errorf("default SectionHeading = %q, want %q", c.SectionHeading(), DefaultContributorsSection)
	}
}

func TestIsContributorExcluded(t *testing.T) {
	yes := true
	no := false

	t.Run("bots excluded by default", func(t *testing.T) {
		c := Contributors{}
		if !c.IsContributorExcluded("renovate[bot]", "", "") {
			t.Error("renovate[bot] should be excluded by default (login)")
		}
		if !c.IsContributorExcluded("", "dependabot[bot]", "") {
			t.Error("dependabot[bot] should be excluded by default (name)")
		}
		if c.IsContributorExcluded("pi0", "Pooya Parsa", "pooya@pi0.io") {
			t.Error("a human should not be excluded with no rules")
		}
	})

	t.Run("excludeBots off keeps bots", func(t *testing.T) {
		c := Contributors{ExcludeBots: &no}
		if c.IsContributorExcluded("renovate[bot]", "", "") {
			t.Error("with excludeBots=false, bots are kept")
		}
	})

	t.Run("exclude matches login, name, or email with globs", func(t *testing.T) {
		c := Contributors{ExcludeBots: &yes, Exclude: []string{"octocat", "ci@*", "John *"}}
		if !c.IsContributorExcluded("octocat", "The Octocat", "x@y.z") {
			t.Error("login octocat should match")
		}
		if !c.IsContributorExcluded("someone", "Someone", "ci@example.com") {
			t.Error("email ci@example.com should match ci@*")
		}
		if !c.IsContributorExcluded("jc", "John Campion", "j@c.com") {
			t.Error("name should match 'John *'")
		}
		if c.IsContributorExcluded("jane", "Jane Doe", "jane@x.io") {
			t.Error("unrelated author should not match")
		}
	})
}
