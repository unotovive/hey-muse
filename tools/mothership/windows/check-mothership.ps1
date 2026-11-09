# 母艦（Windows ネイティブ）の状態をまとめて確かめる。通常権限で実行する。何も変更しない。
#   powershell.exe -ExecutionPolicy Bypass -File tools\mothership\windows\check-mothership.ps1
# Windows の再起動後（受け入れ項目 9）や、端末が答えないときに使う。
param([int]$Port = 50024)
$ErrorActionPreference = 'Continue'
$ok = $true
function Show([bool]$good, [string]$what, [string]$detail = '') {
    $mark = if ($good) { 'OK ' } else { 'NG ' }
    if (-not $good) { $script:ok = $false }
    Write-Host ("[{0}] {1} {2}" -f $mark, $what, $detail)
}

$task = Get-ScheduledTask -TaskName 'hey-muse ASR' -ErrorAction SilentlyContinue
Show ($task -and $task.State -eq 'Running') 'タスク hey-muse ASR' $(if ($task) { $task.State } else { '未登録' })

$listen = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1
Show ([bool]$listen) "受付の待ち受け（$Port）" $(if ($listen) { $listen.LocalAddress } else { '' })

$rule = Get-NetFirewallRule -Name 'muse-asr' -ErrorAction SilentlyContinue
Show ([bool]$rule -and $rule.Enabled -eq 'True') '受信規則 muse-asr'

$log = Join-Path $env:LOCALAPPDATA 'hey-muse\logs\asr.log'
$ready = if (Test-Path $log) { Get-Content $log | Select-String ' ready bind=' | Select-Object -Last 1 } else { $null }
Show ([bool]$ready) '受付の起動ログ' $(if ($ready) { $ready.Line.Substring(0, [Math]::Min(160, $ready.Line.Length)) } else { $log })

try {
    $v = (Invoke-WebRequest -UseBasicParsing http://127.0.0.1:11434/api/version -TimeoutSec 5).Content
    Show $true 'Ollama（答え手）' $v
    $models = & "$env:LOCALAPPDATA\Programs\Ollama\ollama.exe" list 2>$null
    Show ([bool]($models | Select-String 'qwen3:4b-instruct')) 'モデル qwen3:4b-instruct-2507-q4_K_M'
} catch {
    Show $false 'Ollama（答え手）' 'タスクトレイから Ollama を起動してください'
}
Show ([Environment]::GetEnvironmentVariable('OLLAMA_KEEP_ALIVE', 'User') -eq '30m') 'OLLAMA_KEEP_ALIVE=30m'

$vv = @(Get-ChildItem "$env:LOCALAPPDATA\Programs\VOICEVOX\vv-engine\run.exe", "$env:LOCALAPPDATA\Microsoft\WinGet\Packages\HiroshibaKazuyuki.VOICEVOX*\VOICEVOX\vv-engine\run.exe" -ErrorAction SilentlyContinue)
Show ($vv.Count -gt 0) 'VOICEVOX エンジン（読み上げ）' $(if ($vv.Count) { $vv[0].FullName } else { '無ければ Windows の音声で読みます' })

$ip = (Get-NetIPConfiguration | Where-Object { $_.IPv4DefaultGateway } | Select-Object -First 1).IPv4Address.IPAddress
Write-Host ''
Write-Host "この PC の LAN アドレス: $ip（端末の設定は 192.168.0.77 を前提にしています。変わっていたら端末の設定も直します）"
if ($ok) { Write-Host 'すべて OK です。' } else { Write-Host 'NG の行を docs\ja\setup.md 第 3 部の手順 7 と 8 で確認してください。' }
