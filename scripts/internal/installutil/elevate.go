package installutil

import (
	"encoding/base64"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"
)

// forwardedEnvPrefix selects the environment the elevated installer has to see:
// every RIGSMITH_* variable, which is where a custom prefix (RIGSMITH_INSTALL,
// RIGSMITH_DEV_BIN), a build source (RIGSMITH_DEV_SRC) and the elevation marker
// itself all live. Matched without regard to case: Windows variable names
// have none, and `$env:rigsmith_install` reaches the installer's own
// os.Getenv just the same.
const forwardedEnvPrefix = "RIGSMITH_"

// forwardedAlways names the variables forwarded whatever their prefix. PATH
// is the one: the elevated child starts from the machine's registry PATH,
// and the toolchain that built the launcher — `go` from a version manager,
// an IDE terminal, a profile script — is often only on the session's.
var forwardedAlways = map[string]bool{"PATH": true}

// envName is the shape a variable name may take to be written into the
// script. Names come from the caller's environment, and a name is spliced
// into PowerShell syntax unquoted — so one carrying `;`, a newline or a
// brace is not a variable, it is an injection, and is dropped.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

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
		if !ok || !envName.MatchString(k) || strings.EqualFold(k, elevatedEnv) {
			continue
		}
		upper := strings.ToUpper(k)
		if !strings.HasPrefix(upper, forwardedEnvPrefix) && !forwardedAlways[upper] {
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
