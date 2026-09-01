# Build the Windows NSIS installer for x64 or arm64.
# Run this script on Windows in PowerShell with Go, Node.js, Wails CLI and NSIS installed.
[CmdletBinding()]
param(
    [string]$Version = "",
    [switch]$SkipFrontend,
    [ValidateSet("x64", "arm64")]
    [string]$Architecture = "x64",
    [switch]$Sign,
    [string]$SignToolPath = "signtool.exe",
    [string]$CertificateFile,
    [string]$CertificatePassword
)

$ErrorActionPreference = "Stop"
$Root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$config = Get-Content (Join-Path $Root "wails.json") -Raw | ConvertFrom-Json
$configVersion = [string]$config.info.productVersion
if ([string]::IsNullOrWhiteSpace($Version)) { $Version = $configVersion }
if ($Version -ne $configVersion) { throw "Version '$Version' does not match wails.json productVersion '$configVersion'" }
$Dist = Join-Path $Root "dist\windows"
$Checksums = Join-Path $Root "dist\checksums"
$Bin = Join-Path $Root "build\bin"
$AppBinaryName = "MaxKB-Local-File-Sync.exe"
# Keep the Windows executable filename ASCII-only. Windows PowerShell 5.1 can
# misread UTF-8 scripts without a BOM and pass a mojibake filename to Wails.
# The user-facing product name remains the Chinese name from wails.json.
$WailsArchitecture = if ($Architecture -eq "x64") { "amd64" } else { "arm64" }
$ReleasePrefix = "MaxKB-Local-File-Sync-v$Version-windows-$Architecture"

New-Item -ItemType Directory -Force -Path $Dist, $Checksums | Out-Null

Push-Location $Root
try {
    # The generated NSIS package contains the install-scope selector. Do not
    # pass -installscope here: that flag creates a fixed-scope installer and
    # would defeat the runtime choice shown by build/windows/installer/project.nsi.
    $wailsArgs = @("build", "-clean", "-platform", "windows/$WailsArchitecture", "-trimpath", "-ldflags", "-s -w -X main.appVersion=v$Version", "-nsis", "-o", $AppBinaryName)
    if ($SkipFrontend) { $wailsArgs += "-s" }
    & wails @wailsArgs
    if ($LASTEXITCODE -ne 0) { throw "wails build failed" }
} finally {
    Pop-Location
}

$installer = Get-ChildItem -Path $Bin -Filter "*-installer.exe" | Sort-Object LastWriteTime -Descending | Select-Object -First 1
if ($null -eq $installer) { throw "NSIS installer was not found in $Bin" }
# Remove the old fixed-scope artifact if this directory was used by an older
# release script. A release directory must not accidentally publish two
# Windows installers with different semantics.
Get-ChildItem -Path $Dist -Filter "$ReleasePrefix-*-setup.exe" -ErrorAction SilentlyContinue |
    Remove-Item -Force
$target = Join-Path $Dist "$ReleasePrefix-setup.exe"
Copy-Item $installer.FullName $target -Force

if ($Sign) {
    if ([string]::IsNullOrWhiteSpace($CertificateFile)) {
        throw "-CertificateFile is required when -Sign is specified"
    }
    $signArgs = @("sign", "/fd", "SHA256", "/a", "/f", $CertificateFile)
    if (-not [string]::IsNullOrWhiteSpace($CertificatePassword)) {
        $signArgs += @("/p", $CertificatePassword)
    }
    $signArgs += $target
    & $SignToolPath @signArgs
    if ($LASTEXITCODE -ne 0) { throw "signtool failed for $target" }
}

$checksumFile = Join-Path $Checksums "$ReleasePrefix.sha256"
Get-FileHash $target -Algorithm SHA256 |
    ForEach-Object { "$($_.Hash.ToLowerInvariant())  $([System.IO.Path]::GetFileName($_.Path))" } |
    Set-Content -Encoding ascii $checksumFile

Write-Host "Created: $target"
Write-Host "SHA-256: $checksumFile"
