# Install ops locally on Windows: copy the binary onto PATH and prepare
# ssh-agent so SSH (and thus enroll/connect) work without re-entering the key
# passphrase. Run from the repo root after ./build.ps1. Re-runnable.
# ASCII-only on purpose: a .ps1 with non-ASCII text breaks under Windows
# PowerShell 5.1 when the console code page is not UTF-8.
$ErrorActionPreference = "Stop"

$src = Join-Path $PSScriptRoot "dist\ops-windows-amd64.exe"
if (-not (Test-Path $src)) {
    Write-Error "$src not found -- run ./build.ps1 first"
}

# 1) Copy into a per-user directory
$destDir = Join-Path $env:LOCALAPPDATA "ops"
New-Item -ItemType Directory -Force -Path $destDir | Out-Null
Copy-Item $src (Join-Path $destDir "ops.exe") -Force
Write-Host "[ok] installed ops.exe -> $destDir"

# 2) Add to the user PATH (idempotent)
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ([string]::IsNullOrEmpty($userPath)) {
    [Environment]::SetEnvironmentVariable("Path", $destDir, "User")
    Write-Host "[ok] set $destDir as user PATH (effective in a new terminal)"
} elseif ($userPath -notlike "*$destDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$userPath;$destDir", "User")
    Write-Host "[ok] added $destDir to user PATH (effective in a new terminal)"
} else {
    Write-Host "[*] PATH already contains $destDir"
}

# 3) ssh-agent: enable and start (enabling a Disabled service needs admin)
$svc = Get-Service ssh-agent -ErrorAction SilentlyContinue
if ($null -eq $svc) {
    Write-Warning "ssh-agent service not found; ensure the Windows OpenSSH Client is installed (Settings > Apps > Optional Features)"
} elseif ($svc.Status -ne "Running") {
    try {
        if ($svc.StartType -eq "Disabled") {
            Set-Service ssh-agent -StartupType Automatic
        }
        Start-Service ssh-agent
        Write-Host "[ok] ssh-agent started (auto-start on boot)"
    } catch {
        Write-Warning "could not start ssh-agent (usually needs admin). In an ADMIN PowerShell run:`n    Set-Service ssh-agent -StartupType Automatic; Start-Service ssh-agent"
    }
} else {
    Write-Host "[*] ssh-agent already running"
}

Write-Host ""
Write-Host "Next:"
Write-Host "  1) Load your private key into the agent (once; one passphrase prompt):"
Write-Host "       ssh-add `$env:USERPROFILE\.ssh\id_ed25519"
Write-Host "  2) Open a new terminal and use it directly (no .\dist\... prefix):"
Write-Host "       ops setup"
