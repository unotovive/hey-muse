# 母艦（Windows）の設定。管理者権限の PowerShell で実行する。何度実行しても安全。
#   powershell.exe -ExecutionPolicy Bypass -File tools\mothership\windows\setup-mothership.ps1
# 1) .wslconfig の [wsl2] 節に networkingMode=mirrored を入れる（節は二重にしない）
# 2) 受信ファイアウォール（TCP 50024）を開け、AC 電源時のスタンバイを無効にする
# 3) ログオン時に WSL を起こして起こしたままにするタスクを登録する（実行時間無制限、バッテリーでも止めない）
# 実行後に WSL の再起動（wsl --shutdown）が必要。このスクリプトは再起動しない。
param(
    [string]$Distro = 'Ubuntu',
    [int]$Port = 50024
)
$ErrorActionPreference = 'Stop'

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw '管理者として実行してください（スタートメニューで PowerShell を右クリック、管理者として実行）。'
}

# --- ディストリビューション名の確認 ---
$distros = @(wsl.exe -l -q | ForEach-Object { ($_ -replace "`0", '').Trim() } | Where-Object { $_ })
if ($distros -notcontains $Distro) {
    throw "WSL に '$Distro' がありません。見つかった名前: $($distros -join ', ')。-Distro で指定してください（docker-desktop は不可）。"
}

# --- 1) .wslconfig ---
$cfg = Join-Path $env:USERPROFILE '.wslconfig'
$lines = New-Object System.Collections.Generic.List[string]
if (Test-Path $cfg) { Get-Content $cfg | ForEach-Object { $lines.Add($_) } }
$sec = -1
for ($i = 0; $i -lt $lines.Count; $i++) {
    if ($lines[$i].Trim() -ieq '[wsl2]') { $sec = $i; break }
}
if ($sec -lt 0) {
    if ($lines.Count -gt 0) { $lines.Add('') }
    $lines.Add('[wsl2]')
    $lines.Add('networkingMode=mirrored')
} else {
    $end = $lines.Count
    for ($i = $sec + 1; $i -lt $lines.Count; $i++) {
        if ($lines[$i].Trim() -match '^\[.+\]$') { $end = $i; break }
    }
    $found = $false
    for ($i = $sec + 1; $i -lt $end; $i++) {
        if ($lines[$i] -match '^\s*networkingMode\s*=') { $lines[$i] = 'networkingMode=mirrored'; $found = $true }
    }
    if (-not $found) { $lines.Insert($sec + 1, 'networkingMode=mirrored') }
}
if (Test-Path $cfg) { Copy-Item $cfg "$cfg.bak" -Force }
Set-Content -Path $cfg -Value $lines -Encoding ASCII
Write-Host "--- $cfg ---"
Get-Content $cfg

# --- 2) ファイアウォールと電源 ---
if (-not (Get-NetFirewallRule -Name 'muse-asr' -ErrorAction SilentlyContinue)) {
    New-NetFirewallRule -Name 'muse-asr' -DisplayName 'muse-asr' -Direction Inbound -Protocol TCP -LocalPort $Port -Action Allow | Out-Null
}
if (-not (Get-NetFirewallHyperVRule -Name 'muse-asr' -ErrorAction SilentlyContinue)) {
    New-NetFirewallHyperVRule -Name 'muse-asr' -DisplayName 'muse-asr' -Direction Inbound `
        -VMCreatorId '{40E0AC32-46A5-438A-A0B2-2B479E8F2E90}' -Protocol TCP -LocalPorts $Port | Out-Null
}
powercfg /change standby-timeout-ac 0

# --- 3) ログオン時に WSL を起こしたままにする ---
# schtasks /Create は実行時間 72 時間の制限とバッテリー時の停止が既定で入るため、Register-ScheduledTask で制限を外す。
$action = New-ScheduledTaskAction -Execute 'wsl.exe' -Argument "-d $Distro --exec /bin/sh -c 'sleep infinity'"
$trigger = New-ScheduledTaskTrigger -AtLogOn
$taskPrincipal = New-ScheduledTaskPrincipal -UserId ([Security.Principal.WindowsIdentity]::GetCurrent().Name) -LogonType Interactive -RunLevel Highest
$settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
Register-ScheduledTask -TaskName 'hey-muse WSL' -Action $action -Trigger $trigger -Principal $taskPrincipal -Settings $settings -Force | Out-Null

Write-Host ''
Write-Host '完了。次に PowerShell で wsl --shutdown を実行し、WSL を開き直してください。'
