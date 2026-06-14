package cli

import "testing"

func TestGoRemovalSpec(t *testing.T) {
	// A bare module path gets the @none suffix that drops it.
	if got := goRemovalSpec("example.com/foo"); got != "example.com/foo@none" {
		t.Fatalf("bare = %q, want example.com/foo@none", got)
	}
	// An explicit @version/@none the caller wrote is left untouched.
	if got := goRemovalSpec("example.com/foo@none"); got != "example.com/foo@none" {
		t.Fatalf("explicit @none = %q, want unchanged", got)
	}
	if got := goRemovalSpec("example.com/foo@v1.2.3"); got != "example.com/foo@v1.2.3" {
		t.Fatalf("explicit @version = %q, want unchanged", got)
	}
}
