package gitutil

import "testing"

func TestLatestTag(t *testing.T) {
	tags := []string{"lib@1.0.0", "lib@1.10.0", "lib@1.2.0", "lib@2.0.0-rc.1", "lib@next", "@acme/lib@9.0.0", "libx@5.0.0", "lib@"}
	if got, ok := LatestTag(tags, "lib@", ""); !ok || got != "lib@2.0.0-rc.1" {
		t.Errorf("LatestTag = %q, %v; want lib@2.0.0-rc.1", got, ok)
	}
	if got, ok := LatestTag([]string{"v1.0.0-app", "v2.0.0-app", "v3.0.0-lib"}, "v", "-app"); !ok || got != "v2.0.0-app" {
		t.Errorf("LatestTag with a suffix = %q, %v; want v2.0.0-app", got, ok)
	}
	if _, ok := LatestTag([]string{"lib@next", "app@1.0.0"}, "lib@", ""); ok {
		t.Error("LatestTag found a tag with no version")
	}
}
