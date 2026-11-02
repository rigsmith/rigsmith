package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// Runs in ordinary Windows CI without RIG_STACK_E2E or a dotnet SDK.
func TestStackCIGuardWindowsPathSpellings(t *testing.T) {
	root, member, sibling := stackGuardPathFixture(t)
	t.Run("case", func(t *testing.T) {
		stackGuardCheckPathSpellings(t, strings.ToUpper(root), member, sibling, strings.ToUpper(member), strings.ToUpper(sibling))
	})
	t.Run("drive-case", func(t *testing.T) {
		lowerDrive := func(path string) string {
			volume := filepath.VolumeName(path)
			return strings.ToLower(volume) + strings.TrimPrefix(path, volume)
		}
		stackGuardCheckPathSpellings(t, lowerDrive(root), member, sibling, lowerDrive(member), lowerDrive(sibling))
	})
	t.Run("short-names", func(t *testing.T) {
		short := func(path string) string {
			t.Helper()
			src, err := windows.UTF16PtrFromString(path)
			if err != nil {
				t.Fatal(err)
			}
			buf := make([]uint16, 32768)
			n, err := windows.GetShortPathName(src, &buf[0], uint32(len(buf)))
			if err != nil || n == 0 || int(n) >= len(buf) {
				t.Fatalf("GetShortPathName: %d %v", n, err)
			}
			return windows.UTF16ToString(buf[:n])
		}
		shortRoot, shortMember, shortSibling := short(root), short(member), short(sibling)
		if strings.EqualFold(shortMember, member) && strings.EqualFold(shortSibling, sibling) {
			t.Skip("8.3 aliases are disabled on this volume")
		}
		stackGuardCheckPathSpellings(t, shortRoot, member, sibling, shortMember, shortSibling)
	})
}
