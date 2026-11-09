# 母艦ブリッジ

Echo の音声出力、カレンダー、室内環境、SwitchBot の定型操作を提供します。カレンダーには接続済みの Codex Calendar コネクターを使います。室内環境の取得と画面のシーン操作は、保存済みのToken/SecretでSwitchBot APIへ直接通信します。音声の定型家電操作は SwitchBot API と、対応する廊下電球の Bluetooth 接続を使います。

## 起動

Python 3.12 以降の仮想環境で `pip install -r tools/mothership/requirements.txt` を実行します。VOICEVOX Engine は同じPCのループバック上の50021番ポートで起動します。

`SPEECH_BRIDGE_TOKEN` に自分で生成したランダムなトークンを設定し、Echo の音声プロバイダーのキーにも同じ値を設定してください。キーを起動引数には渡しません。生成例は `python -c "import secrets; print(secrets.token_urlsafe(32))"` です。

```text
python -I tools/mothership/speech_bridge.py --bind PC_LAN_IP --allowed-network ECHO_LAN_IP/32
```

プレースホルダーは自分のアドレスに置き換えます。省略時はループバックのみです。既定ポートは50022、音声APIは `/v1/audio/speech`、稼働確認は `/health` です。Windowsで必要な場合は `--keep-awake` を指定できます。通常のテストでは自動起動登録や電源設定の変更はしません。

## Windows ネイティブ母艦の受付（50024）

`tools/echo-asr/pc/recognizer.py` は、50024 番の 1 つの口で次の 3 つを受けます（最初の 1 バイトで振り分け）。端末の direct パイプライン（Muse の契約前の暫定）と、Muse の読み上げ先の両方に使えます。手順は [docs/ja/setup.md](../../docs/ja/setup.md) 第 3 部の手順 8 です。

| 要求 | 中身 | 待機中 |
| --- | --- | --- |
| Wyoming `transcribe` | `worker.py`（faster-whisper、GPU）で認識 | 30 分で終了 |
| Wyoming `synthesize`、HTTP `POST /v1/audio/speech`（pcm） | `speaker.py`: VOICEVOX エンジンを必要なときだけ起動。無ければ Windows の SAPI | 30 分で終了。受付と一緒に終わる |
| HTTP `POST /v1/chat/completions`、`GET /v1/models` | `llm_door.py`: 127.0.0.1:11434 の Ollama へ中継（それ以外の Ollama API は通さない） | モデルは Ollama が 30 分で下ろす |

許可するのは `--allow` に入れた端末の IP と 127.0.0.1 だけです。主な引数: `--tts auto|off|run.exe のパス`、`--tts-speaker`（VOICEVOX の話者 ID、既定 30）、`--llm-upstream`（空で中継しない）、`--llm-model`（要求にモデル名が無いときの既定）。

## WSL・systemd・自動起動

母艦が WSL2（`Ubuntu`）の Windows PC のとき、Whisper 認識器 `muse-asr` を systemd で常駐させ、Windows 再起動後も戻します。詳しい手順と確認の期待値は [docs/ja/setup.md](../../docs/ja/setup.md) の第 3 部にあります。ここでは要点だけ書きます。

| ファイル | 役割 |
| --- | --- |
| `windows/setup-mothership.ps1` | 管理者の PowerShell で実行する。`.wslconfig` の `[wsl2]` 節へ `networkingMode=mirrored` を入れ（節は二重にしない）、TCP 50024 の受信許可と AC スタンバイ無効化、ログオン時に WSL を起こすタスク `hey-muse WSL` を登録する（実行時間無制限、バッテリーでも開始・継続。`Register-ScheduledTask` を使う）。何度実行しても安全 |
| `windows/install-asr.ps1` | Windows ネイティブ母艦用。通常権限（非昇格）で実行し、`-Allow 端末のIP` が必須。`%LOCALAPPDATA%\hey-muse` に venv とアプリを入れ、CUDA とモデルの読み込みを確認し、ログオン時起動のタスク `hey-muse ASR`（ログオン時のトリガーに加え、インストール直後から 5 分ごとに見張るトリガーを持ち、落ちても最長 5 分以内に起動し直す）を登録して起動する。管理者が要る残り作業（旧 `hey-muse WSL` タスクの削除、受信規則）は実行すべきコマンドとして最後に表示する |
| `windows/setup-mothership.ps1 -WindowsNative` | 管理者の PowerShell で実行する。WSL 関連は行わず、TCP 50024 の受信許可と AC スタンバイ無効化だけを行う |
| `install.sh` | `systemd/*.service` の `@USER@`・`@HOME@`・`@REPO@`（この clone の場所）を実ユーザーに置き換えて `/etc/systemd/system/` へ入れ、`muse-asr` を有効化する。`~/.config/hey-muse/asr.env` が無ければ雛形を作る。何度実行しても安全。`--with-bridge` で `muse-bridge` も入れる |
| `systemd/muse-asr.service` | `recognizer.py --bind 0.0.0.0 --device auto $ASR_ARGS`。`asr.env` の `ASR_ARGS="--allow 端末のIP"` と `LD_LIBRARY_PATH` を読む |
| `systemd/muse-bridge.service` | 任意。`speech_bridge.py --bind 0.0.0.0 $BRIDGE_ARGS`。`bridge.env` の `SPEECH_BRIDGE_TOKEN` と `BRIDGE_ARGS="--allowed-network LAN/24"` を読む |

Windows ネイティブ母艦の場合の実行の順序（IP は例のダミー値に置き換える）:

```text
# 管理者の PowerShell（受信規則と電源だけ）
powershell.exe -ExecutionPolicy Bypass -File tools\mothership\windows\setup-mothership.ps1 -WindowsNative
# 通常権限の PowerShell
powershell.exe -ExecutionPolicy Bypass -File tools\mothership\windows\install-asr.ps1 -Allow 192.0.2.10
```

タスク `hey-muse ASR` を完全に止めるときは `Disable-ScheduledTask -TaskName 'hey-muse ASR'` のあとに `Stop-ScheduledTask`（Stop だけだと最長 5 分で起動し直す）。戻すときは `Enable-ScheduledTask` と `Start-ScheduledTask`。

Linux/WSL 母艦の場合の実行の順序（IP やトークンは例のダミー値に置き換える）:

```text
# Windows、管理者の PowerShell
powershell.exe -ExecutionPolicy Bypass -File tools\mothership\windows\setup-mothership.ps1
wsl --shutdown
# WSL
bash tools/mothership/install.sh
# ~/.config/hey-muse/asr.env の ASR_ARGS="--allow 192.0.2.10" を端末の IP に直す
sudo systemctl restart muse-asr
systemctl is-active muse-asr
```

systemd は `/etc/wsl.conf` の `[boot]` に `systemd=true` が必要です（`install.sh` が確認します）。

システムユニットでは `%h` が root のホームになるため、ユニットには `%h` ではなく置換用の `@HOME@` を使っています。

## ローカル設定

`home-local.example.json` を同じディレクトリの `home-local.json` にコピーします。このファイルと実行時のSQLiteはGitの対象外です。

| 項目 | 値 |
| --- | --- |
| `key` | ランダムに生成する32バイトの共有鍵の16進表記。Echoと同じ値にする |
| `codex` | Codex実行ファイルのパスまたは `codex`。接続済みアカウントの既存認証を使う |
| `calendars` | `{"id": "カレンダーID", "name": "表示名"}` の配列 |
| `scenes` | 画面から実行を許可するシーンIDの配列 |
| `environment_device` | 任意の環境センサーの機器ID。省略すると取得しない |
| `power_controls` | 下記の電源ボタン定義。空なら表示しない |
| `hall` | `{"name": "自分の電球名", "type": "Color Bulb"}`。Bluetooth対応の電球1台のみ |
| `voice_devices` | 論理対象IDを `{"name": "自分の機器名", "type": "APIが返す機器種別"}` に対応させる辞書 |
| `voice_scenes` | 論理対象IDを `{"toggle": "自分の電源切替シーン名"}` に対応させる辞書 |

`voice_devices` の論理対象IDは `living`、`fan`、`plug`、`curtain`、`ac`、`vacuum` です。`voice_scenes` は `monitor`、`projector` です。未設定・不一致・同名複数は実行を拒否します。シーン名・機器名に既定値はありません。旧実機用設定の `power_controls: true` は辞書形式のボタン定義へ置き換えてください。旧コード内の機器名・シーン名もローカル設定へ移す必要があります。

`power_controls` は例えば `monitor` という論理IDに、`label`（表示名）、`icon`（MDIアイコン）、`toggle`（切替シーン名）、または `on` と `off`（それぞれのシーン名）を設定します。ボタンに表示した方向を要求へ含めるため、表示が古くてもサーバー側で方向を反転しません。APIの受付を表示し、家電の実際の電源状態を断定しません。

SwitchBotのAPI認証は `SWITCHBOT_CREDENTIALS` で指定するローカルファイルから読みます。未指定時はホームディレクトリの `switchbot_token.env` を使います。ファイルには `SWITCHBOT_TOKEN` と `SWITCHBOT_SECRET` を設定します。トークン・秘密鍵はEchoへ転送しません。画面のボタン・温湿度・CO₂・電池残量の取得にもこの認証を使用し、ChatGPT側のSwitchBotコネクター認証には依存しません。シーン操作はローカル許可リストと現在のシーン一覧で対象を確認し、実行結果が不明な場合は自動再送しません。

## Echo側

`/data/misc/techo5/idle-home.json` を用意します。

```json
{
  "URL": "http://PC_LAN_IP:50022/echo-home/v1",
  "Key": "REPLACE_WITH_GENERATED_64_HEX_CHARACTERS",
  "Scenes": []
}
```

`Scenes` の各要素は `ID`、`Label`、`Icon` を持ちます。通常のシーンは許可リストのシーンID、動的な電源ボタンは `power/論理ID/setup` をIDに設定します。共有鍵は母艦の `key` と同じ値です。要求はAES-GCMとHMACで保護し、時刻・nonce・操作IDで再送を防ぎます。

Wake-on-LANを使う場合は `/data/misc/techo5/mothership.json` に `Address`（母艦の `IP:port`）、`MAC`、`Broadcast` を指定します。対応する音声出力と画面操作は必要時に起動待ちをします。高速な音声家電操作は8秒の制限があり、母艦の起動を待つ経路ではありません。

予定と環境は約5分ごとに更新します。予定表示のタイムゾーンは現在 `Asia/Tokyo` です。読み取りエラー時は前回の情報と取得失敗の表示を使います。

## テストと計測

```text
python -m unittest discover -s tools/mothership -p "test_*.py"
```

通常テストは偽のAPIと一時ファイルを使います。`test_speech_bridge.py` を直接起動した場合のみ、ローカルVOICEVOXの実応答も確認して音声ファイルを書き出します。

`measure_jev.py` は標準入力の1行目から評価用APIキーを読み、固定例文をJevへ送ります。結果は `hall-jev-measurement.json` に保存し、Gitには含めません。家電操作は行いません。自動テストでは実行しません。

## 天気・予定の言い換え

Jevによる分類を任意で有効化できます。認証情報は `TYPESAFE_API_KEY` または `TYPESAFE_API_KEY_FILE` から読みます。[対応範囲・設定・実API評価](../../docs/jev-read-only.md)を参照してください。
