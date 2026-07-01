//go:build windows

package brand

import "golang.org/x/sys/windows"

// The banner's frame (╭─╴ ╶─╮), the interior glyphs (● ↻ ↑ ✳), and the "·"
// separator are UTF-8. A legacy Windows console (conhost) defaults to an OEM code
// page such as 437, which decodes those bytes as mojibake (Γò¡ΓöÇ… ┬╖) — the
// garbled logo people see on Windows. Switching the console's OUTPUT code page to
// UTF-8 (65001) at startup makes the designed banner render correctly.
//
// Every rigsmith CLI imports core/brand for its banner, so this init runs for all
// of them with no per-main wiring. It's best-effort: SetConsoleOutputCP is a no-op
// when output is redirected or there's no console, and kernel32 always exports it,
// so this neither fails a build nor panics at runtime.
func init() {
	const cpUTF8 = 65001
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleOutputCP")
	if proc.Find() == nil {
		_, _, _ = proc.Call(uintptr(cpUTF8))
	}
}
