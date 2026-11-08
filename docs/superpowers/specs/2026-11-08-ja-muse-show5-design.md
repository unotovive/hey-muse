# 日本語 Muse 端末としての Echo Show 5（第 1 世代）設計書

作成日: 2026-11-08
対象ブランチ: `feature/ja-muse-show5`（2357gi/hey-muse の `506b173d`「本体連携」を起点。2026-11-09 改訂）

## 1. 目的と背景

Amazon 純正のまま手元にある Echo Show 5 第 1 世代（モデル H23K37、基板名 `checkers`）を、
Meta の AI エージェント Muse に日本語で話しかけられるリビング常設端末にする。

土台は友人（2357gi）のフォークで、すでに日本語表示・日本語音声認識・サイドチャット会話・
待機画面ダッシュボード・家電操作が入っている。2026-11-09 の友人の更新（本体連携、`lib/standalone`）で
予定取得と SwitchBot 操作は母艦を介さず端末内の Go で動くようになった。本設計はその方向に沿い、
母艦の役割を音声認識だけに絞る。本設計はそれを施主の環境
（Nature Remo、Google カレンダー、GPU 付き常時稼働 PC）に合わせて差し替え、体感速度と
常設運用の品質を上げるものである。

Muse 本体は Meta のクラウドで動き、端末は Muse Gadget SDK のクライアントとして
音声と画面の入出力を担う。この構造は変えない。

## 2. ゴール・非ゴール・アンチゴール

### ゴール

- 上流 TECHO5 の最新リリース（着手時点で v1.6.30、2026-11-07）に追従した土台の上に
  友人版の日本語化と Muse 対応が載っている。以後も上流のタグ単位で追従できる構成にする。
- 「Hey Muse」で起動し、日本語で聞いて日本語で答える。回答は書き終わる前に喋り始める。
- 照明とエアコンを「電気消して」などの定型句で Muse を介さず即時操作できる枠組みがある
  （Nature Remo 実機への接続は後回し。機種が判明してから繋ぐ）。
- 待機画面に時計・天気・今後の予定が出る。予定は Google カレンダーから直接読む。
- 地震情報が来たら待機画面に割り込んで表示し、一定時間で元に戻る。
- 部屋に人がいなければ画面を消し、近づいたら点ける。手のひらをカメラにかざすと読み上げが止まる。
- 母艦（この WSL2 PC）のサービスは Windows 起動時に自動で立ち上がる。
- 停電からの復帰後、人手なしで待機画面まで戻る（これが満たせない場合は原因と回避策を文書化する）。

### 非ゴール（v1 では作らない）

- ゴミ出し・花粉・洗濯指数・電車遅延の表示、朝のブリーフィング、カメラ画像の Muse 送信、
  PC の会議中表示。候補として `research_notes/追加機能アイデア/` に残す。
- ElevenLabs 対応。読み上げは OpenAI の音声で始める。設計上は追加できる形にしておく。
- Home Assistant 連携。友人版の HA 経路は壊さないが、設定も検証もしない。
- 顔認識、声紋判別、Meta 公式ウェイクワードモデルへの差し替え。

### アンチゴール（できても失敗とみなす）

- 家電操作の誤発火。引用・否定・曖昧な発話で照明やエアコンが動く。
- 地震表示が消えずに待機画面を占有し続ける。
- 母艦が落ちていると会話そのものができなくなる（読み上げは端末が直接クラウドへ行くので
  母艦不在でも会話は成立させる。音声認識だけは母艦依存を許容し、失敗時は Muse の音声認識へ
  フォールバックする。施主の判断: 母艦不在時に録音を Muse へ送ってよい。友人版にある
  「`idle-home.json` があれば音声ノートを送らず聞き返す」分岐は外す）。
- 個人情報（トークン、鍵、カレンダー URL、機器 ID）がリポジトリに入る。

## 3. 制約

- 端末: Echo Show 5 第 1 世代。MT8163、RAM 1GB、32bit ユーザーランド。音声認識や LLM 推論は
  端末では行わない。
- 解除: amonet（`mt8163-checkers` ブランチ）→ TWRP → LineageOS 18.1 checkers ビルド
  （R0rt1z2、上流 v1.6.30 が要求する v0.7）→ `tools/install-show.py`。LineageOS の zip は XDA 配布で、
  取得に施主の XDA アカウントが要る可能性がある。
- ブートイメージ: 友人版・wobsoriano 版のリリースに第 1 世代用は無い。上流 TECHO5 v1.6.30 の
  `techo5-boot-checkers-v1.6.30.img`（カーネル 4.9.337 系）を自分たちのリリースに同梱する。
  カーネルは GPL-2.0 なので上流のソース参照をリリースノートに書く。
- 母艦: WSL2（Ubuntu、systemd 有効）、RTX 4070 Ti SUPER 16GB、RAM 64GB、Windows 11 25H2。
  現在は NAT ネットワークで LAN から WSL へ届かない。
- Muse: 施主は未契約。契約と SDK トークン発行、Muse アプリからの BLE ペアリングは施主が行う。
  契約前でも解除・ビルド・母艦・待機画面・地震・人感は検証できる。
- Meta の SDK 規約: 個人・非商用、トークン 1 つで 50 台、転売禁止、Meta がいつでも停止可。
- 施主の作業ルール: 会話・コメント・コミットメッセージは日本語、ブランチ名は英語 kebab-case、
  `.env` 類はコミットしない、テストを壊さない。

## 4. 全体構成

```
[Echo Show 5 / echod (Go, ARMv7)]
  ├─ ウェイクワード・録音・AEC（TECHO5 既存）
  ├─ 音声認識: 母艦の Wyoming サーバーへ → 失敗時 Muse 音声ノートへフォールバック（既存）
  ├─ 定型句判定 → 端末内の家電バックエンドへ（既存 lib/standalone、Nature Remo を追加）
  ├─ Muse クライアント（/chat/subscribe、既存）→ 文単位ストリーミング読み上げ（新規）
  ├─ 読み上げ: OpenAI /v1/audio/speech を端末から直接（既存の speech.Client を使う）
  ├─ 待機画面: 端末内で予定・環境を取得（既存 lib/standalone。予定の取得元に Google Calendar API を追加）
  ├─ 地震: P2P 地震情報 WebSocket に端末から直接接続（新規）
  └─ 人感・手のひら: 既存の presence を有効化、手のひらで読み上げ停止（拡張）

[母艦 / WSL2 (Python 3.12 + systemd)]
  ├─ recognizer.py（既存を GPU 対応に）: faster-whisper large-v3-turbo、CUDA、Wyoming 50024
  ├─ speech_bridge.py（既存、任意）: VOICEVOX を使う場合だけ。既定では起動しない
  └─ 起動: systemd ユニット + Windows タスクスケジューラで WSL を起動時に立ち上げる（新規）
```

友人版の母艦経由ダッシュボード（`speech_bridge.py` の `/echo-home` 等、`lib/mothership`）は
壊さずに残すが、本設計では使わない。端末は `/data/misc/techo5/standalone-home/enabled` がある
本体連携モードで動く。

## 5. 決定事項（施主の判断）

| 項目 | 決定 |
| --- | --- |
| 音声認識 | 母艦の Whisper を GPU で動かす |
| 読み上げ | OpenAI の音声から始める。ElevenLabs は将来候補 |
| 家電 | Nature Remo。対象は照明とエアコン。機種不明のため枠だけ作り、実機接続は後回し。実装は端末内 Go（友人の本体連携に合わせる。2026-11-09 の設計者判断） |
| カレンダー | Google Calendar API（OAuth）で直接読む。家族から共有されたカレンダーも対象。実行場所は端末内 Go（2026-11-09、友人の iCal 方式との比較で施主が選択） |
| 音声の送信 | 母艦不在で音声認識できないときは、録音を Muse に送って認識させてよい |
| 追加機能 | ストリーミング読み上げ、地震速報の割り込み、人感で画面 ON/OFF と手のひら停止 |
| 見送り | 情報表示系、電車遅延、朝ブリーフィング、カメラ質問 |
| 母艦 | この PC（常時起動）。WSL2 上に置く |
| 上流追従 | 上流 TECHO5 の最新版にしっかり追従する（施主の指示。友人版の土台 v1.2.0 のままでは進めない） |
| 端末作業 | USB 接続、Windows 側の操作（PowerShell）は施主が全面的に許可。Muse 契約とペアリングは施主 |

## 6. 決定事項（設計者の判断。施主は読んで差し戻せる）

### 6.0 上流への追従

- 方式は「上流のリリースタグを友人版ブランチにマージする」。リベースは 36 コミット分の履歴を
  書き換え、友人の履歴との照合を難しくするので採らない。
- 対象は上流 `v1.6.30` のタグ。日々動いている `main` ではなくタグに合わせ、以後の追従も
  タグ単位で行う（上流はほぼ毎日リリースしているため、節目ごとにまとめて取り込む）。
- 試験マージ（2026-11-09、友人の本体連携を取り込んだ後）では衝突 36 ファイル。内訳は `feature/display` 13、
  `tools/linux` 3、`feature/voice` 3、`lib/bluez` 2、`feature/setup` 2、`config` 2、`go.mod`/`go.sum` 2、
  文書 4、その他 5。
  衝突しない箇所でも上流の API 変更が Muse 周りのコードに及ぶため、マージ後にビルドと
  Go 全テストを通すまでを一つの作業単位とする。
- 上流 v1.6.x で入ったものは移植せずそのまま使う（一定時間後の減光、次の予定の時計表示、
  夜間の赤時計、スライドショーなど）。上流の「返答の先読み再生」は Home Assistant 経路用で
  Muse には効かないので、6.1 は自前で作る。
- マージ後の Go は上流に合わせて 1.27、依存に `gorilla/websocket` が入る。地震情報の
  WebSocket はこれを使う。
- 上流が参照する LineageOS checkers ビルドは `v0.7`。インストーラーはこの版を要求する。

### 6.1 読み上げのストリーミング

- 現状、端末は Muse のチャットメッセージが完成するたびに喋る（`voice/muse.go` の `speakPart`）。
  Muse は通常 1 メッセージで答えるので、実質「全文完成を待ってから喋る」。
- 変更: `ReplyText` スナップショット（全文の途中経過）を監視し、文末記号（。！？、および改行）で
  区切れた文が増えるたびに、その文だけを `speech.Speaker.Speak` に渡す。読み上げは直列キューで、
  TTS の取得は次の文を先行して始める（先読み 1 文）。
- 途中経過のテキストが後から書き換わる可能性に備え、すでに喋った文字数より前は無視し、
  末尾の未完文は `Settled` または最終 `Reply` で確定して喋る。
- 文の分割ルールは `speech_bridge.py` の `speech_parts()` と同等のものを Go で実装し、
  Markdown 除去は既存の `speech.Spoken` を通す。
- 「考え中」の扱い: 録音終了から最初の音声が出るまで 5 秒を超えたら短い相槌音（既存の
  音クリップ機構 `docs/voice-clips.md` を使う）を 1 回だけ鳴らす。画面は既存の thinking 表示を使う。
- 読み上げは端末から OpenAI へ直接行く。母艦の VOICEVOX 経路は残すが既定にしない。
  友人の本体連携にある「定型句の返答は事前生成音声を再生」（`speakHomeReply`）は、音声ファイルが
  置かれていなければ素通りするので、そのまま残す。
- Muse への指示（長文を避け結論を先に）は Muse 側の設定であり、コードでは扱わない。
  セットアップ手順書に推奨文として書く。

### 6.2 Nature Remo バックエンド（端末内 Go）

- 友人の `lib/standalone` は SwitchBot 直叩き（`switchbot.go`）で、`Home.Appliance(ctx, key, action)`、
  `Dashboard(ctx, "environment", "")`、`CheckTargets` が入口。ここに家電バックエンドの抽象
  （`Appliance`、`Environment`、`Check`）を切り出し、`home-local.json` の
  `backend: "switchbot" | "nature_remo"`（省略時 `switchbot`）で選ぶ。SwitchBot 実装は現状の
  振る舞いを保ち、友人のテスト `home_test.go` を通す。
- Nature Remo 実装は Cloud API（`GET /1/appliances`、`POST /1/appliances/{id}/light`、
  `POST /1/appliances/{id}/aircon_settings`、`GET /1/devices` の `newest_events`）を使う。
  トークンは端末の `/data/misc/techo5/standalone-home/natureremo.env`（`NATURE_REMO_TOKEN=...`）から読む。
  レート制限（30 回/5 分）を守るため、家電一覧と環境値は 60 秒キャッシュする。
- ローカル API（`POST /messages`、ヘッダー `X-Requested-With` 必須、mDNS `_remo._tcp`）は
  照明・エアコンの定型操作で学習済み信号が取れる場合にだけ使い、2 秒で失敗したら Cloud に
  フォールバックする。nano と Lapis はローカル API 非対応なので設定で無効化できる。v1 では枠（設定項目と
  フォールバック順）だけ作り、実装は Cloud のみでよい。
- 論理 ID は `living`（照明）と `ac`（エアコン）。`home-local.json` の `voice_devices` に
  Remo 側の家電名（`nickname`）を書く。エアコンは「つけて／消して」「冷房／暖房」「○度にして」の
  最小セットとし、友人版の定型句判定（`voice/appliances.go`）を日本語表現で拡張する。
  `applianceCommand` に数値（温度）を運ぶ場を足す。
- 実機接続は後回し。Swagger 定義に基づく `httptest` のモックでユニットテストを書き、
  `techo5 tools home-check` が Remo でも疎通確認できるようにする。

### 6.3 Google カレンダー（端末内 Go）

- 友人の `lib/standalone/calendar.go` は非公開 iCal URL を 5 分ごとに読み、`agenda-cache.json` に
  キャッシュし、失敗時は前回結果と「更新待ち」表示を返す。この器（`refresh` → `agenda`、
  `Result{Stale, Fetched}`）は残し、取得元に Google Calendar API を足す。
  端末ディレクトリに `google-token.json` があれば API、無ければ従来の `calendar-feeds.txt`（iCal）を使う。
- 認証は「デスクトップアプリ」種別の OAuth クライアント、スコープは `calendar.readonly` のみ。
  初回だけ施主が PC のブラウザで同意し、`tools/standalone-home/google_auth.py` が
  `google-token.json`（`client_id`、`client_secret`、`refresh_token`）を書く。これを友人の
  `deploy_usb.py config` と同じ経路で端末へ置く。端末は `https://oauth2.googleapis.com/token` で
  アクセストークンを更新し、メモリに持つ（ディスクには書かない）。
- Google Cloud プロジェクトと OAuth クライアントの作成手順は `docs/ja/setup.md` に書く。
  テストユーザー登録が要る（公開審査は受けない）。
- API は `GET /calendar/v3/calendars/{calendarId}/events` に `singleEvents=true`、`orderBy=startTime`、
  `timeMin`/`timeMax`（今から 8 日後まで）、`maxResults=100`、`pageToken` で繰り返し予定を展開済みで取る。
  `home-local.json` の `calendars` 配列（`id`、`name`）を使い、共有カレンダーも ID で指定する。
  終日・複数日・複数カレンダーの表示は友人版の仕様（`Event{Day}`、24 件上限）を踏襲する。
- 母艦の `echo_home.py`（Codex MCP 経由）は触らない。本体連携モードでは使われない。

### 6.4 地震情報の割り込み

- 端末の `echod` から `wss://api.p2pquake.net/v2/ws` に直接接続する（母艦が落ちていても動く）。
  同一 IP から 2 接続までなので端末だけが接続し、母艦は接続しない。
- 表示対象: コード 551（地震情報）は設定した最大震度以上（既定: 震度 3）または設定地域が
  含まれるとき。556（緊急地震速報・警報）は参考情報として表示し、「警報ではない」旨を添える。
  552（津波予報）は発表時に表示。561 は無視。
- 表示は `render.go` の優先チェーンに `s.quake` を加え、着信・アラーム鳴動の次の優先度で
  全画面に出す。表示時間は 551 で 90 秒、556 で 60 秒、タップで閉じられる。
  音は鳴らさない（夜間に不要な覚醒を招くため。設定で短いチャイムを有効化できる）。
- 同一 `id` の重複配信を除去し、切断時は指数バックオフで再接続する。
- WebSocket クライアントは、上流マージで入る `gorilla/websocket` を使う。

### 6.5 人感と手のひら

- カメラによる在室検知と、無人で画面 OFF・接近で ON は友人版にすでにある
  （`feature/presence/`、`display/presence_screen.go`）。v1 では設定画面から有効化できることと、
  待機画面の消灯時間を施主の生活に合わせられることを確認する。
- 手のひらジェスチャーは現状アラーム・タイマー停止のみ。これを拡張し、Muse の読み上げ中なら
  読み上げとターンを中断し、鳴動中ならアラームを止める。
- 「一定時間操作がなければ減光」は上流 v1.6.30 のマージで入るので、設定画面から使えることを確認する。

### 6.6 母艦の運用

- 母艦の役割は音声認識（Wyoming、50024）だけ。読み上げは端末から OpenAI へ、予定と家電は端末内で
  完結するので、母艦が落ちても会話・待機画面・家電操作は動く（音声認識だけ Muse 側へフォールバック）。
- WSL2 のネットワークを `networkingMode=mirrored` に切り替え、Windows ファイアウォールに
  50024（音声認識）の受信許可を追加する。これで Echo から PC の LAN アドレスで届く。
  切り替えには WSL の再起動が要る。
- サービスは systemd ユニット `muse-asr.service`。VOICEVOX を使う場合だけ `muse-bridge.service`
  （`speech_bridge.py`、50022）を追加で有効にする。Windows のタスクスケジューラに
  「ログオン時に `wsl.exe -d <distro> --exec /bin/true`」を登録して WSL を起こす。
  Windows のスリープは「なし」に設定する。
- 音声認識サーバーは `recognizer.py`（現在は `cpu`/`int8` 固定、`--model-dir` 必須）に
  `--device`、`--compute-type`、`--model` を追加し、`cuda`/`float16`/`large-v3-turbo` を既定にする。
  CPU フォールバックは残す。
- 端末側の音声認識待ちは現在最大 12 秒。母艦不在時に毎ターン待たされないよう、接続タイムアウトを
  短くし、連続失敗時は一定時間 Muse 音声認識へ直行する。
- 秘密情報は `~/.config/hey-muse/` 配下のファイルに置き、systemd の `EnvironmentFile` で渡す。

### 6.7 リリースと更新

- 施主の GitHub アカウント（unotovive）に `hey-muse` リポジトリを作り、origin をそこに向ける。
  2357gi は `friend` リモートとして残す。
- 更新 URL（`update/releases_cronos.go`）、インストーラーの `REPO`、ビルド検証の参照、
  署名鍵（`update/trust.go`、`techo5lib.py`）を新リポジトリと新しい ed25519 鍵に置き換える。
  秘密鍵は `~/.config/techo5-release/sign.key` に置き、リポジトリにも CI にも入れない。
- リリースは `vX.Y.Z-ja.N` 形式（`vX.Y.Z` は取り込んだ上流の版）。v1 は `v1.6.30-ja.1`。`install-show.py` の版の正規表現
  （`-muse\.(\d+)` 固定）と `update/version.go` の比較を `-ja.N` に対応させる。
  GitHub Actions の既存ワークフローでテストと ARMv7 ビルドを行い、ルートファイルシステムと署名は
  WSL で作る。
- 第 1 世代用ブートイメージは上流 v1.6.30 の `techo5-boot-checkers-v1.6.30.img` を
  リリース資産として同梱する（上流の該当版と同じカーネル 4.9.337 系）。
- 友人のリポジトリにはリリースが無く、インストーラーが向いている wobsoriano のリリースには
  日本語化も checkers 用ブートイメージも無い。そのため初回インストールの前に自分のリリースを
  作る（工程の順序は 8 章）。

### 6.8 導入手順の自動化

- Windows 側: `winget install usbipd` を PowerShell（管理者）で実行し、Echo を `usbipd bind` →
  `usbipd attach --wsl` で WSL に渡す。WSL 側に `adb`、`fastboot`、`python3`、Go をそろえる。
- 解除とインストール: amonet の実行、TWRP からの `install-show.py --lineage-zip` を順に行う。
  各段階の確認コマンドと「ここで止まったら」の対処を `docs/ja/setup.md` にまとめる。
- 母艦のセットアップは `tools/mothership/install.sh` 一本で systemd ユニットまで入れる。

## 7. 受け入れ確認

1. 解除後、`install-show.py` が checkers を認識し、初回起動で待機画面（時計）が出る。
2. 電源ケーブルを抜いて 30 秒後に挿し直し、人手なしで待機画面に戻る。戻らない場合は
   現象（FASTBOOT 停止など）と回避策を文書化する。
3. 母艦不在でも「Hey Muse」→ 質問 → 回答読み上げが成立する（音声認識は Muse 側にフォールバック）。
4. 母艦ありで、録音終了から最初の音声が出るまでの時間を 10 回計測し、中央値を記録する。
   文単位ストリーミングの有効・無効で比較する。
5. 「電気消して」で端末が Nature Remo のモック API に正しい要求を送ることを Go のテストで確認する。
   「電気消してって言われた」「電気消さないで」では送らない。
6. Google Calendar API 経由の予定が待機画面に出て、繰り返し予定と終日予定が正しい。
   `techo5 tools home-check` が件数と取得時刻を返す。
7. P2P 地震情報のテスト配信（公式のサンプルまたは録画した JSON）で割り込み表示と自動復帰を確認する。
8. 無人で設定時間後に画面が消え、近づくと点く。読み上げ中に手のひらをかざすと止まる。
9. Windows を再起動すると、母艦の音声認識サービスが自動で上がり、端末からの認識が通る。
10. Go 全パッケージのテスト、Python のテスト、ARMv7 ビルドがローカルと GitHub Actions で通る。

## 8. 作業の進め方

1. 上流 `v1.6.30` のマージ。衝突解消、ビルド、Go 全テストが通るまで。
2. リポジトリの付け替え、署名鍵、版名の対応。マージ済みの状態で最初のリリース
   `v1.6.30-ja.1` を作る（rootfs ビルド + 上流 v1.6.30 の checkers ブートイメージ）。
3. 端末の解除（amonet、LineageOS v0.7）。1・2 と並行できる。
4. 端末へ初回インストール。受け入れ 1・2 を確認。
5. 母艦の基盤（ネットワーク、systemd、GPU 認識）、Whisper GPU。
6. 端末内の Google Calendar API と Nature Remo 枠（友人の `lib/standalone` の拡張）。
7. ストリーミング読み上げ、音声フォールバックの分岐変更、手のひら停止、地震割り込み。
8. リリース `v1.6.30-ja.2` と端末更新、Muse 契約後のペアリングと受け入れ 3〜10。

## 9. 作り手に委ねる事項

- Go 側のパッケージ分割とテストの書き方（既存の `_test.go` の流儀に合わせる）。
- 文分割の細部（括弧や引用符の扱い、英数字の混在）。
- 地震表示のレイアウト（既存の全画面ページの見た目に合わせる）。
- Nature Remo のエアコン温度の丸め、照明の調光対応の有無。
- systemd ユニットの再起動ポリシー、ログの置き場。
- `docs/ja/` 配下の手順書の構成。

## 10. 参考

- 調査ノート: `/home/unotovive/projects/musekusa/research_notes/`（Muse 公式、Echo 解除、
  コミュニティ事例、法的側面、追加機能アイデア）
- 上流 TECHO5: https://github.com/HuskerMinion/techo5 （v1.2.10 に checkers ブートイメージ）
- amonet: https://github.com/R0rt1z2/amonet/tree/mt8163-checkers
- Muse Gadget SDK: https://github.com/facebookincubator/muse-gadget-sdk
- P2P 地震情報 API v2: https://www.p2pquake.net/develop/json_api_v2/
- Nature Remo API: https://swagger.nature.global/ 、ローカル API: https://local-swagger.nature.global/
