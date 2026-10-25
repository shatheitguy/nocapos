# Builds NoCapOS as a native app: the Web OS (frontend) is bundled and embedded
# into alfad, producing a single executable in backend\bin.
#   .\build.ps1                      # alfad.exe for this machine
#   .\build.ps1 -Target linux/arm64  # cross-compile, e.g. for a Raspberry Pi
#   .\build.ps1 -SkipUI              # reuse the already-built UI in backend\web\dist
param(
    [string]$Target = "",
    [string]$Version = "0.1.0-dev",
    [switch]$SkipUI
)
$ErrorActionPreference = "Stop"

if (-not $SkipUI) {
    Push-Location "$PSScriptRoot\frontend"
    try {
        # Through cmd: Windows PowerShell 5.1 turns npm's stderr progress into errors under "Stop".
        if (-not (Test-Path node_modules)) { cmd /c "npm install --no-audit --no-fund 2>&1"; if ($LASTEXITCODE -ne 0) { throw "npm install failed" } }
        cmd /c "npm run build 2>&1" | Select-String -Pattern "built in|error" | ForEach-Object { $_.Line }
        if ($LASTEXITCODE -ne 0) { throw "frontend build failed" }
    }
    finally { Pop-Location }
}

Push-Location "$PSScriptRoot\backend"
try {
    $env:CGO_ENABLED = "0"
    $os = "windows"
    $out = "bin\alfad.exe"
    $capOut = "bin\cap.exe"
    if ($Target) {
        $os, $arch, $variant = $Target -split "/"
        $env:GOOS = $os; $env:GOARCH = $arch
        if ($variant) { $env:GOARM = $variant.TrimStart("v") }
        $out = "bin\alfad-$os-$arch$variant" + $(if ($os -eq "windows") { ".exe" } else { "" })
        $capOut = "bin\cap-$os-$arch$variant"
    }
    go build -trimpath -ldflags "-s -w -X main.version=$Version" -o $out .\cmd\alfad
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
    Write-Host "built $((Resolve-Path $out).Path)"

    # cap (the admin CLI) only makes sense on the Linux hosts NoCapOS installs on.
    if ($os -eq "linux") {
        go build -trimpath -ldflags "-s -w -X main.version=$Version" -o $capOut .\cmd\cap
        if ($LASTEXITCODE -ne 0) { throw "go build failed (cap)" }
        Write-Host "built $((Resolve-Path $capOut).Path)"
    }
}
finally {
    Remove-Item Env:GOOS, Env:GOARCH, Env:GOARM -ErrorAction SilentlyContinue
    Pop-Location
}
