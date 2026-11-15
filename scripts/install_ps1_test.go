package scripts

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// install.ps1 runs under PowerShell 7 (pwsh) here, on any OS that has it. The
// network is replaced the PowerShell way: functions shadow cmdlets and
// commands, so the prelude's Invoke-WebRequest serves the release from a
// directory, and its cosign accepts only the bundle the release was signed with.
const ps1Prelude = `
function Invoke-WebRequest {
    param([string]$Uri, [string]$OutFile, [switch]$UseBasicParsing)
    $src = Join-Path $env:FAKE_ASSETS (($Uri -split '/')[-1])
    if (-not (Test-Path -LiteralPath $src)) { throw "404 Not Found: $Uri" }
    Copy-Item -LiteralPath $src -Destination $OutFile
}
`

const ps1FakeCosign = `
function cosign {
    $i = [array]::IndexOf($args, '--bundle')
    if ((Get-Content -Raw -LiteralPath $args[$i + 1]) -match 'by the release workflow') { $global:LASTEXITCODE = 0 }
    else { Write-Output 'invalid signature'; $global:LASTEXITCODE = 1 }
}
`

type ps1Release struct {
	t      *testing.T
	assets string
	prefix string
	cosign bool
	env    []string
}

func newPS1Release(t *testing.T) *ps1Release {
	t.Helper()
	if _, err := exec.LookPath("pwsh"); err != nil {
		t.Skip("pwsh not installed")
	}
	r := &ps1Release{t: t, assets: t.TempDir(), prefix: t.TempDir(), cosign: true}
	archive := filepath.Join(r.assets, "rig_0.0.1_windows_amd64.zip")
	r.zip(archive, "rig.exe", "MZ fake rig")
	sum := fileSHA256(t, archive)
	r.write("checksums.txt", sum+"  rig_0.0.1_windows_amd64.zip\n")
	r.write("checksums.txt.sigstore.json", `{"signed":"by the release workflow"}`)
	return r
}

func (r *ps1Release) write(name, body string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.assets, name), []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *ps1Release) zip(path, name, body string) {
	r.t.Helper()
	f, err := os.Create(path)
	if err != nil {
		r.t.Fatal(err)
	}
	w := zip.NewWriter(f)
	fw, err := w.Create(name)
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err := fw.Write([]byte(body)); err != nil {
		r.t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		r.t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		r.t.Fatal(err)
	}
}

// run installs rig with install.ps1 against the fake release.
func (r *ps1Release) run() (string, error) {
	r.t.Helper()
	script, err := filepath.Abs("install.ps1")
	if err != nil {
		r.t.Fatal(err)
	}
	prelude := ps1Prelude
	if r.cosign {
		prelude += ps1FakeCosign
	}
	cmd := exec.Command("pwsh", "-NoProfile", "-NonInteractive", "-Command",
		prelude+"\n$RigsmithTool = 'rig'\n. '"+script+"'")
	cmd.Env = append(os.Environ(),
		"FAKE_ASSETS="+r.assets, "RIGSMITH_VERSION=v0.0.1", "RIGSMITH_INSTALL="+r.prefix,
		"PROCESSOR_ARCHITECTURE=AMD64", "PROCESSOR_ARCHITEW6432=", "RIGSMITH_REQUIRE_SIGNATURE=")
	cmd.Env = append(cmd.Env, r.env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (r *ps1Release) installed() bool {
	_, err := os.Stat(filepath.Join(r.prefix, "bin", "rig.exe"))
	return err == nil
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestInstallPS1_VerifiesBeforeInstalling(t *testing.T) {
	t.Run("verified release installs", func(t *testing.T) {
		r := newPS1Release(t)
		out, err := r.run()
		if err != nil || !r.installed() {
			t.Fatalf("a verified release didn't install: %v\n%s", err, out)
		}
		for _, want := range []string{"verified the signature on checksums.txt", "matches the release's checksums"} {
			if !strings.Contains(out, want) {
				t.Errorf("output missing %q:\n%s", want, out)
			}
		}
	})
	t.Run("tampered archive installs nothing", func(t *testing.T) {
		r := newPS1Release(t)
		r.zip(filepath.Join(r.assets, "rig_0.0.1_windows_amd64.zip"), "rig.exe", "MZ something else")
		out, err := r.run()
		if err == nil || r.installed() || !strings.Contains(out, "does not match checksums.txt") {
			t.Fatalf("a tampered archive was not refused: %v\n%s", err, out)
		}
	})
	t.Run("missing signature installs nothing", func(t *testing.T) {
		r := newPS1Release(t)
		if err := os.Remove(filepath.Join(r.assets, "checksums.txt.sigstore.json")); err != nil {
			t.Fatal(err)
		}
		out, err := r.run()
		if err == nil || r.installed() || !strings.Contains(out, "has no signature") {
			t.Fatalf("a missing signature was not refused: %v\n%s", err, out)
		}
	})
	t.Run("invalid signature installs nothing", func(t *testing.T) {
		r := newPS1Release(t)
		r.write("checksums.txt.sigstore.json", `{"signed":"by someone else"}`)
		out, err := r.run()
		if err == nil || r.installed() || !strings.Contains(out, "does not verify") {
			t.Fatalf("an invalid signature was not refused: %v\n%s", err, out)
		}
	})
	t.Run("without cosign: checksum only, said plainly", func(t *testing.T) {
		if _, err := exec.LookPath("cosign"); err == nil {
			t.Skip("a real cosign on PATH would be found")
		}
		r := newPS1Release(t)
		r.cosign = false
		out, err := r.run()
		if err != nil || !r.installed() {
			t.Fatalf("without cosign the checksum alone should install: %v\n%s", err, out)
		}
		if !strings.Contains(out, "cosign is not installed") || !strings.Contains(out, "RIGSMITH_REQUIRE_SIGNATURE=1") {
			t.Errorf("the fallback isn't said plainly:\n%s", out)
		}
	})
	t.Run("RIGSMITH_REQUIRE_SIGNATURE=1 without cosign installs nothing", func(t *testing.T) {
		if _, err := exec.LookPath("cosign"); err == nil {
			t.Skip("a real cosign on PATH would be found")
		}
		r := newPS1Release(t)
		r.cosign = false
		r.env = []string{"RIGSMITH_REQUIRE_SIGNATURE=1"}
		out, err := r.run()
		if err == nil || r.installed() {
			t.Fatalf("RIGSMITH_REQUIRE_SIGNATURE=1 installed without a signature check:\n%s", out)
		}
	})
}
