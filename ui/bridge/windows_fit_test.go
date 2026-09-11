package bridge

import (
	"context"
	"testing"
)

// The height arrives from a page. A window one pixel tall, or taller than any
// display, is not a state a caller should be able to ask for — and a failed
// render asking for zero would leave nothing to click on but a sliver.
func TestFitBoundsWhatAPageMayAskFor(t *testing.T) {
	w := NewWindows()
	var got int
	w.Resizable("desktops", func(h int) { got = h })

	for _, tc := range []struct{ asked, want int }{
		{0, minPopoverHeight},
		{-400, minPopoverHeight},
		{minPopoverHeight - 1, minPopoverHeight},
		{292, 292},
		{maxPopoverHeight + 1, maxPopoverHeight},
		{99999, maxPopoverHeight},
	} {
		if err := w.Fit(context.Background(), "desktops", tc.asked); err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("asked %d, resized to %d, want %d", tc.asked, got, tc.want)
		}
	}
}

// An unknown name is an error rather than a silent no-op, the same stance Open
// and Hide take: a popover that never resizes reads as a broken window.
func TestFitRefusesAnUnknownWindow(t *testing.T) {
	if err := NewWindows().Fit(context.Background(), "nope", 300); err == nil {
		t.Error("resized a window that was never registered")
	}
}

// Registering a window to open does not make it resizable: only the popover
// asks, and the other windows are sized by the person using them.
func TestFitRefusesAWindowThatIsOnlyOpenable(t *testing.T) {
	w := NewWindows()
	w.Register("main", func() {}, func() {})
	if err := w.Fit(context.Background(), "main", 300); err == nil {
		t.Error("resized a window that never offered to be resized")
	}
}
