# Builds Linux release bundles: one self-contained tarball per architecture with
# the alfad binary (Web OS embedded), installer, systemd unit and Compose files.
#   .\package.ps1                      # all targets
#   .\package.ps1 -Version 0.2.0 -Targets linux/arm64
param(
    [string]$Version = "0.1.0",
    [string[]]$Targets = @("linux/amd64", "linux/arm64", "linux/arm/v7")
)
$ErrorActionPreference = "Stop"
$root = $PSScriptRoot
$dist = Join-Path $root "dist"
New-Item -ItemType Directory -Force $dist | Out-Null

$first = $true
$sums = @()
foreach ($t in $Targets) {
    $os, $arch, $variant = $t -split "/"
    $label = "$arch$variant"                          # amd64, arm64, armv7
    if ($first) { & "$root\build.ps1" -Target $t -Version $Version } else { & "$root\build.ps1" -Target $t -Version $Version -SkipUI }
    $first = $false

    $name = "alfaos-$Version-$os-$label"
    $stage = Join-Path $dist $name
    if (Test-Path $stage) { Remove-Item -Recurse -Force $stage }
    New-Item -ItemType Directory -Force "$stage\rootfs\data" | Out-Null

    Copy-Item "$root\backend\bin\alfad-$os-$label" "$stage\alfad"
    foreach ($f in "install.sh", "alfad.service", "docker-compose.yml", "docker-compose.nvidia.yml", "Dockerfile.bundle") {
        Copy-Item "$root\deploy\$f" "$stage\$f"
    }
    Set-Content -NoNewline -Encoding ascii "$stage\VERSION" $Version
    Set-Content -NoNewline -Encoding ascii "$stage\ARCH" $label
    Set-Content -NoNewline -Encoding ascii "$stage\rootfs\data\.keep" ""
    @"
Alfa OS $Version for Linux ($label)

Install (Docker mode, recommended: Traefik + HTTPS on ports 80/443):
    sudo bash install.sh

Install without Docker containers (systemd service, HTTPS on port 443):
    sudo bash install.sh --native

Options:  --storage /path/for/files   --install-docker   --yes
Remove:   sudo bash install.sh --uninstall   (your files are never deleted)
"@ | Set-Content -Encoding ascii "$stage\README.txt"

    $tgz = Join-Path $dist "$name.tar.gz"
    if (Test-Path $tgz) { Remove-Item $tgz }
    tar.exe -czf $tgz -C $dist $name
    if ($LASTEXITCODE -ne 0) { throw "tar failed for $name" }
    Remove-Item -Recurse -Force $stage
    $hash = (Get-FileHash -Algorithm SHA256 $tgz).Hash.ToLower()
    $sums += "$hash  $name.tar.gz"
    Write-Host ("packaged {0} ({1:N1} MB)" -f "$name.tar.gz", ((Get-Item $tgz).Length / 1MB))
}
$sums | Set-Content -Encoding ascii (Join-Path $dist "SHA256SUMS")
