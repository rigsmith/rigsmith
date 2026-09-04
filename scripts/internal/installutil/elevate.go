package installutil

import (
	"encoding/base64"
	"os"
	"sort"
	"strings"
	"unicode/utf16"
)

// forwardedEnvPrefix selects the environment the elevated installer has to see:
// every RIGSMITH_* variable, which is where a custom prefix (RIGSMITH_INSTALL,
// RIGSMITH_DEV_BIN), a build source (RIGSMITH_DEV_SRC) and the elevation marker
// itself all live.
const forwardedEnvPrefix = "RIGSMITH_"

// elevatedCommand renders the PowerShell the elevated child runs.
//
// A process started with `Start-Process -Verb RunAs` is created by the AppInfo
// service from a fresh environment, not the caller's block, so nothing set on
// the launcher reaches it — neither the marker that stops a second elevation
// nor the custom prefix that made elevation necessary in the first place.
// Rather than hand the child variables, the child is a script that sets them
// itself, moves to the working directory, and only then runs the installer.
// Values are single-quoted PowerShell literals, where the only escape is a
// doubled quote, so a path with a space or an apostrophe survives intact.
func elevatedCommand(exe, cwd string, environ []string) string {
	vars := map[string]string{elevatedEnv: "1"}
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(k, forwardedEnvPrefix) || k == elevatedEnv {
			continue
		}
		vars[k] = v
	}
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("$ErrorActionPreference = 'Stop'\n")
	for _, k := range keys {
		b.WriteString("$env:" + k + " = " + psQuote(vars[k]) + "\n")
	}
	b.WriteString("Set-Location -LiteralPath " + psQuote(cwd) + "\n")
	b.WriteString("& " + psQuote(exe) + "\n")
	b.WriteString("exit $LASTEXITCODE\n")
	return b.String()
}

// psQuote renders s as a single-quoted PowerShell string literal.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// encodeCommand renders a script the way `powershell -EncodedCommand` expects
// it: UTF-16LE, base64. Passing the script that way sidesteps every layer of
// quoting between here and the elevated process — Start-Process joins its
// argument list with spaces and quotes nothing.
func encodeCommand(script string) string {
	u := utf16.Encode([]rune(script))
	raw := make([]byte, 0, len(u)*2)
	for _, c := range u {
		raw = append(raw, byte(c), byte(c>>8))
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// The seams PrepareDir runs through, as variables so a test can force the
// protected-directory path on any platform: the real elevation only exists on
// Windows, and the control flow around it is where a regression would hide.
var (
	writable = ensureWritable
	elevate  = shouldElevate
	relaunch = relaunchElevated
	getenv   = os.Getenv
)
