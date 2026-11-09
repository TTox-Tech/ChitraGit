# Installs the latest ChitraGit release on Windows.
#
#   irm https://raw.githubusercontent.com/TTox-Tech/ChitraGit/main/install.ps1 | iex
#
# Settings (environment variables):
#   CHITRAGIT_VERSION      a tag such as v0.3.0 (default: the latest release)
#   CHITRAGIT_INSTALL_DIR  where to put the binary (default: %LOCALAPPDATA%\Programs\chitragit)
#   CHITRAGIT_ALIASES      short names for it: any of chit, cg, git-chit
#                          (default: chit; "none" for none)
#
# The archive is checked against the release's checksums.txt before anything
# is installed. The folder is added to your user PATH.

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$Repo = 'TTox-Tech/ChitraGit'
$InstallDir = if ($env:CHITRAGIT_INSTALL_DIR) { $env:CHITRAGIT_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\chitragit' }

function Fail($msg) { Write-Error "chitragit install: $msg"; exit 1 }

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default { Fail "unsupported CPU $($env:PROCESSOR_ARCHITECTURE)" }
}

$tag = $env:CHITRAGIT_VERSION
if (-not $tag) {
    try {
        $release = Invoke-RestMethod -UseBasicParsing "https://api.github.com/repos/$Repo/releases/latest"
        $tag = $release.tag_name
    } catch {
        Fail "could not find the latest release: $($_.Exception.Message)"
    }
}
$version = $tag.TrimStart('v')
$archive = "chitragit_${version}_windows_${arch}.zip"
$base = "https://github.com/$Repo/releases/download/$tag"

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("chitragit-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    Write-Host "Downloading ChitraGit $tag for windows/$arch..."
    Invoke-WebRequest -UseBasicParsing "$base/$archive" -OutFile (Join-Path $tmp $archive)
    Invoke-WebRequest -UseBasicParsing "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt')

    $line = Get-Content (Join-Path $tmp 'checksums.txt') | Where-Object { ($_ -split '\s+')[1] -eq $archive }
    if (-not $line) { Fail "$archive is not listed in checksums.txt" }
    $want = ($line -split '\s+')[0].ToLower()
    $got = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $archive)).Hash.ToLower()
    if ($want -ne $got) { Fail "checksum mismatch for $archive (expected $want, got $got); nothing was installed" }
    Write-Host 'Checksum verified.'

    Expand-Archive -Path (Join-Path $tmp $archive) -DestinationPath (Join-Path $tmp 'x') -Force
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    Copy-Item (Join-Path $tmp 'x\chitragit.exe') (Join-Path $InstallDir 'chitragit.exe') -Force
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not (($userPath -split ';') -contains $InstallDir)) {
    [Environment]::SetEnvironmentVariable('Path', ($userPath.TrimEnd(';') + ";$InstallDir").TrimStart(';'), 'User')
    $env:Path += ";$InstallDir"
    Write-Host "Added $InstallDir to your user PATH. Open a new terminal to use it everywhere."
}

# Short names are hard links to the same exe (no admin rights needed, no extra
# disk), made again on every install so they never point at an old version.
# A name another program already uses is left alone.
$exe = Join-Path $InstallDir 'chitragit.exe'
$aliases = if ($env:CHITRAGIT_ALIASES) { $env:CHITRAGIT_ALIASES } else { 'chit' }
foreach ($name in ($aliases -split '[\s,]+' | Where-Object { $_ -and $_ -ne 'none' })) {
    if ($name -notin 'chit', 'cg', 'git-chit') { Write-Host "Skipped '$name': short names are chit, cg and git-chit."; continue }
    $link = Join-Path $InstallDir "$name.exe"
    $other = Get-Command $name -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($other -and $other.Source -ne $link) { Write-Host "Skipped '$name': $($other.Source) already uses that name."; continue }
    try {
        Remove-Item $link -Force -ErrorAction SilentlyContinue
        try { New-Item -ItemType HardLink -Path $link -Target $exe | Out-Null } catch { Copy-Item $exe $link -Force }
        Write-Host "Added ${name}: run '$($name -replace '-', ' ') sync' just like 'chitragit sync'."
    } catch {
        Write-Host "Skipped '$name': $($_.Exception.Message)"
    }
}

& $exe version
Write-Host "Next: cd into your archive folder and run 'chitragit init'."
