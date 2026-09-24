package gitutil

import "testing"

func TestLatestTag(t *testing.T) {
	tags := []string{"lib@1.0.0", "lib@1.10.0", "lib@1.2.0", "lib@2.0.0-rc.1", "lib@next", "@acme/lib@9.0.0", "libx@5.0.0", "lib@", "lib@3.0"}
	if got, ok := LatestTag(tags, []string{"lib@", ""}); !ok || got != "lib@2.0.0-rc.1" {
		t.Errorf("LatestTag = %q, %v; want lib@2.0.0-rc.1 (lib@3.0 isn't a full version)", got, ok)
	}
	if got, ok := LatestTag([]string{"v1.0.0-app", "v2.0.0-app", "v3.0.0-lib"}, []string{"v", "-app"}); !ok || got != "v2.0.0-app" {
		t.Errorf("LatestTag with a suffix = %q, %v; want v2.0.0-app", got, ok)
	}
	if got, ok := LatestTag([]string{"lib@1.2.3.4", "lib@1.2.3"}, []string{"lib@", ""}); !ok || got != "lib@1.2.3.4" {
		t.Errorf("LatestTag with a .NET version = %q, %v; want lib@1.2.3.4", got, ok)
	}
	// The fourth part decides between otherwise-equal versions, in any order.
	for _, tags := range [][]string{{"lib@1.2.3.4", "lib@1.2.3.10"}, {"lib@1.2.3.10", "lib@1.2.3.4"}} {
		if got, _ := LatestTag(tags, []string{"lib@", ""}); got != "lib@1.2.3.10" {
			t.Errorf("LatestTag(%v) = %q, want lib@1.2.3.10", tags, got)
		}
	}
	if _, ok := LatestTag([]string{"lib@next", "app@1.0.0"}, []string{"lib@", ""}); ok {
		t.Error("LatestTag found a tag with no version")
	}
	// ${version} twice: both places hold the same version.
	twice := []string{"v", "-", ""}
	if got, ok := LatestTag([]string{"v1.0.0-1.0.0", "v2.0.0-1.0.0", "v1.5.0-1.5.0"}, twice); !ok || got != "v1.5.0-1.5.0" {
		t.Errorf("LatestTag with a repeated version = %q, %v; want v1.5.0-1.5.0", got, ok)
	}
	if _, ok := LatestTag([]string{"v1.0.0"}, []string{"v1.0.0"}); ok {
		t.Error("LatestTag matched a template with no version in it")
	}
}
