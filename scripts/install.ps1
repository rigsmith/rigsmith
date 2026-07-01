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
# directly it defaults to all four.
#
# Env:
#     RIGSMITH_INSTALL   install prefix (default: $HOME\.local) -> bin\ underneath
#     RIGSMITH_VERSION   pin a release tag (default: latest)

$ErrorActionPreference = 'Stop'

$Repo = 'rigsmith/rigsmith'

# $RigsmithTool is injected by the install edge function; default to all.
$target = if ($RigsmithTool) { $RigsmithTool } else { 'all' }
$prefix = if ($env:RIGSMITH_INSTALL) { $env:RIGSMITH_INSTALL } else { Join-Path $HOME '.local' }
$binDir = Join-Path $prefix 'bin'
$version = if ($env:RIGSMITH_VERSION) { $env:RIGSMITH_VERSION } else { 'latest' }

function Info($m) { Write-Host "==> $m" -ForegroundColor Cyan }
function Fail($m) { Write-Host "error: $m" -ForegroundColor Red; exit 1 }

$known = @('rig', 'shiprig', 'clauderig', 'changerig', 'all')
if ($known -notcontains $target) {
    Fail "unknown target '$target' (expected: rig, shiprig, clauderig, changerig, or omit for all)"
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

# The bundle zip carries all four binaries; a single tool has its own zip.
if ($target -eq 'all') {
    $binaries = @('rig', 'changerig', 'shiprig', 'clauderig')
    $archive = "rigsmith_${ver}_windows_${arch}.zip"
} else {
    $binaries = @($target)
    $archive = "${target}_${ver}_windows_${arch}.zip"
}
$url = "https://github.com/$Repo/releases/download/$tag/$archive"

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("rigsmith-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
try {
    $zip = Join-Path $tmp $archive
    Info "downloading $archive ($tag)"
    Invoke-WebRequest -Uri $url -OutFile $zip -UseBasicParsing

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
