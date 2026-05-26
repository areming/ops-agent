# Install ops locally on Windows: copy the binary onto PATH and prepare
# ssh-agent so SSH (and thus enroll/connect) work without re-entering the key
# passphrase. Run from the repo root after ./build.ps1. Re-runnable.
$ErrorActionPreference = "Stop"

$src = Join-Path $PSScriptRoot "dist\ops-windows-amd64.exe"
if (-not (Test-Path $src)) {
    Write-Error "未找到 $src —— 请先运行 ./build.ps1"
}

# 1) 拷贝到一个用户级目录
$destDir = Join-Path $env:LOCALAPPDATA "ops"
New-Item -ItemType Directory -Force -Path $destDir | Out-Null
Copy-Item $src (Join-Path $destDir "ops.exe") -Force
Write-Host "✓ 已安装 ops.exe -> $destDir"

# 2) 加入用户 PATH（幂等）
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ([string]::IsNullOrEmpty($userPath)) {
    [Environment]::SetEnvironmentVariable("Path", $destDir, "User")
    Write-Host "✓ 已把 $destDir 设为用户 PATH（新开终端生效）"
} elseif ($userPath -notlike "*$destDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$userPath;$destDir", "User")
    Write-Host "✓ 已把 $destDir 加入用户 PATH（新开终端生效）"
} else {
    Write-Host "* PATH 已包含 $destDir"
}

# 3) ssh-agent：启用并启动（启用 Disabled 服务需要管理员）
$svc = Get-Service ssh-agent -ErrorAction SilentlyContinue
if ($null -eq $svc) {
    Write-Warning "未找到 ssh-agent 服务，确认已安装 Windows OpenSSH 客户端（设置 > 应用 > 可选功能）"
} elseif ($svc.Status -ne "Running") {
    try {
        if ($svc.StartType -eq "Disabled") {
            Set-Service ssh-agent -StartupType Automatic
        }
        Start-Service ssh-agent
        Write-Host "✓ ssh-agent 已启动（开机自启）"
    } catch {
        Write-Warning "无法启动 ssh-agent（多半需要管理员）。请用【管理员】PowerShell 跑：`n    Set-Service ssh-agent -StartupType Automatic; Start-Service ssh-agent"
    }
} else {
    Write-Host "* ssh-agent 已在运行"
}

Write-Host ""
Write-Host "下一步："
Write-Host "  1) 把私钥加载进 agent（只需一次，输一次 passphrase）："
Write-Host "       ssh-add $env:USERPROFILE\.ssh\id_ed25519"
Write-Host "  2) 新开一个终端，直接用（无需 .\dist\... 前缀）："
Write-Host "       ops setup"
