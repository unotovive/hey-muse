# 受け入れ記録（日本語版 Muse / Echo Show 5）

対象リリースは `v1.6.30-ja.2`（タグ e97152cd）です。記録の日付は 2026-11-09 です。実機（Echo Show 5）はこの時点でブートローダー解除前のため、端末で行う確認はすべて「未実施（実機待ち）」とし、その手順と期待する結果を載せます。実施したものは、実行したコマンドと結果をそのまま書きます。

| 項目 | 内容 | 状態 |
|---|---|---|
| 1 | ブートローダー解除と起動の確認 | 別途記録（実機待ち） |
| 2 | 署名つきイメージの導入の確認 | 別途記録（実機待ち） |
| 3 | 母艦停止時の音声ノートへの切替 | 未実施（実機待ち） |
| 4 | 録音終了から最初の音声までの時間 | 未実施（実機待ち） |
| 5 | Nature Remo と家電コマンドのテスト | 実施済み・合格 |
| 6 | 待機画面の予定（終日・繰り返し） | 未実施（実機待ち） |
| 7 | 地震情報の割り込み表示 | 未実施（実機待ち） |
| 8 | 在室検知と手で覆って停止 | 未実施（実機待ち） |
| 9 | 母艦の再起動後の認識 | 未実施（実機待ち） |
| 10 | ローカルのテスト・ビルド・CI | 実施済み・合格 |

## 項目 1・2（別タスクの範囲）

項目 1 と 2 はブートローダー解除と起動の作業の側で記録し、ここには記録しません。解除が終わるまで、以降の実機の項目は進められません。

## 項目 3 母艦停止時の音声ノートへの切替（未実施・実機待ち）

前提: 端末が `v1.6.30-ja.2` で動き、Muse のペアリングが済んでいること（`docs/ja/setup.md` 第 8 部）。

1. 母艦（WSL）で認識を止めます。

   ```bash
   sudo systemctl stop muse-asr
   ```

2. 端末に「Hey Muse、今日の天気は」と話しかけます。
3. 端末の USB コンソールまたは SSH でログを確認します。ログは、SSH が有効なときの既定で `/data/techo5-linux/techo5.log` です（Dot では `echod.log`）。違うときは `ls /data/techo5-linux/` で探します。

   ```sh
   grep 'local recognition unavailable' /data/techo5-linux/techo5.log | tail -3
   ```

4. 母艦で認識を戻し、もう一度話しかけます。

   ```bash
   sudo systemctl start muse-asr
   ```

期待する結果:

- 手順 2 で、回答が読み上げられます。
- ログに `muse: local recognition unavailable, using Muse voice recognition` が出て、Muse の音声認識（音声ノートの送信）に切り替わります。
- 手順 4 のあとは、ログに `muse: using local speech recognition` が出ます。

## 項目 4 録音終了から最初の音声までの時間（未実施・実機待ち）

母艦が動いている状態で、同じ質問を 10 回します。各回のログ `muse turn answered` の `first_audio_ms` を集め、中央値を記録します。

```sh
grep 'muse turn answered' /data/techo5-linux/techo5.log | tail -10
```

比較用の値は、ストリーミングを使わない `v1.6.30-ja.1` のものを使ってかまいません。`thinkingAfter` など他の設定は変えません。

| 版 | 10 回の `first_audio_ms` | 中央値 |
|---|---|---|
| `v1.6.30-ja.2` | （未計測） | （未計測） |
| `v1.6.30-ja.1`（比較） | （未計測） | （未計測） |

## 項目 5 Nature Remo と家電コマンドのテスト（実施済み・合格）

Nature Remo の Cloud API 呼び出し（照明・エアコン、トークン欠落、429 の送信停止、環境値）と家電コマンドの解釈のテストです。

```sh
cd echod && export PATH=$HOME/sdk/go/bin:$HOME/go/bin:$PATH
go test ./... -run 'Remo|ApplianceCommands' -v
```

結果: 一致したテスト 36 件がすべて `PASS`、`FAIL` は 0 件でした（パターン `Remo` は、リモート関連の既存テストにも一致します）。Remo に関わるものは次のとおりです。

- `TestApplianceCommands`（`internal/feature/voice`）
- `TestRemoLightAndAirconCommands`
- `TestRemoRateLimitedSendsNothing`
- `TestRemoMissingTokenFails`
- `TestRemoEnvironmentAndCheck`（以上 `internal/lib/standalone`）

これは Cloud API をテスト用サーバーで置き換えた検証です。実際の Remo との通信は、機種が決まってから確認します。

## 項目 6 待機画面の予定（未実施・実機待ち）

前提: `docs/ja/setup.md` 第 7 部の手順 3 が通っていること。

1. 自分の Google カレンダーに、終日の予定と、毎週繰り返す予定を 1 つずつ作ります（今後 7 日以内に当たる日付にします）。
2. 最大 5 分待ち、端末の待機画面を見ます。

期待する結果: 両方の予定が待機画面に出ます。`techo5 tools home-check` の `Calendar refreshed: N events` の N が、作った予定を含む件数になります。

## 項目 7 地震情報の割り込み表示（未実施・実機待ち）

端末の USB コンソールか SSH で実行します。

```sh
techo5 tools quake-test --code 551
techo5 tools quake-test --code 556
```

期待する結果:

- コード 551: 数秒以内に全画面で地震情報が割り込み表示され、90 秒後に元の画面へ戻ります。
- コード 556: 60 秒表示され、「※参考情報」と「気象庁の警報そのものではありません」の表記があります。
- どちらも、画面のタップで閉じられます。スイッチが切れていても見本は表示されます。

## 項目 8 在室検知と手で覆って停止（未実施・実機待ち）

1. 設定画面の「画面」カードで、「人感検知」を入れ、消灯までの分数を 5 分にします。
2. 部屋を無人にして 5 分待ちます。続けて、端末へ近づきます。
3. 「手で覆って停止」を入れ、アラームを鳴らして、カメラを手のひらで覆います。

期待する結果:

- 無人 5 分で画面が消え、近づくと点灯します。
- アラームが鳴っている間にカメラを覆うと、アラームが止まります。

## 項目 9 母艦の再起動後の認識（未実施・実機待ち）

母艦の systemd と Windows 自動起動は `docs/ja/setup.md` 第 3 部のとおりです。Windows を再起動し、ログオンして 2 分待ってから確認します。

```bash
systemctl is-active muse-asr
```

```powershell
Test-NetConnection 母艦のLAN_IP -Port 50024
```

期待する結果は `active` と `TcpTestSucceeded : True` です。続けて、項目 3 の手順 2 に準じて話しかけ、ログに `muse: using local speech recognition` が出ることを確認します。

## 項目 10 ローカルのテスト・ビルド・CI（実施済み・合格）

| 確認 | コマンド | 結果 |
|---|---|---|
| Go | `cd echod && go test ./...` | 162 パッケージが `ok`、`FAIL` なし |
| Go（dot） | `cd echod && go test -tags dot ./...` | 147 パッケージが `ok`、`FAIL` なし |
| Go（spot） | `cd echod && go test -tags spot ./...` | 55 パッケージが `ok`、`FAIL` なし |
| 母艦の Python | `cd tools/mothership && python3 -m unittest` | 31 件、`OK`（1 件は skip） |
| 認識の Python | `cd tools/echo-asr/pc && python3 -m unittest` | 8 件、`OK` |
| ARMv7 ビルド | `cd echod && GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -o /tmp/echod-arm-check ./cmd/echod` | 成功。32 ビット ARM の静的 ELF、約 72 MB |
| CI | GitHub Actions の `v1.6.30-ja.2` の run（42517139377） | `uboot` と `build` が成功、`panel` は実行されず |

CI の run は https://github.com/unotovive/hey-muse/actions/runs/42517139377 です。`gh run view` で確認しました。リリースは、`gh release view v1.6.30-ja.2 --repo unotovive/hey-muse --json isDraft,assets` で、`isDraft` が `true`（ドラフト）、添付は `echod-arm`、`manifest.json`、`manifest.json.sig`、`SHA256SUMS`、`techo5-boot-checkers-v1.6.30-ja.2.img`、`techo5-rootfs-v1.6.30-ja.2.tar.gz` の 6 つでした。施主の公開操作を待っています。
