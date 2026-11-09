# 母艦（Windows ネイティブ）に音声認識器を入れる。通常権限（非昇格）で実行する。何度実行しても安全。
#   powershell.exe -ExecutionPolicy Bypass -File tools\mothership\windows\install-asr.ps1 -Allow 192.0.2.10
# venv・アプリ・ログは %LOCALAPPDATA%\hey-muse に置き、ログオン時に起動するタスク 'hey-muse ASR' を登録する。
# ファイアウォールの受信規則など管理者権限が要るものは、最後に実行すべきコマンドとして表示する。
param(
    [Parameter(Mandatory)][string[]]$Allow,
    [int]$Port = 50024,
    [int]$IdleMinutes = 30,
    [string]$Model = 'large-v3-turbo'
)
$ErrorActionPreference = 'Stop'

$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = New-Object Security.Principal.WindowsPrincipal($identity)
if ($principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw '管理者権限で実行されています。venv とモデルキャッシュの所有者がずれるため、通常の（非昇格の）PowerShell で実行してください。'
}
$currentUser = $identity.Name

$taskName = 'hey-muse ASR'
$app = Join-Path $env:LOCALAPPDATA 'hey-muse'
$appDir = Join-Path $app 'app'
$logDir = Join-Path $app 'logs'
$venvPython = Join-Path $app 'venv\Scripts\python.exe'
$venvPythonw = Join-Path $app 'venv\Scripts\pythonw.exe'
$logFile = Join-Path $logDir 'asr.log'
$adminTodo = @()

# --- 1) フォルダと venv ---
New-Item -ItemType Directory -Force -Path $appDir, $logDir | Out-Null
if (-not (Test-Path $venvPython)) {
    & py -3 -m venv (Join-Path $app 'venv')
    if ($LASTEXITCODE -ne 0) { throw 'venv の作成に失敗しました（py -3 が使えるか確認してください）。' }
}
& $venvPython -m pip install --upgrade pip
if ($LASTEXITCODE -ne 0) { throw 'pip の更新に失敗しました。' }
& $venvPython -m pip install faster-whisper==1.2.1 nvidia-cublas-cu12 nvidia-cudnn-cu12
if ($LASTEXITCODE -ne 0) { throw 'faster-whisper などのインストールに失敗しました。' }

# --- 2) アプリのコピー（recognizer.py は worker.py を隣から読むので両方必要） ---
$src = Join-Path $PSScriptRoot '..\..\echo-asr\pc'
Copy-Item -Path (Join-Path $src 'recognizer.py') -Destination $appDir -Force
Copy-Item -Path (Join-Path $src 'worker.py') -Destination $appDir -Force

# --- 3) CUDA とモデルの読み込み確認 ---
& $venvPython (Join-Path $appDir 'worker.py') --model $Model --device cuda --idle-seconds 0 --check
if ($LASTEXITCODE -ne 0) {
    throw "--check が失敗しました（終了コード $LASTEXITCODE）。上の出力を確認してください。CUDA が使えないか、cublas/cudnn の DLL が読めない可能性があります。"
}

# --- 4) ログオン時に起動するタスク ---
$argList = @(
    "`"$(Join-Path $appDir 'recognizer.py')`"",
    '--bind', '0.0.0.0',
    '--port', $Port,
    '--device', 'cuda',
    '--idle-unload-minutes', $IdleMinutes,
    '--log-file', "`"$logFile`""
)
foreach ($ip in $Allow) { $argList += @('--allow', $ip) }
$action = New-ScheduledTaskAction -Execute $venvPythonw -Argument ($argList -join ' ') -WorkingDirectory $appDir
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $currentUser
$taskPrincipal = New-ScheduledTaskPrincipal -UserId $currentUser -LogonType Interactive -RunLevel Limited
$settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
    -StartWhenAvailable -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -MultipleInstances IgnoreNew
Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Principal $taskPrincipal -Settings $settings -Force | Out-Null

# --- 5) 旧 WSL タスクの削除（昇格登録のため失敗したら管理者への依頼に回す） ---
if (Get-ScheduledTask -TaskName 'hey-muse WSL' -ErrorAction SilentlyContinue) {
    try {
        Unregister-ScheduledTask -TaskName 'hey-muse WSL' -Confirm:$false
    } catch {
        $adminTodo += "Unregister-ScheduledTask -TaskName 'hey-muse WSL' -Confirm:`$false"
    }
}

# --- 6) 受信規則の有無 ---
if (-not (Get-NetFirewallRule -Name 'muse-asr' -ErrorAction SilentlyContinue)) {
    $adminTodo += "powershell.exe -ExecutionPolicy Bypass -File `"$PSScriptRoot\setup-mothership.ps1`" -WindowsNative -Port $Port"
}

# --- 7) 起動と待ち受けの確認 ---
$existing = Get-ScheduledTask -TaskName $taskName
if ($existing.State -eq 'Running') {
    Stop-ScheduledTask -TaskName $taskName
    Start-Sleep -Seconds 2
}
Start-ScheduledTask -TaskName $taskName
$up = $false
for ($i = 0; $i -lt 20; $i++) {
    Start-Sleep -Seconds 1
    if (Test-NetConnection 127.0.0.1 -Port $Port -InformationLevel Quiet -WarningAction SilentlyContinue) { $up = $true; break }
}
if (-not $up) {
    if (Test-Path $logFile) { Get-Content $logFile -Tail 20 }
    throw "20 秒待ちましたが 127.0.0.1:$Port が待ち受けになりません。上のログを確認してください。"
}

# --- 8) まとめ ---
Write-Host ''
Write-Host "完了。タスク: $taskName"
Write-Host "ログ: $logFile"
Write-Host "--allow: $($Allow -join ', ')"
Write-Host "アイドル解放: $IdleMinutes 分"
if ($adminTodo.Count -gt 0) {
    Write-Host ''
    Write-Host '管理者 PowerShell で実行してください:'
    $adminTodo | ForEach-Object { Write-Host "  $_" }
}
