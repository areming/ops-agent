# Cross-compile ops into ./dist as single static binaries.
# Pure Go (CGO disabled) keeps each output dependency-free.
$ErrorActionPreference = "Stop"
$env:CGO_ENABLED = "0"

# Version is stamped into the binary so `ops --version` and the connect
# self-install (fetch matching release) agree. Override with $env:OPS_VERSION;
# otherwise derive from git, falling back to "dev".
$Version = $env:OPS_VERSION
if (-not $Version) { $Version = (git describe --tags --always 2>$null) }
if (-not $Version) { $Version = "dev" }
$ldflags = "-X github.com/areming/ops-agent/internal/version.Value=$Version"
Write-Host "version $Version"

$targets = @(
    @{ os = "linux";   arch = "amd64"; ext = "" },
    @{ os = "linux";   arch = "arm64"; ext = "" },
    @{ os = "windows"; arch = "amd64"; ext = ".exe" }
)

New-Item -ItemType Directory -Force -Path dist | Out-Null

foreach ($t in $targets) {
    $env:GOOS = $t.os
    $env:GOARCH = $t.arch
    $out = "dist/ops-$($t.os)-$($t.arch)$($t.ext)"
    Write-Host "building $out"
    go build -trimpath -ldflags $ldflags -o $out ./cmd/ops
}

Write-Host "done -> ./dist"
