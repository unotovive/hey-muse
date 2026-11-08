# Echo Show 5 の本体連携

待機画面と定型の音声操作は、Go の `techo5` プロセス内で動きます。本体から SwitchBot API v1.1 に接続してセンサー値を取得し、設定済みの家電とシーンを操作します。Google カレンダーの iCal 購読から今後7日間の予定を約5分ごとに取得します。更新に失敗すると前回の予定と「更新待ち」を表示します。

「明日の予定を教えて」「廊下の電気を消して」などの定型句には、端末の音声認識と保存済みの日本語音声で応答します。一般の Muse 会話は設定済みの Muse 接続先を使います。

## 既存の Echo を更新する

1. `echod` ディレクトリで本体用の実行ファイルをビルドします。

   ```sh
   GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o techo5-arm ./cmd/echod
   ```

2. Windows のセットアップ用 Python で、USB 接続した端末に実行ファイルを転送します。以前のスタンドアロン版からの更新は、設定と応答音声を再利用します。移行ツールは旧版の SQLite を PC で読み、最後に受け付けた電源操作の向きを引き継ぎます。

   ```text
   python deploy_usb.py daemon --serial SERIAL --binary PATH_TO_TECHO5_ARM
   ```

3. Echo の USB コンソールでインストールと読み取り確認を実行します。

   ```sh
   cd /data/misc/techo5/standalone-home
   sh install.sh
   techo5 tools home-check
   ```

4. 再起動後、待機画面に予定とセンサー値が表示されたら、同じディレクトリで `sh cleanup-legacy.sh` を実行します。旧版が本体に展開した Python とライブラリを削除します。

初めて本体連携を導入する場合は、設定済みの PC ブリッジから `deploy_usb.py config --serial SERIAL` で接続情報を転送し、`daemon` に `--audio` で `make_voice.py` の出力ディレクトリを渡します。従来の待機画面設定 `idle-home.json` が必要です。

`calendar-feeds.txt` には `home-local.json` の `calendars` と同じ順に購読 URL を1行ずつ保存します。秘密情報を含む設定と予定キャッシュは管理者用ディレクトリに置きます。

## Nature Remo を使う

`home-local.json` の `backend` に `nature_remo` を指定すると、SwitchBot の代わりに Nature Remo の Cloud API（`https://api.nature.global`）で家電を操作し、温度・湿度を取得します。`backend` を省略または `switchbot` にすると従来どおりです。Remo ではシーンと電源ボタンは表示されません。

トークンは https://home.nature.global/ で発行し、`~/natureremo.env`（または `deploy_usb.py config --remo-env <ファイル>`）に次の1行で保存すると、`natureremo.env` として端末へ送られます。`switchbot.env` と `calendar-feeds.txt` は存在するときだけ送られます。

```
NATURE_REMO_TOKEN=取得したトークン
```

`voice_devices` の `name` は Remo アプリの機器名（nickname）、`type` は照明が `LIGHT`、エアコンが `AC` です。

```json
{
  "backend": "nature_remo",
  "remo_local": false,
  "calendars": [],
  "voice_devices": {
    "living": {"name": "リビング照明", "type": "LIGHT"},
    "ac": {"name": "エアコン", "type": "AC"}
  }
}
```

`deploy_usb.py config` は `backend` が `nature_remo` のとき、ブリッジ側 `home-local.json` の `voice_devices` と `hall` を SwitchBot 側の名前で上書きせず、そのまま送ります。

`remo_local` はローカル API 用の枠で、現在は `home-check` に表示するだけです。送信は Cloud API のみです。機器一覧とセンサー値は60秒キャッシュされ、429 が返ると `Retry-After`（無ければ300秒）の間は送信を止めます。

## 元の構成に戻す

初回の導入時に保存した待機画面の設定、起動設定、実行ファイルに戻すには、USB コンソールで次を実行します。

```sh
sh /data/misc/techo5/standalone-home/rollback.sh
```
