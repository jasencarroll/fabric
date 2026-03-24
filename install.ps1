# Fabric installer for Windows
# Usage: irm https://raw.githubusercontent.com/jasencarroll/fabric-server/main/install.ps1 | iex
$ErrorActionPreference = "Stop"

$repo = "jasencarroll/fabric-server"
$installDir = if ($env:FABRIC_INSTALL_DIR) { $env:FABRIC_INSTALL_DIR } else { "$env:LOCALAPPDATA\fabric" }

$arch = if ([Environment]::Is64BitOperatingSystem) {
    if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "x86_64" }
} else {
    Write-Host "error: 32-bit systems not supported"; exit 1
}

$binary = "fabric-Windows-${arch}.exe"
$url = "https://github.com/${repo}/releases/latest/download/${binary}"

Write-Host "  fabric installer"
Write-Host "  os:      Windows"
Write-Host "  arch:    $arch"
Write-Host "  install: $installDir\fabric.exe"
Write-Host ""

New-Item -ItemType Directory -Force -Path $installDir | Out-Null

Write-Host "  downloading $binary..."
Invoke-WebRequest -Uri $url -OutFile "$installDir\fabric.exe" -UseBasicParsing

Write-Host "  installed to $installDir\fabric.exe"

$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$installDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$installDir;$userPath", "User")
    Write-Host ""
    Write-Host "  added $installDir to PATH"
    Write-Host "  restart your terminal for it to take effect"
}

Write-Host ""
Write-Host "  done! run 'fabric version' to verify."
