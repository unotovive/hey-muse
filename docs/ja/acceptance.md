# 受け入れ記録（日本語版 Muse / Echo Show 5）

対象リリースは `v1.6.30-ja.2`（タグ e97152cd）です。記録の日付は 2026-11-09 です。実機（Echo Show 5 第 1 世代、checkers）は 2026-11-09 にブートローダーを解除し、`v1.6.30-ja.2` を導入しました（項目 1・2）。端末で行うそれ以外の確認は「未実施（実機待ち）」とし、その手順と期待する結果を載せます。実施したものは、実行したコマンドと結果をそのまま書きます。

| 項目 | 内容 | 状態 |
|---|---|---|
| 1 | ブートローダー解除と起動の確認 | 実施済み・合格 |
| 2 | 署名つきイメージの導入の確認 | 実施済み・合格 |
| 3 | 母艦停止時の音声ノートへの切替 | 未実施（実機待ち） |
| 4 | 録音終了から最初の音声までの時間 | 未実施（実機待ち） |
| 5 | Nature Remo と家電コマンドのテスト | 実施済み・合格 |
| 6 | 待機画面の予定（終日・繰り返し） | 未実施（実機待ち） |
| 7 | 地震情報の割り込み表示 | 実施済み・合格 |
| 8 | 在室検知と手で覆って停止 | 未実施（実機待ち） |
| 9 | 母艦の再起動後の認識 | 一部実施（Windows 再起動は未実施） |
| 10 | ローカルのテスト・ビルド・CI | 実施済み・合格 |
| 11 | 母艦の省メモリ（待機中は GPU を使わない） | 実施済み・合格 |

## 項目 1 ブートローダー解除と起動の確認（実施済み・合格）

実施日: 2026-11-09。端末: Echo Show 5 第 1 世代（checkers）。手順は `docs/ja/setup.md` 第 1 部・第 2 部。

| 確認 | 結果 |
|---|---|
| amonet による解除後、TWRP で起動 | TWRP 3.7.0_9-0、`ro.product.device` = checkers、`ro.boot.verifiedbootstate` = yellow |
| `fastboot getvar product` / `unlock_status` | `product: CHECKERS`、`unlock_status: true` |
| 導入後の初回起動 | 歓迎カード「This is Show5」→ 初期設定 → 時計の待機画面 |
| 停電からの復帰 | 電源ケーブルを抜いて 30 秒後に挿し直し、3 分以内に時計の待機画面へ自力で復帰。FASTBOOT で止まることはなかった |

導入までに起きたこと（いずれも slot store 作成前で、やり直しで解消）: usbipd の共有の残りで fastboot が WSL に取られた、Windows に fastboot（`0BB4:0C01`）用のドライバーが無かった、TWRP の MTP が有効で zip の転送中に adb が落ちた、userdata 初期化後の再起動で 1 回 HACKED FASTBOOT に止まった。対処は第 1 部・第 2 部に書いた。

## 項目 2 署名つきイメージの導入の確認（実施済み・合格）

`tools/install-show.py`（Windows の Python 3.13.7、platform-tools 37.0.2）で TWRP から導入。

| 確認 | 結果 |
|---|---|
| マニフェストの署名 | `release manifest signed by the project key` |
| rootfs / boot | `techo5-rootfs-v1.6.30-ja.2.tar.gz`、`techo5-boot-checkers-v1.6.30-ja.2.img` をマニフェストで照合 |
| 導入の完了 | `Done. 'Show5' runs TECHO5 v1.6.30-ja.2 from slot a` と端末の IP |
| SSH での版 | `/etc/techo5-release` = `techo5 rootfs v1.6.30-ja.2 ... daemon echod version v1.6.30-ja.2 (e97152c)` |
| スロット | `active: a`、`booted: a`、slot a が `trial 2` → 起動から約 8 分で `good`。停電復帰後も `good` |

補足: 初期設定で Home Assistant に追加する選択肢を選ぶと、15 分間は暗号鍵が無く SSH が起動しない（仕様。`echod/internal/feature/api/adopt.go`）。この間に再起動したため一時的に SSH に入れなかったが、枠が閉じて新しい鍵ができた後に SSH をオンにして到達を確認した。

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

4. 母艦で認識を戻し、もう一度話しかけます。失敗が 2 回続くと 60 秒は母艦を使わず、その間は Muse へ直行します。戻ったことを確かめるときは、60 秒待ってから話しかけます。

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

比較用の値は、文単位の先読み読み上げ（コミット bfe463ca）より前の版で測ります。タグ `v1.6.30-ja.1` はすでに先読みを含み、アセットも公開していないため、比較相手にはなりません。また先読みを切る実行時の設定はありません。そのため仕様 7.4 の「有効・無効の比較」とは異なり、版どうしの比較にします。

bfe463ca の 1 つ前（a9e80bbb）を一時的な worktree に取り出し、そこから端末へ入れます。版名は任意の文字列でよく（`deploy-rootfs.sh` は `--version` の値を検査しません）、ここでは `0.0.0-nostream.1` とします。`0.0.0-` で始めると、どの正式版より古い版として扱われます。

```bash
git worktree add ../hey-muse-nostream a9e80bbb
cp -r bin ../hey-muse-nostream/bin        # git 管理外の ARM ヘルパー。無いと deploy-rootfs.sh が止まる
ln -s "$PWD/inputs" ../hey-muse-nostream/inputs   # 同じく git 管理外（代わりに TECHO5_INPUTS=$PWD/inputs でも可）
cd ../hey-muse-nostream
HOST=<端末IP> tools/linux/deploy-rootfs.sh --version 0.0.0-nostream.1 --install --reboot
```

`bin/` と `inputs/` は `.gitignore` 対象のため、新しい worktree には含まれません。元の checkout から持ち込んでから実行します。

測り終えたら `v1.6.30-ja.2` を入れ直し（第 7 部の手順 0）、`git worktree remove ../hey-muse-nostream` で片付けます。`thinkingAfter` など他の設定は変えません。

| 版 | 10 回の `first_audio_ms` | 中央値 |
|---|---|---|
| `v1.6.30-ja.2` | （未計測） | （未計測） |
| `0.0.0-nostream.1`（a9e80bbb、比較） | （未計測） | （未計測） |

母艦は認識プロセスを 30 分使われないと下ろすため、次の 2 通りも測ります（`v1.6.30-ja.2` で各 10 回）。

| 場面 | 10 回の `first_audio_ms` | 中央値 |
|---|---|---|
| 久しぶりの一言（認識プロセスが居ない状態。30 分以上あけて話しかける） | （未計測） | （未計測） |
| 連続の一言（認識プロセスが居る状態。続けて話しかける） | （未計測） | （未計測） |

合否基準: 久しぶりの一言は、10 回とも 12 秒以内に母艦で認識されること（ログに `muse: using local speech recognition`）。

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

## 項目 7 地震情報の割り込み表示（実施済み・合格）

端末の USB コンソールか SSH で実行します。

```sh
techo5 tools quake-test --code 551
techo5 tools quake-test --code 556
```

期待する結果:

- コード 551: 数秒以内に全画面で地震情報が割り込み表示され、90 秒後に元の画面へ戻ります。
- コード 556: 60 秒表示され、「※参考情報」と「気象庁の警報そのものではありません」の表記があります。
- どちらも、画面のタップで閉じられます。スイッチが切れていても見本は表示されます。

2026-11-10 に SSH から実施しました（`v1.6.30-ja.2`、地震情報のスイッチは既定の切）。

| コード | 結果 |
|---|---|
| 551 | 「千葉県北西部（千葉県・東京都・神奈川県）」の見本が全画面で割り込み表示され、日本語も正しく表示された。タップまたは時間経過で元の画面に戻った |
| 556 | 「※参考情報」と「気象庁の警報そのものではありません」の表記つきで表示され、元の画面に戻った |

## 項目 8 在室検知と手で覆って停止（未実施・実機待ち）

1. 設定画面の「画面」カードで、「人感検知」を入れ、消灯までの分数を 5 分にします。
2. 部屋を無人にして 5 分待ちます。続けて、端末へ近づきます。
3. 「手で覆って停止」を入れ、アラームを鳴らして、カメラを手のひらで覆います。

期待する結果:

- 無人 5 分で画面が消え、近づくと点灯します。
- アラームが鳴っている間にカメラを覆うと、アラームが止まります。

## 項目 9 母艦の再起動後の認識（一部実施・Windows 再起動は未実施）

母艦は Windows ネイティブ（タスク `hey-muse ASR`）です。手順は `docs/ja/setup.md` 第 3 部のとおりです。Windows を再起動し、ログオンして 2 分待ってから確認します。

```powershell
Get-ScheduledTask -TaskName 'hey-muse ASR' | Select-Object State
Test-NetConnection 母艦のLAN_IP -Port 50024
```

端末の SSH または USB コンソールから、母艦に届くことも確認します。

```sh
nc -zv 母艦のLAN_IP 50024
```

次に `wsl --shutdown` を実行し、同じ確認をもう一度行います。

期待する結果:

- `State` が `Running`、`TcpTestSucceeded : True`、端末からの `nc` が成功します。
- `wsl --shutdown` のあとも、同じ結果になります。
- 続けて、項目 3 の手順 2 に準じて話しかけ、ログに `muse: using local speech recognition` が出ます。

### 2026-11-09 の記録（Windows 再起動の前に確認できたもの）

`install-asr.ps1 -Allow 端末のIP` で導入し、WSL の `muse-asr` を止めた（`inactive`・`disabled`）うえで確認しました。Windows の再起動と `wsl --shutdown` 後の確認は未実施です。

| 確認 | 結果 |
|---|---|
| タスク | `hey-muse ASR` が `Running`。旧タスク `hey-muse WSL` は削除済み |
| 待ち受け | `0.0.0.0:50024`。python のブロック規則なし |
| 端末から | `nc -zv 母艦のLAN_IP 50024` が `open` |
| 認識 | 端末から Wyoming の要求（1 秒の音声）を手で送り、`transcript` が返る |
| 受付を強制終了 | 認識プロセスも終了して GPU が解放され、見張りのトリガーで約 4 分 50 秒後に起動し直した（`Running`、ポート `open`） |
| `Stop-ScheduledTask` | 受付・認識プロセスとも残らず終了し、GPU が解放された |

導入後に実機で見つかり、修正したこと:

- 冷えた状態の最初の要求で、モデルの読み込みが次の要求が届くまで止まった（認識プロセスの標準入力の読み待ちが Windows で他の処理を止めていた）。修正後は、冷えた状態からの要求が約 5 秒で `transcript` になった。
- 受付が落ちてもタスクが起動し直さなかった（失敗時の再起動設定はプロセスの異常終了では働かない）。5 分ごとの見張りのトリガーを追加した。
- インストーラの実行中に、見張りのトリガーでタスクが起動しうる。インストール中はタスクを無効にするようにした。

### 旧構成での記録

2026-11-09 に WSL の母艦の手順 1（`setup-mothership.ps1`）を実施し、`.wslconfig` の `networkingMode=mirrored`、タスク `hey-muse WSL`（「タスクを停止するまでの時間」= 無効、「電源管理」= 空欄）、ファイアウォール規則（通常・Hyper-V とも TCP 50024）、AC 電源のスタンバイ無効を確認しました。再起動後の確認は未実施でした。この構成は、Windows ネイティブの母艦に置き換えました（手順は `docs/ja/setup.md` 付録 A・B）。

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

## 項目 11 母艦の省メモリ（実施済み・合格）

母艦が待機中に GPU を使わず、話しかけたときだけモデルを読み込むことを確認します。

1. 母艦の PowerShell で、話しかける前の GPU メモリを記録します。

   ```powershell
   nvidia-smi --query-gpu=memory.used --format=csv
   ```

2. 端末に話しかけ、直後にもう一度同じコマンドを実行します。
3. 30 分以上、話しかけずに置き、同じコマンドを実行します。あわせて、残っているプロセスを確認します。

   ```powershell
   Get-CimInstance Win32_Process -Filter "Name='pythonw.exe'" | Select-Object ProcessId,ParentProcessId,CommandLine
   ```

   受付も認識プロセスも `pythonw.exe` です。venv の `pythonw.exe` はランチャーなので、受付だけでも 2 つ見えます。プロセスの数ではなく、コマンドラインに `worker.py` を含むものがあるかで判定します。

期待する結果:

- 手順 2 の直後は、手順 1 より約 2 GB 増え、コマンドラインに `worker.py` を含むプロセスがあります。
- 30 分後は、手順 1 の値に戻ります。
- 30 分後は、コマンドラインに `worker.py` を含むプロセスがありません（`recognizer.py` を含むものだけが残ります）。

### 2026-11-09〜10 の記録

| 時点 | GPU メモリ | `worker.py` のプロセス |
|---|---|---|
| 待機中（23:34 の要求の前） | 8177 MiB | なし（受付の 2 つだけ、計 約 24 MB） |
| 要求の直後（23:34） | 10551 MiB（+約 2.4 GB） | あり（ランチャーと本体の 2 つ） |
| 最後の認識（23:34:33）から 30 分後（00:04:33） | 8131 MiB | なし |

ログには `idle for 1800 seconds, exiting` が出て、認識プロセスが自分で終了しました。要求は端末から Wyoming の要求を手で送って作りました（端末の音声認識の宛先は第 7 部の設定前のため）。
