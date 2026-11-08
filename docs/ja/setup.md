# セットアップ（日本語）

この文書の他の部は、別のタスクで追加されます。ここには第 4 部「Google カレンダー」を置きます。

## 第 3 部 母艦（WSL の音声認識を常駐させる）

母艦は、WSL2（ディストリビューション名 `Ubuntu`）を動かす Windows の PC です。端末（Echo Show 5）の音声を Whisper で文字にする `tools/echo-asr/pc/recognizer.py` を、systemd のサービス `muse-asr` として常駐させます。Windows を再起動しても、ログオンすれば自動で戻ります。仮想環境 `~/.venvs/muse-asr` は先に作ってあることが前提です（[tools/echo-asr/README.md](../../tools/echo-asr/README.md) の「Run Whisper on a PC」）。

次の作業のうち、手順 1・3・4 は Windows 側の管理者権限や WSL の再起動を伴います。作業中の WSL が止まるので、他の作業を保存してから行ってください。

### 1. WSL を mirrored にし、ファイアウォールを開ける

mirrored にすると、WSL が Windows と同じ LAN の IP アドレスを持ち、端末から直接届きます。既存の `.wslconfig` には `[wsl2]` 節があることが多く、節を二重にすると壊れます。次のスクリプトは、既存の節に `networkingMode=mirrored` を入れ、元のファイルを `.wslconfig.bak` に残します。ファイアウォールの受信許可（TCP 50024）と、AC 電源時にスタンバイしない設定も同じ実行で入ります。手順 3 のタスク登録も含みます。

管理者として開いた PowerShell で、リポジトリの場所へ移動して実行します（`\\wsl$\Ubuntu\home\ユーザー名\projects\musekusa\hey-muse` など）。

```powershell
cd \\wsl$\Ubuntu\home\ユーザー名\projects\musekusa\hey-muse
powershell.exe -ExecutionPolicy Bypass -File tools\mothership\windows\setup-mothership.ps1
```

期待する結果: 画面に `.wslconfig` の中身が出て、`[wsl2]` が 1 つだけで `networkingMode=mirrored` を含みます。最後に「完了」と出ます。

その後、PowerShell で WSL を止め、開き直します。

```powershell
wsl --shutdown
wsl -d Ubuntu
```

WSL の中で確認します。

```bash
ip -4 addr show | grep -v 127.0.0.1 | grep inet
```

期待する結果: Windows と同じ LAN のアドレス（例 `192.168.x.x`）が出ます。NAT のときの `172.17.x.x` ではありません。

### 2. systemd ユニットを入れる

`/etc/wsl.conf` の `[boot]` に `systemd=true` が必要です（`install.sh` が確認し、なければ止まります）。WSL の中で `install.sh` を実行します。`sudo` のパスワードを 1 回聞かれます。何度実行しても安全です。ユニットの `User=` とリポジトリの場所は、実行したユーザーと `install.sh` 自身の場所に自動で置き換わります。clone 先は下の例と違っていてもかまいません。

```bash
cd ~/projects/musekusa/hey-muse
bash tools/mothership/install.sh
```

初回は `~/.config/hey-muse/asr.env` の雛形ができます。`ASR_ARGS` の `--allow` を、端末の IP アドレスに直してから再起動します（下は例のダミー値です）。

```bash
# ~/.config/hey-muse/asr.env
ASR_ARGS="--allow 192.0.2.10"
LD_LIBRARY_PATH=/home/ユーザー名/.venvs/muse-asr/lib/python3.12/site-packages/nvidia/cublas/lib:/home/ユーザー名/.venvs/muse-asr/lib/python3.12/site-packages/nvidia/cudnn/lib
```

```bash
sudo systemctl restart muse-asr
systemctl is-active muse-asr && journalctl -u muse-asr -n 5 --no-pager
```

期待する結果: `active` と出て、ログに待ち受け開始とモデル読み込みの表示があります。初回はモデルのダウンロード（約 1.6 GB）に時間がかかります。

ブリッジ（`speech_bridge.py`）も常駐させる場合は、`bash tools/mothership/install.sh --with-bridge` を使います。事前に `~/.venvs/muse-bridge` を作り、`~/.config/hey-muse/bridge.env` に `SPEECH_BRIDGE_TOKEN=ランダムなトークン` と `BRIDGE_ARGS="--allowed-network 192.168.1.0/24"` を書きます（値は自分の LAN に合わせます）。`bridge.env` がなければ起動しません。

### 3. Windows ログオン時に WSL を起こしたままにする

手順 1 のスクリプトが、タスク `hey-muse WSL` を登録しています。ログオン時に `wsl.exe -d Ubuntu --exec /bin/sleep infinity` を 1 本起動し、WSL が idle で終了するのを防ぎます。タスクは実行時間の制限なし、バッテリー駆動でも開始・継続する設定です。ディストリビューション名は `wsl -l -q` で確認し、`Ubuntu` がなければ止まります（`docker-desktop` は使いません）。別の名前なら `-Distro 名前` を付けて実行します。

```powershell
schtasks /Query /TN "hey-muse WSL" /V /FO LIST | Select-String "Task To Run","Stop the task if it runs longer than","Power Management"
```

期待する結果は次のとおりです。

- `Task To Run:` の行に `wsl.exe -d Ubuntu --exec /bin/sleep infinity` が出ます。
- `Stop the task if it runs longer than:` が `Disabled` です。
- `Power Management` に電源条件の停止がありません（`Stop On Battery Mode: No Start On Batteries`）。

### 4. 再起動して確認する

Windows を再起動し、ログオンして 2 分待ちます。WSL の中で次を実行します。

```bash
systemctl is-active muse-asr
```

期待する結果: `active`。

別の LAN 機器から届くかも確認します。端末が使えなければ、別の Windows PC の PowerShell か、スマホの TCP テストアプリを使います。

```powershell
Test-NetConnection 母艦のLAN_IP -Port 50024
```

期待する結果: `TcpTestSucceeded : True`。

うまくいかないとき:

- `TcpTestSucceeded : False` のとき: ファイアウォールの規則 `muse-asr` が 2 種類（`Get-NetFirewallRule -Name muse-asr` と `Get-NetFirewallHyperVRule -Name muse-asr`）あるか確認します。
- `ip addr` に 172.17.x が出るとき: `.wslconfig` の `networkingMode=mirrored` が `[wsl2]` の下にあるか、`wsl --shutdown` を実行したかを確認します。
- `active` にならないとき: `journalctl -u muse-asr -n 30 --no-pager` を見ます。`--allow` の書き方と `LD_LIBRARY_PATH` が主な原因です。

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

ブラウザが開くので、手順 2 で登録したアカウントで同意します。成功すると `~/.config/hey-muse/google-token.json` ができ、使えるカレンダーの `id` と名前が一覧で表示されます。

### 5. 使うカレンダーを設定する

表示された一覧から使うカレンダーを選び、`home-local.json` の `calendars` に書きます。API 経路では `id` が必須です。

```json
"calendars": [
  {"id": "xxxxxxxx@group.calendar.google.com", "name": "家族"},
  {"id": "you@gmail.com", "name": "自分"}
]
```

### 6. トークンを端末へ送る

```
python3 tools/standalone-home/deploy_usb.py config --serial SERIAL
```

`~/.config/hey-muse/google-token.json` が `google-token.json` として端末へ送られます。端末にこのファイルがあるあいだは API を使い、削除すると従来の `calendar-feeds.txt`（iCal）に戻ります。`google-token.json` と `google-client.json` は秘密情報なので、リポジトリにコミットしないでください。

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

端末に置く設定は、母艦（または Windows）の `~/.config/hey-muse/` で用意し、USB 経由でまとめて送ります。ここでは Nature Remo と Google カレンダーを使う構成を前提にします。

> 番号について: 計画では第 5・6 部としていましたが、その番号は在室検知と地震情報の節がすでに使っているため、この端末設定と次の Muse の節は第 7・8 部にしました。在室検知は第 5 部、地震情報は第 6 部です。

### 1. 設定ファイルを用意する

| ファイル（母艦） | 端末での名前 | 内容 |
|---|---|---|
| `home-local.json` | `home-local.json` | `backend`、`calendars`、`voice_devices` |
| `natureremo.env` | `natureremo.env` | `NATURE_REMO_TOKEN=…`（機種が決まるまで空でも可） |
| `google-token.json` | `google-token.json` | 第 4 部で作ったトークン |

`home-local.json` は `tools/mothership/home-local.example.json` を元に作ります。Nature Remo を使うときの形は次のとおりです。`key` は SwitchBot 用なので空のままにします。`calendars` の `id` は第 4 部の手順 5 で表示されたものです（下はダミー値）。

```json
{
  "backend": "nature_remo",
  "key": "",
  "calendars": [{"id": "xxxxxxxx@group.calendar.google.com", "name": "家族"}],
  "voice_devices": {}
}
```

Remo の機種が決まったら、`voice_devices` に Remo アプリの機器名と種類（照明は `LIGHT`、エアコンは `AC`）を書き、`natureremo.env` にトークンを入れて、もう一度手順 2 を実行します。書き方は `tools/standalone-home/README.md` の「Nature Remo を使う」にあります。

### 2. 端末へ送る

端末を USB でつなぎ、シリアル番号を指定して送ります。

```
python3 tools/standalone-home/deploy_usb.py config --serial SERIAL
```

`natureremo.env` と `google-token.json` は、母艦に存在するときだけ送られます。端末側の置き場所は `/data/misc/techo5/standalone-home/` です。

### 3. 有効にして確認する

端末の USB コンソールで次を実行します。

```sh
touch /data/misc/techo5/standalone-home/enabled
techo5 tools home-check
ls -l /data/misc/techo5/standalone-home/
```

期待する結果は次のとおりです。

- `Backend: nature_remo` と出ます。
- `Calendar refreshed: N events` と出ます（N は今後 7 日間の予定の数）。
- Remo のトークンが空なら、家電の検査はエラーで止まります。これは想定どおりで、その行までが出れば足ります。
- 一覧に `enabled`、`home-local.json`、`natureremo.env`、`google-token.json` が見えます。

`natureremo.env`、`google-token.json`、`home-local.json` の `key` は秘密情報です。リポジトリにコミットしないでください。

### 4. 在室検知・地震情報・手で覆う停止

これらは端末の設定画面の「画面」カードで入り切りします。内容は第 5 部と第 6 部を見てください。施主向けの目安は次のとおりです。

- 地震情報: 新しい端末では切れています。「地震情報を表示」を入れます。既定は震度 3 以上（`min_scale` 30）です。都道府県と音は `state.json` の `quake` で設定します。
- 在室検知: 「人感検知」を入れ、消灯までの分数を選びます。就寝中に点かないよう、消灯を 5 分程度にするのが目安です。
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
4. 「Speaking voice」を選びます。キー不要の内蔵音声は米国英語のみです。日本語の答えを聞くには、クラウド音声（「Speech key」が必要）を選びます。音声の認識は第 3 部の母艦を使います。
5. 「Save」を押します。トークンが空だと保存を断られます。

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
