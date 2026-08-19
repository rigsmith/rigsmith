package status

import "testing"

// The three states are independent, and conflating them is how a report starts
// lying: Observation.InSync is false for pointer drift and unreadable halves
// too, so reading it as "desynced" would claim a desync that is not there.
func TestAccountInfoSeparatesDesyncFromDrift(t *testing.T) {
	var a AccountInfo

	// Pointer drift alone: the identity halves agree.
	a = AccountInfo{Email: "john@work.com", PointerEmail: "john@home.com"}
	if a.Desynced {
		t.Error("pointer drift was reported as a desync")
	}

	// A desync alone: clauderig's pointer may be perfectly correct.
	a = AccountInfo{Email: "john@work.com", Desynced: true}
	if a.PointerEmail != "" {
		t.Error("a desync invented pointer drift")
	}

	// A logout is neither.
	a = AccountInfo{LoggedOut: true}
	if a.Desynced || a.Problem != "" {
		t.Error("a logout was reported as a failure")
	}
}
