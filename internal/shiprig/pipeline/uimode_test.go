// Ported from net-changesets Release/ReleaseUiModeTests.cs (6 tests).
package pipeline

import "testing"

func TestUIModeTerminalDefaultsToRichAndInteractive(t *testing.T) {
	mode := ResolveUIMode(false, false, false, false, false)

	if !mode.Rich {
		t.Error("Rich should be true on a terminal")
	}
	if !mode.Interactive {
		t.Error("Interactive should be true on a terminal")
	}
}

func TestUIModeRedirectedOutputIsPlainAndNonInteractive(t *testing.T) {
	mode := ResolveUIMode(false, false, false, true, false)

	if mode.Rich {
		t.Error("Rich should be false with redirected output")
	}
	if mode.Interactive {
		t.Error("Interactive should be false with redirected output")
	}
}

func TestUIModeForceUIIsRichButNotInteractiveWhenPiped(t *testing.T) {
	mode := ResolveUIMode(true, false, false, true, true)

	if !mode.Rich {
		t.Error("--ui should force Rich even when piped")
	}
	if mode.Interactive {
		t.Error("Interactive should stay false when piped")
	}
}

func TestUIModeNoUIWinsOverUI(t *testing.T) {
	mode := ResolveUIMode(true, true, false, false, false)

	if mode.Rich {
		t.Error("--no-ui should win over --ui")
	}
	if !mode.Interactive {
		t.Error("--no-ui still prompts on a terminal")
	}
}

func TestUIModeYesDisablesInteractivity(t *testing.T) {
	mode := ResolveUIMode(false, false, true, false, false)

	if !mode.Rich {
		t.Error("Rich should remain true")
	}
	if mode.Interactive {
		t.Error("--yes should disable interactivity")
	}
}

func TestUIModeRedirectedInputDisablesInteractivity(t *testing.T) {
	mode := ResolveUIMode(false, false, false, false, true)

	if !mode.Rich {
		t.Error("Rich should remain true")
	}
	if mode.Interactive {
		t.Error("redirected input should disable interactivity")
	}
}
