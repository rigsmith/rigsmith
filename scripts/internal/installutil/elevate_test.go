package installutil

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"unicode/utf16"
)

// The elevated child cannot inherit the launcher's environment, so the script
// it runs has to carry everything: the marker, every RIGSMITH_* variable, the
// working directory, and the installer itself, each quoted so that spaces and
// apostrophes in paths survive.
func TestElevatedCommandCarriesTheContext(t *testing.T) {
	environ := []string{
		"PATH=/usr/bin",
		"RIGSMITH_INSTALL=C:\\Program Files\\rig's tools",
		"RIGSMITH_DEV_BIN=D:\\bin",
		elevatedEnv + "=stale", // never forwarded as-is: the child sets it to 1 itself
		"HOME=/home/x",
	}
	got := elevatedCommand(`C:\Users\me\AppData\Local\go-build\exe\source-install.exe`, `C:\src\rigsmith`, environ)
	for _, want := range []string{
		"$env:" + elevatedEnv + " = '1'\n",
		"$env:RIGSMITH_INSTALL = 'C:\\Program Files\\rig''s tools'\n",
		"$env:RIGSMITH_DEV_BIN = 'D:\\bin'\n",
		"Set-Location -LiteralPath 'C:\\src\\rigsmith'\n",
		"& 'C:\\Users\\me\\AppData\\Local\\go-build\\exe\\source-install.exe'\n",
		"exit $LASTEXITCODE\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("script missing %q:\n%s", want, got)
		}
	}
	for _, leak := range []string{"PATH", "HOME", "stale"} {
		if strings.Contains(got, leak) {
			t.Errorf("script forwards %q, which is not the installer's:\n%s", leak, got)
		}
	}
	// The marker is set before the installer runs, not after.
	if strings.Index(got, "$env:"+elevatedEnv) > strings.Index(got, "& '") {
		t.Error("marker is set after the installer runs")
	}
}

func TestEncodeCommandIsUTF16LEBase64(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString(encodeCommand("exit 0"))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw)%2 != 0 {
		t.Fatalf("odd byte count %d for UTF-16", len(raw))
	}
	u := make([]uint16, len(raw)/2)
	for i := range u {
		u[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	if got := string(utf16.Decode(u)); got != "exit 0" {
		t.Fatalf("decoded %q", got)
	}
}

// PrepareDir's control flow around elevation is the same on every platform;
// only the elevation itself is Windows. Force each branch through the seams.
func TestPrepareDirElevationFlow(t *testing.T) {
	perm := errors.New("access denied")
	swap := func(t *testing.T, w func(string) error, e func(error) bool, r func() error, env string) {
		t.Helper()
		pw, pe, pr, pg := writable, elevate, relaunch, getenv
		writable, elevate, relaunch = w, e, r
		getenv = func(k string) string {
			if k == elevatedEnv {
				return env
			}
			return ""
		}
		t.Cleanup(func() { writable, elevate, relaunch, getenv = pw, pe, pr, pg })
	}
	isPerm := func(err error) bool { return errors.Is(err, perm) }
	unwritable := func(string) error { return perm }

	t.Run("a protected directory relaunches elevated", func(t *testing.T) {
		called := false
		swap(t, unwritable, isPerm, func() error { called = true; return nil }, "")
		relaunched, err := PrepareDir("C:/Program Files/rig")
		if err != nil || !relaunched || !called {
			t.Fatalf("relaunched=%v called=%v err=%v", relaunched, called, err)
		}
	})
	t.Run("a relaunch failure propagates", func(t *testing.T) {
		boom := errors.New("consent refused")
		swap(t, unwritable, isPerm, func() error { return boom }, "")
		if relaunched, err := PrepareDir("x"); !errors.Is(err, boom) || relaunched {
			t.Fatalf("relaunched=%v err=%v, want the relaunch error", relaunched, err)
		}
	})
	t.Run("already elevated never relaunches again", func(t *testing.T) {
		swap(t, unwritable, isPerm, func() error { t.Fatal("relaunched while elevated"); return nil }, "1")
		relaunched, err := PrepareDir("x")
		if err == nil || relaunched || !strings.Contains(err.Error(), "after elevation") {
			t.Fatalf("relaunched=%v err=%v", relaunched, err)
		}
	})
	t.Run("an error that elevation cannot fix is returned as is", func(t *testing.T) {
		other := errors.New("disk full")
		swap(t, func(string) error { return other }, isPerm, func() error { t.Fatal("relaunched for a non-permission error"); return nil }, "")
		if relaunched, err := PrepareDir("x"); !errors.Is(err, other) || relaunched {
			t.Fatalf("relaunched=%v err=%v", relaunched, err)
		}
	})
	t.Run("a writable directory needs nothing", func(t *testing.T) {
		swap(t, func(string) error { return nil }, isPerm, func() error { t.Fatal("relaunched for a writable dir"); return nil }, "")
		if relaunched, err := PrepareDir("x"); err != nil || relaunched {
			t.Fatalf("relaunched=%v err=%v", relaunched, err)
		}
	})
}
