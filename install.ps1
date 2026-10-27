# Builds mslrt and installs it for the current user (no administrator rights
# needed): programs go to %LOCALAPPDATA%\Programs\mslrt, .mrc files open with
# mslrt when double-clicked, and `mslrt` is added to your PATH.
#
#   .\install.ps1            build and install
#   .\install.ps1 -Uninstall remove the file type, PATH entry and programs

param([switch]$Uninstall)
$ErrorActionPreference = 'Stop'

$installed = Join-Path $env:LOCALAPPDATA 'Programs\mslrt\mslrt.exe'
if ($Uninstall) {
    if (-not (Test-Path $installed)) { Write-Host 'mslrt is not installed.'; exit 0 }
    & $installed uninstall
    exit $LASTEXITCODE
}

$go = Get-Command go -ErrorAction SilentlyContinue
if (-not $go) {
    $fallback = 'C:\Program Files\Go\bin\go.exe'
    if (Test-Path $fallback) { $go = $fallback } else { throw 'Go is needed to build mslrt: https://go.dev/dl/' }
}
$go = if ($go -is [System.Management.Automation.CommandInfo]) { $go.Source } else { $go }

$root = $PSScriptRoot
$out = Join-Path $root 'bin'
New-Item -ItemType Directory -Force $out | Out-Null

Write-Host 'Building mslrt.exe (terminal) and mslrtw.exe (for double-click)...'
& $go -C $root build -o (Join-Path $out 'mslrt.exe') ./cmd/mslrt
if ($LASTEXITCODE) { throw 'build failed' }
& $go -C $root build -ldflags '-H windowsgui' -o (Join-Path $out 'mslrtw.exe') ./cmd/mslrt
if ($LASTEXITCODE) { throw 'build failed' }

& (Join-Path $out 'mslrt.exe') install
exit $LASTEXITCODE
