package doctor

import (
	"context"
	"errors"
	"testing"
)

// Every check needs an id, or a front end that is not a terminal cannot name it
// — and a check added later without one is invisible to the window rather than
// visibly broken, which is the failure mode worth a test.
func TestEveryCheckHasAnID(t *testing.T) {
	seen := map[string]string{}
	for _, sec := range Run(context.Background(), Env{}) {
		for _, r := range sec.Results {
			if r.ID == "" {
				t.Errorf("check %q has no ID — nothing outside a terminal can ask for it", r.Name)
				continue
			}
			// Ids are an addressing scheme; two checks answering to one name
			// means Fix repairs whichever happens to come first.
			if prev, dup := seen[r.ID]; dup {
				t.Errorf("id %q is used by both %q and %q", r.ID, prev, r.Name)
			}
			seen[r.ID] = r.Name
		}
	}
	if len(seen) == 0 {
		t.Fatal("no checks ran at all")
	}
}

// The two refusals point a caller in different directions, so they must be
// distinguishable rather than both being "no".
func TestFixDistinguishesUnknownFromUnfixable(t *testing.T) {
	ctx := context.Background()
	if err := Fix(ctx, Env{}, "no-such-check-at-all"); !errors.Is(err, ErrNoSuchCheck) {
		t.Errorf("unknown id gave %v, want ErrNoSuchCheck", err)
	}
	// `git` reports whether git is installed and cannot install it.
	if err := Fix(ctx, Env{}, "git"); err != nil && !errors.Is(err, ErrNotFixable) {
		t.Errorf("report-only check gave %v, want ErrNotFixable or nil", err)
	}
}

func TestFindReturnsTheNamedCheck(t *testing.T) {
	r, err := Find(context.Background(), Env{}, "git")
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != "git" || r.Name == "" {
		t.Errorf("Find returned %+v", r)
	}
}
