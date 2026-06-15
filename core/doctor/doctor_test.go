package doctor

import (
	"context"
	"testing"
)

func TestCountsAndFixable(t *testing.T) {
	noop := func(context.Context) error { return nil }
	sections := []Section{{Title: "x", Results: []Result{
		{Name: "a", Status: OK},
		{Name: "b", Status: Warn, Fix: noop, FixLabel: "fix b"},
		{Name: "c", Status: Fail, Fix: noop, FixLabel: "fix c"},
		{Name: "d", Status: Fail}, // not fixable
		{Name: "e", Status: Info},
	}}}
	fails, warns, fixable := Counts(sections)
	if fails != 2 || warns != 1 || fixable != 2 {
		t.Fatalf("Counts = %d,%d,%d; want 2,1,2", fails, warns, fixable)
	}
	fx := Fixable(sections)
	if len(fx) != 2 || fx[0].Name != "b" || fx[1].Name != "c" {
		t.Fatalf("Fixable = %+v", fx)
	}
}
