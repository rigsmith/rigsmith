# rigsmith installer (Windows / PowerShell)
# -----------------------------------------
# The PowerShell counterpart to scripts/install.sh. Detects the architecture,
# downloads the latest GoReleaser-built release zip from GitHub Releases, extracts
# it, drops the binaries into:
#
#     ${env:RIGSMITH_INSTALL:-$HOME\.local}\bin
#
# and adds that directory to the user PATH. This is the script behind:
#
#     irm https://rigsmith.sh | iex            # installs all tools (bundle zip)
#     irm https://rigsmith.sh/rig | iex        # just rig
#     irm https://rigsmith.sh/shiprig | iex    # just shiprig
#
# The install edge function bakes the requested tool in as $RigsmithTool; run
# directly it defaults to the whole family.
#
# Env:
#     RIGSMITH_INSTALL   install prefix (default: $HOME\.local) -> bin\ underneath
#     RIGSMITH_VERSION   pin a release tag (default: latest)
#     RIGSMITH_REPO      GitHub repo to download from (default: rigsmith/rigsmith)
#     RIGSMITH_REQUIRE_SIGNATURE=1
#                        refuse to install when cosign isn't there to check the
#                        signature (default: warn, and check the checksum only)
#
# Verification, as in install.sh: every release publishes checksums.txt (the
# SHA-256 of every archive and of the installers) and checksums.txt.sigstore.json,
# a Sigstore bundle signing it with the release workflow's GitHub OIDC identity.
# The archive is checked against checksums.txt (Get-FileHash) before anything is
# extracted; a mismatch, or a missing or invalid signature, installs nothing.
# Checking the signature needs cosign; without it the checksum is still checked,
# and the script says so. (Run as `irm | iex`, the script itself never touches
# disk, so there is no file of its own to check here; rigsmith.sh serves the
# release's copy, checked against the same checksums.txt when the site is built.)

$ErrorActionPreference = 'Stop'

$Repo = if ($env:RIGSMITH_REPO) { $env:RIGSMITH_REPO } else { 'rigsmith/rigsmith' }

# $RigsmithTool is injected by the install edge function; default to all.
$target = if ($RigsmithTool) { $RigsmithTool } else { 'all' }
$prefix = if ($env:RIGSMITH_INSTALL) { $env:RIGSMITH_INSTALL } else { Join-Path $HOME '.local' }
$binDir = Join-Path $prefix 'bin'
$version = if ($env:RIGSMITH_VERSION) { $env:RIGSMITH_VERSION } else { 'latest' }

function Info($m) { Write-Host "==> $m" -ForegroundColor Cyan }
function Fail($m) { Write-Host "error: $m" -ForegroundColor Red; exit 1 }
function Warn($m) { Write-Host "warning: $m" -ForegroundColor Yellow }

# brewrig is a real tool, just not one that exists here: Homebrew has no Windows
# build. Say that, rather than letting it fall through to "unknown target",
# which reads as a typo.
if ($target -eq 'brewrig') {
    Fail "brewrig is macOS/Linux only — Homebrew does not run on Windows"
}

$known = @('rig', 'shiprig', 'clauderig', 'codexrig', 'changerig', 'all')
if ($known -notcontains $target) {
    Fail "unknown target '$target' (expected: rig, shiprig, clauderig, codexrig, changerig, or omit for all)"
}

# --- detect arch -------------------------------------------------------------
$archRaw = $env:PROCESSOR_ARCHITEW6432
if (-not $archRaw) { $archRaw = $env:PROCESSOR_ARCHITECTURE }
$arch = switch ($archRaw) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    'x86'   { Fail '32-bit x86 is not supported' }
    default { Fail "unsupported architecture: $archRaw" }
}

# --- resolve the release tag -------------------------------------------------
function Resolve-Tag {
    if ($version -ne 'latest') { return $version }
    try {
        $headers = @{ 'User-Agent' = 'rigsmith-installer' }
        $rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers $headers
        if ($rel.tag_name) { return $rel.tag_name }
    } catch {}
    Fail "could not determine the latest release tag for $Repo"
}

$tag = Resolve-Tag
$ver = $tag.TrimStart('v')

# The bundle zip carries every Windows binary; a single tool has its own zip.
if ($target -eq 'all') {
    $binaries = @('rig', 'changerig', 'shiprig', 'clauderig', 'codexrig')
    $archive = "rigsmith_${ver}_windows_${arch}.zip"
} else {
    $binaries = @($target)
    $archive = "${target}_${ver}_windows_${arch}.zip"
}
$base = "https://github.com/$Repo/releases/download/$tag"

# The checksum checksums.txt lists for $name, or $null.
function Get-ListedSum($sumsPath, $name) {
    foreach ($line in Get-Content -LiteralPath $sumsPath) {
        $f = -split $line
        if ($f.Count -eq 2 -and $f[1].TrimStart('*') -eq $name) { return $f[0].ToLowerInvariant() }
    }
    return $null
}

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("rigsmith-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
try {
    # checksums.txt and its signature first: nothing is trusted before them.
    $sums = Join-Path $tmp 'checksums.txt'
    $bundle = Join-Path $tmp 'checksums.txt.sigstore.json'
    try { Invoke-WebRequest -Uri "$base/checksums.txt" -OutFile $sums -UseBasicParsing }
    catch { Fail "could not download checksums.txt for $tag — refusing to install unverified files" }
    try { Invoke-WebRequest -Uri "$base/checksums.txt.sigstore.json" -OutFile $bundle -UseBasicParsing }
    catch { Fail "$tag has no signature for its checksums.txt (checksums.txt.sigstore.json is missing) — refusing to install" }

    if (Get-Command cosign -ErrorAction SilentlyContinue) {
        $repoRe = [regex]::Escape($Repo)
        $cosignOut = & cosign verify-blob --bundle $bundle `
            --certificate-identity-regexp "^https://github.com/$repoRe/\.github/workflows/release\.yml@" `
            --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' $sums 2>&1
        if ($LASTEXITCODE -ne 0) {
            Write-Host ($cosignOut | Out-String)
            Fail "the signature on checksums.txt for $tag does not verify — refusing to install"
        }
        Info "verified the signature on checksums.txt"
    } elseif ($env:RIGSMITH_REQUIRE_SIGNATURE -eq '1') {
        Fail "cosign is not installed, so the signature on checksums.txt can't be checked, and RIGSMITH_REQUIRE_SIGNATURE=1 requires it"
    } else {
        Warn "cosign is not installed: checking the download against checksums.txt, but not checksums.txt's own signature."
        Write-Host "  Install cosign (https://docs.sigstore.dev/cosign/system_config/installation/) to verify it,"
        Write-Host "  or set RIGSMITH_REQUIRE_SIGNATURE=1 to refuse to install without it."
    }

    $zip = Join-Path $tmp $archive
    Info "downloading $archive ($tag)"
    Invoke-WebRequest -Uri "$base/$archive" -OutFile $zip -UseBasicParsing

    $want = Get-ListedSum $sums $archive
    if (-not $want) { Fail "$archive is not listed in checksums.txt for $tag — refusing to install" }
    $got = (Get-FileHash -Algorithm SHA256 -LiteralPath $zip).Hash.ToLowerInvariant()
    if ($got -ne $want) { Fail "$archive does not match checksums.txt (expected $want, got $got) — refusing to install" }
    Info "$archive matches the release's checksums"

    Info "extracting $archive"
    Expand-Archive -Path $zip -DestinationPath $tmp -Force

    New-Item -ItemType Directory -Force -Path $binDir | Out-Null
    foreach ($b in $binaries) {
        $src = Join-Path $tmp "$b.exe"
        if (-not (Test-Path $src)) { Fail "expected '$b.exe' inside $archive but it was not found" }
        Copy-Item $src (Join-Path $binDir "$b.exe") -Force
        Info "installed $b -> $binDir\$b.exe"
    }
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

# --- ensure the bin dir is on the user PATH ----------------------------------
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $binDir) {
    $newPath = if ($userPath) { "$binDir;$userPath" } else { $binDir }
    [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    $env:Path = "$binDir;$env:Path"
    Info "added $binDir to your user PATH — restart your terminal to pick it up"
}

Info "done."
