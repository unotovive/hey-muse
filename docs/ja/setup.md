# セットアップ（日本語）

第 1 部・第 2 部で端末に TECHO5（この派生）を入れ、第 3 部以降で母艦と各機能を設定します。

## 第 1 部 端末の解除（ブートローダー）

自分の Echo Show 5 第 1 世代（checkers）のブートローダーを、公開されている [amonet](https://github.com/R0rt1z2/amonet)（ブランチ `mt8163-checkers`）で解除し、TWRP を入れます。ここで行う操作は端末の起動部分を書き換えるため、**途中で USB を抜いたり電源を切ったりすると起動しなくなります**。

### 1. 道具と USB ドライバー（Windows）

この派生では、端末の操作は **Windows ネイティブ** で行います。WSL に USB を渡す usbipd 経由では、端末が通常 → FASTBOOT → TWRP とモードを変えるたびに USB が付け直され、共有が外れて操作が途中で止まりました。

- Python 3（`py -3 --version` で確認）
- adb / fastboot: `winget install --id Google.PlatformTools -e`
- fastboot 用ドライバー: amonet で解除した端末の fastboot は `VID_0BB4&PID_0C01` として見え、Windows には合うドライバーがありません。[Google USB Driver](https://dl.google.com/android/repository/usb_driver_r13-windows.zip) を展開し、端末を fastboot にした状態でデバイス マネージャーの「Android」→ ドライバーの更新 → コンピューターを参照 → 一覧から選択 → すべてのデバイス → ディスク使用 → `android_winusb.inf` →「Android Bootloader Interface」を選びます（互換性の警告は「はい」）。
- TWRP の adb は Windows 標準のドライバーで動くので、追加は要りません。

usbipd-win を以前に使っていたら、**端末の共有を必ず外しておきます**（管理者 PowerShell で `usbipd unbind --all`、`usbipd attach --auto-attach` が動いていれば止める）。残っていると、端末が fastboot に入った瞬間に WSL に取られ、Windows の fastboot から見えなくなります。

### 2. 解除する

amonet の README（checkers の手順）に従って解除します。電源と音量ボタンの長押しで FASTBOOT に入れる方法が使え、分解やはんだ付けは要りません。終わると端末は TWRP で起動します。

### 3. 解除を確認する

TWRP が起動すること自体が解除の証拠です（解除していない端末では TWRP は起動しません）。初回は「Keep System Read Only?」が出るので、**Swipe to Allow Modifications** をスワイプします。

fastboot でも確かめられます（端末が fastboot のとき）。

```powershell
fastboot getvar product        # product: CHECKERS
fastboot getvar unlock_status  # unlock_status: true
fastboot reboot recovery       # TWRP に戻す
```

### ここで止まったら

- `fastboot devices` が空: 上のドライバーと usbipd の共有を確認します。デバイス マネージャーで端末が「USBIP Shared Device」になっていれば usbipd が掴んでいます。
- 画面が「HACKED FASTBOOT MODE」で止まる: 端末は fastboot で待っているだけです。`fastboot reboot recovery` で TWRP に、`fastboot continue` で通常起動に進めます。

## 第 2 部 TECHO5 の導入

TWRP にいる端末へ、`tools/install-show.py` で LineageOS（ドライバーのためだけに入れ、起動はしない）と TECHO5 を入れます。インストーラはリリースのマニフェストをプロジェクトの鍵で検証してから書き込みます。

### 1. ファイルを Windows に置く

```bash
# WSL から（コピー先は例）
mkdir -p /mnt/c/Users/<ユーザー>/hey-muse/lineage
cp -r tools /mnt/c/Users/<ユーザー>/hey-muse/
cp ~/downloads-untrusted/lineage/lineage-18.1-20260904-UNOFFICIAL-checkers.zip /mnt/c/Users/<ユーザー>/hey-muse/lineage/
cp ~/.ssh/id_rsa.pub /mnt/c/Users/<ユーザー>/hey-muse/
```

LineageOS は **2026-09-04 版**（`lineage-18.1-checkers-v0.7`）が必要です。TECHO5 のカーネルはこの版のカーネルから作られており、別の版のドライバーは読み込めません。

### 2. TWRP を整える

端末の TWRP で、毎回次の 2 つを行います。

- Mount →「Mount system partition read-only」の**チェックを外す**
- Mount →「Disable MTP」を押し、ボタンが「Enable MTP」に変わった状態にする

MTP が有効だと、464MB の zip を送っている最中に USB がリセットされ、adb ごと落ちることがあります（落ちたら TWRP の Reboot → Recovery でやり直します）。

### 3. 事前確認（書き込みなし）

```powershell
$pt = "$env:LOCALAPPDATA\Microsoft\WinGet\Packages\Google.PlatformTools_Microsoft.Winget.Source_8wekyb3d8bbwe\platform-tools"
cd $HOME\hey-muse
py -3 tools\install-show.py --adb "$pt\adb.exe" --fastboot "$pt\fastboot.exe" --serial <adb のシリアル> --name <端末名> --lineage-zip "$HOME\hey-muse\lineage\lineage-18.1-20260904-UNOFFICIAL-checkers.zip" --ssh-key "$HOME\hey-muse\id_rsa.pub" --dry-run
```

`release manifest signed by the project key` と `Dry run: ... nothing written to the unit.` が出れば準備完了です。

### 4. インストール

`--dry-run` を外し、`--wifi <SSID>` を付けて実行します（Wi-Fi のパスワードは対話で入力し、画面にもログにも残りません）。消去の前に 1 回だけ `ERASE` の入力を求められます。2.4GHz の SSID を推奨します。

流れは、パーティションの退避（`backups\<シリアル>\partitions`）→ userdata の初期化 → LineageOS の導入 → rootfs の転送 → fastboot でブートイメージ → USB シリアルで slot store の作成と slot a への展開 → 初回起動の確認、です。最後に `Done. '<端末名>' runs TECHO5 v1.6.30-ja.2 from slot a` と端末の IP が出ます。

途中で止まったときは、**slot store の作成（`== slot store`）より前なら、TWRP に戻して同じコマンドで最初からやり直せます**（退避済みのパーティションは飛ばされ、初期化する領域はもともと消す予定のものです）。

- `waiting for TWRP after formatting userdata` で端末が HACKED FASTBOOT に止まる: 前の fastboot 要求が残っていると 1 回だけ起きます。待ちの間に別の PowerShell で `fastboot reboot recovery` を送れば、そのまま続きます。**`== flash the boot image` の `waiting for fastboot` では送らないでください**（インストーラがわざと fastboot に入れています）。
- `waiting for fastboot` で 90 秒止まる: 第 1 部のドライバーと usbipd の共有を確認します。
- `adb does not see ... in TWRP (state '')`: 端末が TWRP にいません。TWRP に戻してから実行します。

### 5. 初期設定と確認

端末に「Hello … Tap anywhere to begin」が出たら、画面の案内で初期設定をします（タイムゾーンは Tokyo）。**Home Assistant を使わないなら、Home Assistant に追加する選択肢は選びません。** 選ぶと 15 分間、端末は暗号鍵を消して登録を待ち、その間は SSH が起動せず、設定画面の SSH も「Starting...」のままオンにできません（15 分たつと新しい鍵で自動的に閉じ、SSH をオンにできるようになります）。

```bash
ssh root@<端末IP> 'cat /etc/techo5-release; PATH=/usr/local/sbin:$PATH slotctl status'
```

期待する結果: `v1.6.30-ja.2`、`active: a`、slot a が `trial` → 起動から約 5〜8 分で `good`。非対話の SSH では `slotctl` が PATH に無いので、上のように PATH を足します。

## 第 3 部 母艦（Windows で音声認識を常駐させる）

母艦は、Windows 11 の PC です。端末（Echo Show 5）の音声を Whisper で文字にする `tools/echo-asr/pc/recognizer.py` を、Windows のタスクスケジューラのタスク `hey-muse ASR` として常駐させます。WSL は使いません。ログオンすれば自動で起動し、`wsl --shutdown` しても止まりません。Linux や WSL を母艦にする場合は、付録 A を見てください。

### 0. 前提

- Windows 11、Python 3.13（`py -3 --version` で確認）、NVIDIA ドライバー（`nvidia-smi` が動くこと）。
- PC は常時起動にします。タスクは Windows にログオンしたときに起動するので、ログオンした状態にしておきます。
- リポジトリは WSL にあります。スクリプトは Windows から `\\wsl.localhost\<ディストリビューション名>\home\<ユーザー名>\...` のパスで実行します。インストーラが Python ファイル 2 つ（`recognizer.py`、`worker.py`）を Windows 側へコピーするので、導入後は WSL を止めてかまいません。

### 1. 管理者の前処理（ファイアウォールと電源）

管理者として開いた PowerShell で、リポジトリの場所へ移動して実行します。ファイアウォールの受信許可（規則 `muse-asr`、TCP 50024）と、AC 電源時にスタンバイしない設定だけを行います。

```powershell
cd \\wsl.localhost\Ubuntu\home\<ユーザー名>\projects\musekusa\hey-muse
powershell.exe -ExecutionPolicy Bypass -File tools\mothership\windows\setup-mothership.ps1 -WindowsNative
```

期待する結果: 最後に「完了（Windows ネイティブ母艦）」と出ます。

### 2. インストール（通常権限）

WSL で母艦を動かしていた場合は、先に付録 B の手順 1 を行ってください（同じ 50024 番を使うため）。

管理者でない PowerShell で実行します。`-Allow` には、端末の IP アドレスを指定します（複数台ならコンマ区切りで、例 `-Allow 192.0.2.10,192.0.2.11`）。

```powershell
cd \\wsl.localhost\Ubuntu\home\<ユーザー名>\projects\musekusa\hey-muse
powershell.exe -ExecutionPolicy Bypass -File tools\mothership\windows\install-asr.ps1 -Allow 端末のIP
```

他の引数は `-Port`（既定 50024）、`-IdleMinutes`（既定 30）、`-Model`（既定 `large-v3-turbo`）です。

インストーラは、`%LOCALAPPDATA%\hey-muse` に `venv`・`app`・`logs` を作り、faster-whisper と CUDA の DLL を入れ、`recognizer.py` と `worker.py` を `app` にコピーします。続けて `worker.py --check` でモデルを読み込みます（初回はモデルの取得、約 1.6 GB に時間がかかります）。そのあと、タスク `hey-muse ASR`（ログオン時に非表示で起動、実行時間の制限なし、インストール直後から 5 分ごとに見張り、落ちても最長 5 分で起動し直す）を登録して開始し、`127.0.0.1:50024` が応答するまで確認します。旧構成のタスク `hey-muse WSL` があれば、消せるときは消します。

期待する結果: 最後に、タスク名、ログの場所、`--allow`、アイドル解放の分数が出ます。「管理者 PowerShell で実行してください」の案内が出たときは、表示されたコマンドを管理者の PowerShell で実行します。

初回は、タスクが待ち受けを始めたときに Windows Defender ファイアウォールの「アクセスを許可しますか」が出ることがあります。そのときは「許可」を選びます。誤って「キャンセル」を選ぶと、python / pythonw をブロックする規則ができ、規則 `muse-asr` の許可より優先されます。消し方は手順 7 にあります。

### 3. 確認

```powershell
Get-ScheduledTask -TaskName 'hey-muse ASR' | Select-Object State
Test-NetConnection 母艦のLAN_IP -Port 50024
Get-Content $env:LOCALAPPDATA\hey-muse\logs\asr.log -Tail 5
```

期待する結果: `State` が `Running`、`TcpTestSucceeded : True`、ログに `ready`（待ち受け開始）の行が出ます。インストール直後は `ready` の行だけです。`worker:` の行（認識プロセスの出力）は、端末に話しかけたあとに出ます。

### 4. 再起動して確認する

Windows を再起動し、ログオンして 2 分待ってから、手順 3 を繰り返します。さらに、`wsl --shutdown` を実行したあとも同じ結果になることを確認します。

### 5. メモリの振る舞い

- 待機中は、受付のプロセス（`pythonw`）だけが動き、GPU のメモリ（VRAM）は使いません。
- 端末が話しかけられると、認識プロセスがモデルを読み込みます（VRAM 約 2 GB、約 3 秒）。最後の使用から 30 分たつと、認識プロセスは終了し、VRAM は元に戻ります。
- 30 分以上あけたあとの最初の一言は、3〜6 秒遅くなります。まれに、その 1 回だけ Muse 側の認識に切り替わることがあります。
- 時間を変えるときは、`install-asr.ps1 -Allow 端末のIP -IdleMinutes N` を再実行します。

端末側の設定は変わりません。端末が使う認識の宛先は、母艦の LAN アドレス（Windows の IP）の 50024 番で、セットアップページの「Speech to text (host:port)」に入れます（第 8 部の手順 2）。

### 6. 設定を変える・更新する

`install-asr.ps1` を、引数を変えて再実行します（何度実行しても安全です）。`recognizer.py` や `worker.py` を更新したときも、同じく再実行して `app` へコピーし直します。

完全に止めたいときは、`Disable-ScheduledTask -TaskName 'hey-muse ASR'` のあとに `Stop-ScheduledTask -TaskName 'hey-muse ASR'` を実行します。`Stop-ScheduledTask` だけでは、最長 5 分で起動し直します。戻すときは `Enable-ScheduledTask -TaskName 'hey-muse ASR'` と `Start-ScheduledTask -TaskName 'hey-muse ASR'` を実行します。

### 7. うまくいかないとき

- ログ `%LOCALAPPDATA%\hey-muse\logs\asr.log`（5 MB × 3 世代でローテーション）で、`ready` と `worker:` の行を探します。`worker fatal:` の行があれば、その内容を見ます（多くは CUDA か DLL の問題です）。
- モデルの読み込みを手で試すには、`%LOCALAPPDATA%\hey-muse\venv\Scripts\python.exe %LOCALAPPDATA%\hey-muse\app\worker.py --check --device cuda --idle-seconds 0` を実行します。
- インストーラ自体が失敗したとき:
  - 「20 秒待ちましたが … 待ち受けになりません」: 表示されたログの末尾に `startup failed` があれば、その下の例外を見ます（ポートが使用中など）。
  - 「--check が失敗しました」: 上に出た `worker.py` の出力を見ます（CUDA か DLL、モデルの取得の失敗）。
  - 「管理者権限で実行されています」: 管理者でない PowerShell で実行し直します。
- `TcpTestSucceeded : False` のとき: ファイアウォールの規則 `muse-asr`（`Get-NetFirewallRule -Name muse-asr`）と、`-Allow` に端末の IP を指定したかを確認します。
- 端末からだけつながらないとき: 手順 2 の「アクセスを許可しますか」をキャンセルしてできたブロック規則が残っていないか確認します。ブロック規則は許可の規則より優先されます。

  ```powershell
  Get-NetFirewallRule | Where-Object DisplayName -like '*python*' | Where-Object Action -eq Block
  ```

  出てきた規則は、管理者の PowerShell で、上のコマンドの末尾に `| Remove-NetFirewallRule` を付けて実行して消します。
- 旧構成のタスク `hey-muse WSL` が残っていないか確認します（付録 B）。

## 第 4 部 Google カレンダー

待機画面の予定を Google Calendar API（読み取り専用）から取得します。iCal 購読より反映が早く、更新に失敗したときは前回の予定を「更新待ち」として表示します。Google Cloud の設定とブラウザでの同意は、カレンダーの持ち主が自分で行います。

### 1. Google Cloud プロジェクトを作る

1. https://console.cloud.google.com/ を開き、新しいプロジェクトを作ります。名前は自由です（例: `hey-muse`）。
2. 「API とサービス」→「ライブラリ」で「Google Calendar API」を検索し、有効にします。

### 2. OAuth 同意画面とテストユーザー

1. 「API とサービス」→「OAuth 同意画面」を開き、ユーザーの種類は「外部」を選びます。
2. アプリ名とメールアドレスを入れて保存します。公開は不要で、公開ステータスは「テスト」のままにします。
3. 「テストユーザー」に、予定を読むカレンダーの持ち主の Google アカウントを追加します。
4. スコープは、同意時に `calendar.readonly` だけが要求されます。追加の登録は不要です。

注意: 公開ステータスが「テスト」のとき、リフレッシュトークンは 7 日で失効します。失効すると予定が取得できず「更新待ち」になります。長く使うには、同意画面を「本番環境」に公開してください。個人利用では審査は不要で、同意時に「未確認のアプリ」の警告が出ますが、詳細から続行できます。

### 3. OAuth クライアント（デスクトップ）を作る

1. 「API とサービス」→「認証情報」→「認証情報を作成」→「OAuth クライアント ID」を選びます。
2. アプリケーションの種類は「デスクトップ アプリ」にします。
3. 作成後に JSON をダウンロードし、`~/.config/hey-muse/google-client.json` に保存します。権限は 0600 にします。

### 4. 同意してトークンを作る

```
python3 -m venv ~/.venvs/muse-tools
~/.venvs/muse-tools/bin/pip install -r tools/standalone-home/requirements.txt
~/.venvs/muse-tools/bin/python tools/standalone-home/google_auth.py --client ~/.config/hey-muse/google-client.json
```

ブラウザが開くので、手順 2 で登録したアカウントで同意します。成功すると WSL の `~/.config/hey-muse/google-token.json` ができ、使えるカレンダーの `id` と名前が一覧で表示されます。

このトークンは WSL 側にできるだけで、端末にはまだ届きません。`deploy_usb.py` は Windows の Python で動き、Windows 側のホームフォルダーしか見ないため、第 7 部の手順 1 で Windows 側へコピーしてから送ります。

### 5. 使うカレンダーを設定する

表示された一覧から使うカレンダーを選び、`home-local.json` の `calendars` に書きます。API 経路では `id` が必須です。

```json
"calendars": [
  {"id": "xxxxxxxx@group.calendar.google.com", "name": "家族"},
  {"id": "you@gmail.com", "name": "自分"}
]
```

### 6. トークンを端末へ送る

端末へ送る作業は第 7 部で行います。第 7 部の手順 1 で、手順 4 のトークンを WSL から Windows 側へコピーし、手順 2 で `deploy_usb.py config` を実行します。ここでは `deploy_usb.py` を実行しません（WSL の中では動かず、動いたとしても WSL 側のトークンは送られません）。

端末にトークンがあるあいだは Google Calendar API を使い、削除すると従来の `calendar-feeds.txt`（iCal）に戻ります。`google-token.json` と `google-client.json` は秘密情報なので、リポジトリにコミットしないでください。

## 第 5 部 在室検知・ジェスチャー・減光の設定

カメラのある端末では、設定画面の「画面」カードで在室検知、消灯、減光を設定します。

- **在室検知**: 「人感検知」の見出しの下にある「人感検知」スイッチで入り切りします。入れると、カメラが近くの人の動きだけを見ます。映像は保存されません。
- **消灯までの分数**: 「人感検知」を入れると、その下に「近くに人がいなければ画面を消す」の行が出ます。なし・1・2・5・10・15・30・60 分から選びます。「なし」は消灯しません。人が近づくと画面は再び点きます。
- **無操作で暗くする（上流 v1.6.30）**: 同じ「画面」カードの「無操作で暗くする」です。なし・1・2・5・10・15・30 分から選びます。触れず、話しかけず、近くに人もいない状態がこの時間続くと、「最低輝度」の明るさまで暗くなります。「なし」は暗くしません。
- **ジェスチャー**: 「画面」カードの「手で覆って停止」スイッチで入り切りします。入れると、カメラを手のひらで覆ったとき、鳴っているアラームやタイマーを止めます。鳴っていなければ、Muse のターン中の読み上げとターンを中断します。「人感検知」を切っていても使えます。Home Assistant の「Gestures」スイッチでも同じ設定を切り替えられます。

## 第 6 部 地震情報の割り込み表示

Echo Show では、P2P 地震情報（https://www.p2pquake.net/）の WebSocket API v2 を端末自身が受信し、地震・津波・緊急地震速報を待機画面に全画面で割り込み表示できます。間にサーバーは置きません。新しい端末では切れています。

- **入り切り**: 設定画面の「画面」カードの「地震情報」の見出しの下にある「地震情報を表示」スイッチで入り切りします。入れている間だけ `wss://api.p2pquake.net/v2/ws` に 1 本接続します（同じ IP アドレスからは 2 本まで。家の中で 3 台以上入れると、3 台目以降が接続できません）。切断時は 1・2・4…秒、最大 60 秒の間隔で再接続します。
- **表示するもの**:
  - 地震情報（コード 551）: 最大震度が設定した震度以上か、設定した都道府県で揺れを観測したとき、90 秒表示します。既定は震度 3 以上です。
  - 津波予報（コード 552）: 常に 90 秒表示します。
  - 緊急地震速報（警報）（コード 556）: 常に 60 秒、「※参考情報」「気象庁の警報そのものではありません」と添えて表示します。取り消しが届くと消えます。
  - そのほか（561 の地震感知情報など）は表示しません。同じ情報（同じ ID）は 24 時間、二度は表示しません。
- **閉じ方**: 画面のどこかをタップすると閉じます。触らなくても表示時間が過ぎれば元の画面に戻ります。鳴動中のアラームやタイマー、通話の画面の下に出ます。夜間に画面が消えているときは点灯しません。
- **音**: 既定では鳴りません。
- **細かい設定**: 震度のしきい値、都道府県、音は画面からは変えられません。`state.json`（Show 5 では `/data/misc/techo5/state.json`）の `quake` を編集し、echod を再起動します。震度は P2P 地震情報の値（30 = 震度 3、40 = 4、45 = 5 弱、50 = 5 強、55 = 6 弱、60 = 6 強、70 = 7）で書きます。

  ```json
  "quake": {"on": true, "min_scale": 40, "prefs": ["千葉県", "東京都"], "chime": true}
  ```

- **表示の確認**: 端末の USB コンソールか SSH で次を実行すると、動いている echod が数秒以内に見本の情報を表示します（スイッチが切れていても表示します）。

  ```sh
  techo5 tools quake-test --code 551   # 556 は緊急地震速報、552 は津波予報
  ```

> **免責**: 表示する内容は P2P 地震情報が中継する気象庁の情報で、気象庁の警報そのものではありません。受信の遅れや欠落があり得ます。身を守る判断には、テレビ・ラジオ・携帯電話の緊急速報など公式の手段を使ってください。

## 第 7 部 端末設定の配置

端末に置く設定は、Windows 側で用意し、USB 経由でまとめて送ります。送信に使う `deploy_usb.py` は Windows の Python で動かします（USB のシリアル接続に Windows の API を使うため、WSL の中では動きません）。ここでは Nature Remo と Google カレンダーを使う構成を前提にします。以下の `~` は Windows のホームフォルダー、`BASE` は `~/Documents/muse_on_echo` のことです。

### 0. 端末を `v1.6.30-ja.2` にする

端末が新しいリリースで動いていることを先に確かめます。方法は 2 つあります。

- 設定画面から: 端末の設定画面で更新を確認し、`v1.6.30-ja.2` を入れます。リリースが公開（ドラフト解除）されていることが前提です。
- 母艦から: 端末の IP アドレスを指定し、ルートファイルシステムを作って送ります。

  ```bash
  HOST=<端末IP> tools/linux/deploy-rootfs.sh --version v1.6.30-ja.2 --install --reboot
  ```

  `deploy-rootfs.sh` は `bin/` のヘルパー類（`docs/ja/release.md` 手順 1 で揃えるもの）が前提です。

  `--version` は端末に記録する版名、`--install` は使っていない側のスロットへ入れること、`--reboot` は入れたあとに再起動して新しい側で起動することです。

更新後に確認します。

```bash
ssh root@<端末IP> 'PATH=/usr/local/sbin:$PATH techo5 --version; slotctl status'
```

期待する結果: `v1.6.30-ja.2` と出て、再起動から 5 分後に `slotctl status` が `good` になります。

### 1. 設定ファイルを用意する

`deploy_usb.py config` が読む場所は次のとおりです（実装に合わせています）。

| 母艦側のファイル | 端末での名前 | 必須か | 内容 |
|---|---|---|---|
| `BASE/speech-bridge/home-local.json` | `home-local.json` | 必須 | `backend`、`calendars`、`voice_devices` |
| `BASE/speech-bridge/home-requests.sqlite` | `initial-power.json`（中身を取り出して送る） | 必須 | 電源の最後の向きの表 `power_last` |
| `~/natureremo.env`（`--remo-env ファイル` で変更可） | `natureremo.env` | あるときだけ | `NATURE_REMO_TOKEN=…` |
| `~/.config/hey-muse/google-token.json`（Windows 側） | `google-token.json` | あるときだけ | 第 4 部で作ったトークン。WSL から Windows 側へコピーが必要（下記） |
| `~/switchbot_token.env`、`BASE/calendar-feeds.txt` | `switchbot.env`、`calendar-feeds.txt` | あるときだけ | SwitchBot の資格情報、iCal の購読 URL |

**第 4 部のトークンを Windows 側へコピーします。** 第 4 部は WSL で行うため、`google-token.json` は WSL のホームにできています。`deploy_usb.py` は Windows のホームだけを見るので、Windows の PowerShell で次のようにコピーします（`<ユーザー>` は WSL のユーザー名、ディストリビューション名が `Ubuntu` でなければ置き換えます）。

```powershell
New-Item -ItemType Directory -Force "$env:USERPROFILE\.config\hey-muse" | Out-Null
Copy-Item "\\wsl$\Ubuntu\home\<ユーザー>\.config\hey-muse\google-token.json" "$env:USERPROFILE\.config\hey-muse\google-token.json"
```

コピーしないと、`deploy_usb.py` は黙ってトークンを送らず、予定は iCal（`calendar-feeds.txt`）に戻ります。第 4 部以外で WSL に作ったファイルも、この表の Windows 側の場所に無ければ送られません。送る前に、`deploy_usb.py` が拾うファイルを Windows の PowerShell で確認します。`True` になったものだけが送られ、必須の 2 つが `False` なら止まります。

```powershell
$base = "$env:USERPROFILE\Documents\muse_on_echo"
"$base\speech-bridge\home-local.json",
"$base\speech-bridge\home-requests.sqlite",
"$env:USERPROFILE\natureremo.env",
"$env:USERPROFILE\switchbot_token.env",
"$base\calendar-feeds.txt",
"$env:USERPROFILE\.config\hey-muse\google-token.json" | ForEach-Object { "{0}  {1}" -f (Test-Path $_), $_ }
```

さらに、`deploy_usb.py` は `BASE/speech-bridge/` にある `echo_home.py` と `hall_light.py`、`BASE/assets/hey-muse/tools/` にある `techo5lib` を読み込みます。`BASE` の下にこの構成がなければ、`ModuleNotFoundError` で止まります。`tools/mothership/` の `echo_home.py` と `hall_light.py` を `BASE/speech-bridge/` に置いてください。

`home-requests.sqlite` は、旧版の電源操作の記録で、`echo_home.py` が最初の電源操作のときに作ります。Nature Remo だけの新しい構成では、まだ無いので、空の表を手で作ります（Python の標準の sqlite3 を使います）。

```text
python -c "import sqlite3; c=sqlite3.connect(r'BASE/speech-bridge/home-requests.sqlite'); c.execute('create table if not exists power_last (device text primary key, direction text)'); c.commit()"
```

（`BASE` は実際のパスに置き換えます。）空の表のままなら、端末へ送られる `initial-power.json` は `{}` になります。

`home-local.json` は `tools/mothership/home-local.example.json` を元に、上の場所に作ります。Nature Remo を使うときの形は次のとおりです。`key` は SwitchBot 用なので空のままにします。`calendars` の `id` は第 4 部の手順 5 で表示されたものです（下はダミー値）。

```json
{
  "backend": "nature_remo",
  "key": "",
  "calendars": [{"id": "xxxxxxxx@group.calendar.google.com", "name": "家族"}],
  "voice_devices": {}
}
```

Remo の機種が決まったら、`voice_devices` に Remo アプリの機器名と種類（照明は `LIGHT`、エアコンは `AC`）を書き、`natureremo.env` にトークンを入れて、もう一度手順 2 を実行します。`natureremo.env` を別の場所に置くときは `--remo-env ファイル` で指定します。書き方は `tools/standalone-home/README.md` の「Nature Remo を使う」にあります。

### 2. 端末へ送る

端末を USB でつなぎ、シリアル番号を指定して送ります。

```text
python tools/standalone-home/deploy_usb.py config --serial SERIAL
```

実行は Windows の Python で行います。`natureremo.env`、`google-token.json` などの「あるときだけ」のファイルは、存在しなければ黙って送られません。送り忘れに注意してください。端末側の置き場所は `/data/misc/techo5/standalone-home/` です。

### 3. 有効にして確認する

端末の USB コンソールで次を実行します。

```sh
touch /data/misc/techo5/standalone-home/enabled
techo5 tools home-check
ls -l /data/misc/techo5/standalone-home/
```

期待する結果は次のとおりです。

- `Backend: nature_remo` と出ます。
- `Calendar refreshed: N events` と出ます（N は今から 8 日後までの予定の数で、上限は 24 件）。
- Remo のトークンが空なら、家電の検査はエラーで止まります。これは想定どおりで、その行までが出れば足ります。
- 一覧に `enabled`、`home-local.json`、`natureremo.env`、`google-token.json` が見えます。

`natureremo.env`、`google-token.json`、`home-local.json` の `key` は秘密情報です。リポジトリにコミットしないでください。

音声で操作できる家電の言い方は次のとおりです（端末内で解釈し、母艦へ送ります）。

- 照明: 「廊下の電気をつけて」「廊下の電気を消して」
- エアコンの運転: 「冷房にして」「暖房にして」「エアコンの冷房をつけて」「冷房を消して」「暖房を止めて」
- エアコンの温度: 「エアコンを26度にして」（16〜30 度の 2 桁だけ。「26度にして」だけでは設定されません）

冷房・暖房・温度の指定は端末の経路だけで使えます。母艦の経路（`hall_light.py`）は照明のオン・オフだけを受け付け、冷房・暖房・温度は受け付けません。

### 4. 在室検知・地震情報・手で覆う停止

これらは端末の設定画面の「画面」カードで入り切りします。内容は第 5 部と第 6 部を見てください。施主向けの目安は次のとおりです。

- 画面の言語: 新しい端末は「すべての言語」になっていて、表示は英語です。設定画面の「General」カードにある「Screen language」で「日本語」を選ぶと、設定画面・日付・天気が日本語になり、画面が聞き取る言葉も日本語になります。

- 地震情報: 新しい端末では切れています。「地震情報を表示」を入れます。既定は震度 3 以上（`min_scale` 30）です。都道府県と音は `state.json` の `quake` で設定します。
- 人感検知（在室検知）: 「人感検知」を入れ、消灯までの分数を選びます。就寝中に点かないよう、消灯を 5 分程度にするのが目安です。
- 手で覆って停止: 「手で覆って停止」を入れると、アラームやタイマーをカメラへ手をかざして止められます。Home Assistant を使っている場合に限り、「Gestures」スイッチでも切り替えられます。使わなくても支障はありません。

## 第 8 部 Muse の契約とペアリング

端末を Muse（Meta の AI エージェント）に答えさせる手順です。契約、トークン発行、ペアリングは施主本人のアカウントで行います。詳しい背景と注意は英語の [docs/muse.md](../muse.md) にあります。

> Meta や Amazon の製品ではありません。利用は Meta の [Gadget SDK Terms](https://gadgets.muse.ai/sdk-terms) に従います。SDK トークンを作るときに同意する内容なので、先に読んでください。

### 1. 契約と SDK トークン

1. Muse を契約し、スマホに Muse アプリを入れます。
2. https://gadgets.muse.ai/settings/sdk-tokens（Account → SDK tokens）で、自分の SDK トークンを作ります。`mgst_` で始まる文字列です。
3. トークンは個人用です。公開せず、イメージやリリースに入れず、他の家庭と共有しません。1 つのトークンで最大 50 台まで使えます。

### 2. 端末の設定画面で入力する

1. 端末の設定画面から「セットアップページ」（設定 → プライバシー → セットアップページ）を開き、「Sound & Voice」へ進みます。
2. 「Voice assistant」の「Answered by」を「Muse, paired with the Muse app」にします。
3. 「Muse SDK token」にトークンを貼り付けます。
4. 「Speaking voice」を選びます。キー不要の内蔵音声は米国英語のみです。日本語の答えを聞くには、クラウド音声（「Speech key」が必要）を選びます。
5. 「Speech to text (host:port)」に、第 3 部の母艦の宛先 `母艦のLAN_IP:50024` を入れます。空のままだと母艦は使われず、毎回 Muse 側で認識します。
6. 「Save」を押します。トークンが空だと保存を断られます。

### 3. Muse アプリからペアリングする

1. セットアップページの「Muse pairing」で「Pair with the Muse app」を押します。端末が 10 分間 Bluetooth に現れ、`MuseGadgetA1B2C3` のような名前が表示されます。
2. スマホの Muse アプリで「Settings → Devices」を開き、「Developer mode」を入れます。
3. 右上の「+」（Add Device）を押し、手順 1 で表示された `MuseGadget` の名前を選びます。
4. 「コミュニティ製デバイス」という警告が出ます。自分の端末なら続けます。
5. Wi-Fi を聞かれたら、表示される 1 つのネットワークを選びます。端末はすでにつながっているので、パスワードは聞かれません。

パネルが「Paired as」とあなたの Muse ユーザー名、そして「connected to Muse」と表示すれば完了です。ペアリングは再起動後も保たれます。10 分以内に終わらなければ、パネルの「Stop pairing」で止めて、やり直します。信頼できるネットワークの上で行ってください。

### 4. 声の扱いを家族に伝える

ウェイクワードのあと、話した内容は音声ノートとしてあなたの Muse アカウントに保存・文字起こしされます。来客の声も同じです。ウェイクワードの検出は端末内で行われ、検出前の音は外へ出ません。

### 5. 推奨する Muse への指示文

声で聞くので、長い答えは向きません。Muse アプリの設定（Muse への指示、カスタム指示など）に、次のような文を入れておくと、短く日本語で返ります。

```
結論を先に、2〜3 文で、日本語で答えてください。箇条書きや記号は使わず、読み上げて自然な文にしてください。
```

画面操作や電源・音量・アラームなど、端末でできることは Muse が自分で呼び出します。家電の操作は、Nature Remo の定型句（「リビングの電気を消して」など）が先に処理します。

## 付録 A Linux / WSL を母艦にする場合

Linux や WSL2 を母艦にするときの手順です。詳しくは [tools/mothership/README.md](../../tools/mothership/README.md) を見てください。

1. Windows の WSL を使うときは、管理者 PowerShell で `setup-mothership.ps1`（`-WindowsNative` なし）を実行します。`.wslconfig` に `networkingMode=mirrored`（WSL が Windows と同じ LAN の IP を持つ設定）を入れ（元のファイルは `.wslconfig.bak`）、ファイアウォール、AC 電源のスタンバイ無効、タスク `hey-muse WSL`（ログオン時に WSL を起こしたままにする）を設定します。そのあと `wsl --shutdown` で WSL を開き直し、`ip -4 addr show` で Windows と同じ LAN のアドレスが出ることを確認します。
2. 仮想環境 `~/.venvs/muse-asr` を先に作ります（[tools/echo-asr/README.md](../../tools/echo-asr/README.md) の「Run Whisper on a PC」）。WSL の中で `bash tools/mothership/install.sh` を実行すると、systemd のサービス `muse-asr` が入ります（`/etc/wsl.conf` に `systemd=true` が必要）。
3. 初回にできる `~/.config/hey-muse/asr.env` の `ASR_ARGS` で、`--allow` を端末の IP に直し、`sudo systemctl restart muse-asr` を実行します。
4. 確認は `systemctl is-active muse-asr`（`active`）と、別の機器からの `Test-NetConnection 母艦のLAN_IP -Port 50024` です。

## 付録 B WSL の母艦からの移行

WSL の母艦（`muse-asr` と `hey-muse WSL`）から、第 3 部の Windows ネイティブ版へ移すときの手順です。mirrored の WSL で `muse-asr` が動いていると、WSL が Windows と同じ 50024 番を使うため、先に WSL 側を止めます。

1. WSL の中で、旧サービスを止めます。

   ```bash
   sudo systemctl disable --now muse-asr
   ```

2. 第 3 部の手順 1・2 を行います。
3. 第 3 部の手順 3 で、動作を確認します。
4. 任意です。`%USERPROFILE%\.wslconfig` の `networkingMode=mirrored` を消し、PowerShell で `wsl --shutdown` を実行します。
5. タスク `hey-muse WSL` が残っていれば消します（インストーラが消せなかったときだけ必要です）。管理者の PowerShell で実行します。

   ```powershell
   Unregister-ScheduledTask -TaskName 'hey-muse WSL' -Confirm:$false
   ```
6. 第 3 部の手順 4 で、再起動と `wsl --shutdown` のあとの動作を確認します。
