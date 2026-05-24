# Cross-compile opsagent into ./dist as single static binaries.
# Pure Go (CGO disabled) keeps each output dependency-free.
$ErrorActionPreference = "Stop"
$env:CGO_ENABLED = "0"

$targets = @(
    @{ os = "linux";   arch = "amd64"; ext = "" },
    @{ os = "linux";   arch = "arm64"; ext = "" },
    @{ os = "windows"; arch = "amd64"; ext = ".exe" }
)

New-Item -ItemType Directory -Force -Path dist | Out-Null

foreach ($t in $targets) {
    $env:GOOS = $t.os
    $env:GOARCH = $t.arch
    $out = "dist/opsagent-$($t.os)-$($t.arch)$($t.ext)"
    Write-Host "building $out"
    go build -trimpath -o $out ./cmd/opsagent
}

Write-Host "done -> ./dist"
