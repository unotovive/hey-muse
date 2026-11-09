# 母艦を Windows ネイティブに移す（設計）

2026-11-09。`2026-11-08-ja-muse-show5-design.md` の 5 章「母艦: この PC（常時起動）。WSL2 上に置く」と 6.6 を、本書で差し替える。

## 1. 意図と理由

母艦（Whisper 日本語音声認識、Wyoming、TCP 50024）は WSL2 の systemd サービス `muse-asr` として動かす設計だった。施主の運用では **PC は常時起動だが WSL は落とすことがある**（開発作業の都合で `wsl --shutdown` する）。WSL が落ちると母艦も止まり、その間の音声認識は Muse 側へフォールバックする（会話は成立するが日本語の精度と速さが落ちる）。

母艦を Windows 側で直接動かし、WSL の状態と無関係に動くようにする。あわせて、**常駐していてもメモリ（GPU・RAM）を食わない**ことを施主が求めた。

Windows 側の Python 3.13.7 で faster-whisper 1.2.1 / ctranslate2 4.8.5 / nvidia-cublas-cu12 / nvidia-cudnn-cu12 の wheel が入ることは確認済み（2026-11-09）。

## 2. 目標・非目標・やってはいけないこと

目標

- WSL が落ちていても、PC にログオンしていれば母艦が動く。
- 待機中（直近に認識要求が無いとき）は **GPU メモリ 0、RAM は Python 1 本分（数十 MB）**。
- 長時間（週単位）動かしてもメモリが増えない。ログは上限つき。
- 端末側（echod）は変更しない。端末から見た振る舞い（プロトコル、ポート、応答時間）は従来と同等。
- Linux / WSL を母艦にする人向けの経路（`tools/mothership/install.sh` + systemd）も同じ `recognizer.py` で動き、同じ省メモリ動作になる。

非目標

- ログオン前（再起動後にログイン画面で止まっている間）に動かすこと。施主の判断で不要。
- VOICEVOX 用ブリッジ（`speech_bridge.py` / `muse-bridge`）の Windows 対応。従来どおり Linux 任意機能のまま。
- Home Assistant 連携。

やってはいけないこと（できていても失敗とみなす）

- Windows 側の母艦が WSL のファイル（`\\wsl$` / `\\wsl.localhost`）を参照する。WSL が落ちた瞬間に起動できなくなる。
- 待機中に Whisper のモデルや CUDA のコンテキストが GPU に残る。
- ログオンのたびにコンソール窓が出る。
- ログファイルが無限に育つ。

## 3. 制約

- Windows 11、Python 3.13.7（`C:\Users\<user>\AppData\Local\Programs\Python\Python313\python.exe`、`py -3` で起動可）、RTX 4070 Ti SUPER、NVIDIA ドライバー 616.92。
- 施主の PC に自動ログオンは無い。自動起動は「ログオン時」トリガーで足りる。
- タスクスケジューラへの登録・削除、`sudo`、管理者 PowerShell は Claude Code の自動許可で拒否される。**インストーラの実行は施主が行う**（Claude Code の入力欄で `! ` を付けて自分の権限で実行、または PowerShell で直接）。設計・実装・テスト・文書は Claude 側。
- 受信ファイアウォール規則 `muse-asr`（TCP 50024、プログラム不問、Inbound Allow）は作成済み。Hyper-V 用規則 `muse-asr` も作成済みだが Windows ネイティブでは不要（残しても害は無い）。
- 端末 Show5 の IP は 192.168.0.124、母艦（Windows のイーサネット）は 192.168.0.77。どちらもルーターで DHCP 予約を推奨（手順書に書く）。
- 端末側クライアント（`echod/internal/lib/wyoming/wyoming.go`、`feature/voice/muse.go`）: 接続タイムアウト 2 秒。話し終わってから `transcribe` → `audio-start` → `audio-chunk`… → `audio-stop` を一気に送る。認識待ちは最大 12 秒。2 回続けて失敗すると 1 分間は母艦を使わない。`describe` を送るコードは端末に無い（`describe` 対応は手動の接続確認用に残す）。

## 4. 決めたこと（施主の判断と、設計者の判断）

### 4.1 施主の判断

- 母艦は Windows ネイティブで動かす（WSL 依存をなくす）。
- 自動起動はログオン後でよい。
- 常駐していてもメモリを食わないように作る。
- モデルを下ろすまでの時間は **30 分**（省メモリと応答の速さの折り合い。2026-11-09、ビルダーチェックの指摘を受けて選択）。

### 4.2 設計者の判断（施主は読んで差し戻せる）

**2 プロセス構成**

`recognizer.py` を「受付」と「認識」の 2 プロセスに分ける。

- 受付（常駐）: 50024 で listen し、Wyoming のイベントを読み書きする。`faster_whisper` / `ctranslate2` / `numpy` を **決して import しない**（CUDA のコンテキストはプロセスが生きている限り解放されないため）。`describe` には従来どおり静的な `info` を返し、認識プロセスを起こさない。
- 認識（必要なときだけ）: 受付が起動する子プロセス（同じ `recognizer.py` を worker モードで起動する等、方式は実装者に任せる）。モデルを GPU に載せ、受付から渡された PCM を認識して返す。**最後の認識から `--idle-unload-minutes`（既定 30、0 で従来どおり常駐）** 経っても要求が無ければ自分で終了する。
- 起動のタイミング: 受付は `transcribe` イベントを受けた時点で認識プロセスが居なければ起動を始め、`audio-stop` で PCM を渡す。**ただし現行の端末は話し終わってから全イベントを一気に送る**（`feature/voice/muse.go` の `ask` → `finalTranscript` → `wyoming.Transcribe`。端末内蔵の early STT は `127.0.0.1:50024` 専用で母艦には送られない）ので、先読みの効果は無く、**下ろした後の最初の一言は Python 起動 + CUDA + モデル読み込み（目安 3〜6 秒）の分だけ遅くなる**。端末の待ちは 12 秒、2 回続けて失敗すると 1 分間は母艦を使わない。
- コールドスタートの上限: 受付は 1 要求あたり **10 秒**で応答を打ち切り、超えたら `error`（`recognition-failed`）を返す（端末の 12 秒より先に返して、無駄に待たせない）。**認識プロセスの読み込みは打ち切らない**ので、直後の要求は温まった状態で受けられる。
- 将来の改善（本書の範囲外）: 端末の early STT を母艦の宛先にも送るよう echod を変えれば、発話中に母艦を温められる。端末側の変更と新しいリリースが要るため、本書では行わない。
- 同時要求: 従来どおり 1 件ずつ。認識中に別の要求が来たら従来と同じ `recognizer busy`（3 秒待ち）扱い。
- 認識プロセスが途中で死んだら、その要求には従来どおり `error` イベント（`recognition-failed`）を返し、次の要求で新しく起動する。
- 受付が死んだらタスクスケジューラの再起動設定で上げ直す（下記）。

**Windows では `--device cuda` 固定**

`model_settings()` の `auto` は CUDA が見えないと黙って CPU/int8 に落ちる。Windows では DLL の読み込み失敗やドライバー更新でこの経路に入ると、`large-v3-turbo` を CPU で回して毎回 12 秒を超え、端末は失敗と遮断を繰り返す。そこで **インストーラが焼き込むタスクの引数は `--device cuda`** とし、CUDA が使えないときは認識プロセスが明示的に失敗してログに残る（受付は `error` を返す）。コードの既定値 `auto` は Linux 経路のため変えない。

**Windows での CUDA DLL**

認識プロセスは `faster_whisper` を import する前に、`sys.platform == 'win32'` なら `nvidia.cublas` / `nvidia.cudnn` パッケージの DLL ディレクトリを `os.add_dll_directory` で足す（無ければ何もしない）。Linux の `LD_LIBRARY_PATH` 方式はそのまま。

**ログ**

`--log-file PATH` を追加。指定時は `RotatingFileHandler`（上限サイズと世代数は実装者が決める。目安 5 MB × 3）。未指定なら従来どおり標準エラー（systemd の journal 向け）。

**Windows 側の配置**

- アプリ用ディレクトリ `%LOCALAPPDATA%\hey-muse\`（例: `C:\Users\<user>\AppData\Local\hey-muse\`）に、`venv\`、`app\recognizer.py`（リポジトリからのコピー）、`logs\`、起動用スクリプトを置く。リポジトリ（WSL 内）は参照しない。
- Hugging Face のモデルキャッシュは Windows ユーザーの既定の場所（`%USERPROFILE%\.cache\huggingface`）。インストール時に一度モデルを読み込んで取得しておく（初回の認識でダウンロードが走らないように）。

**インストーラ `tools/mothership/windows/install-asr.ps1`（新規）**

施主が **通常権限（非昇格）** の PowerShell で実行する。何度実行しても安全（冪等）。インストーラ自身は昇格しない（venv とモデルキャッシュの所有者を施主のユーザーに揃えるため）。管理者権限が要る操作に当たったら、その場で止まらず、最後に「管理者 PowerShell で実行するコマンド」をまとめて表示する。施主のアカウントは Administrators のメンバー（UAC の分割トークン）なので、案内されたコマンドを自分で昇格して実行できる。引数: `-Allow <端末IP>`（必須、複数可）、`-Port`（既定 50024）、`-IdleMinutes`（既定 30）。

1. `py -3` で `venv` を作り（あれば再利用）、`faster-whisper==1.2.1 nvidia-cublas-cu12 nvidia-cudnn-cu12` を入れる。
2. `recognizer.py` を `app\` へコピーする（上書き）。
3. モデル `large-v3-turbo` を一度読み込んで取得し、CUDA で動くことを確認する（失敗したら理由を出して止まる）。
4. 起動用スクリプトを書き、タスク **`hey-muse ASR`** を登録する: ログオン時トリガー、実行ユーザーは現在のユーザー（対話ログオン、RunLevel は通常）、ウィンドウ非表示、実行時間制限なし、バッテリーでも開始・継続、失敗時は再起動（例: 1 分間隔で 3 回。回数と間隔は実装者が決める）。
5. 旧タスク `hey-muse WSL`（WSL を起こしたままにするもの）があれば削除を試み、権限不足なら上記の案内に `Unregister-ScheduledTask -TaskName 'hey-muse WSL' -Confirm:$false` を載せる。
6. ファイアウォール規則 `muse-asr`（TCP、指定ポート）が無ければ、上記の案内に `setup-mothership.ps1 -WindowsNative` の実行を載せる。
7. 最後に、タスクを即時開始し、`Test-NetConnection 127.0.0.1 -Port <Port>` で待ち受けを確かめる。

`setup-mothership.ps1`（管理者用）に **`-WindowsNative` スイッチ**を足す。付けると、ファイアウォール規則（通常の受信規則のみ。Hyper-V 規則は作らない）と AC 電源時スタンバイ無効化だけを行い、`.wslconfig` の mirrored と WSL 常駐タスクの登録、ディストリビューションの確認は行わない。付けなければ従来どおり（Linux/WSL 母艦の経路）。手順書の Windows ネイティブ版では `-WindowsNative` 付きで案内する。

**WSL 側の後始末（施主の手で）**

- `sudo systemctl disable --now muse-asr`。
- `.wslconfig` の `networkingMode=mirrored` は不要になる。消して元の NAT に戻すことを手順書で勧める（任意。消す場合は `wsl --shutdown` が要る）。
- `~/.config/hey-muse/asr.env` は Linux 経路用に残す。

**設定値**

`--allow`、`--port`、`--idle-unload-minutes`、`--log-file` はタスクのコマンドラインに焼き込む。変えたいときはインストーラを引数を変えて再実行する。

### 4.3 端末側

変更なし。端末の設定（手順書 第 7 部）で音声認識の宛先を `192.168.0.77:50024` にする。

## 5. 受け入れ

- 項目 9（差し替え）: Windows を再起動してログオンし、2 分以内にタスク `hey-muse ASR` が実行中、`Test-NetConnection 192.168.0.77 -Port 50024` が True、端末から `nc -zv 192.168.0.77 50024` が成功。**WSL を `wsl --shutdown` で落としても同じ結果**。
- 省メモリ（新規）: 認識直後に `nvidia-smi` の使用量が増え、`--idle-unload-minutes` 経過後に認識プロセスが消えて使用量が元に戻る。受付プロセスの RAM が数十 MB。
- 項目 4（追記）: 「久しぶりの一言」（認識プロセスが居ない状態）と「連続の一言」の両方で `first_audio_ms` を 10 回ずつ測り、中央値を併記する。合否基準: 連続の一言は従来どおり。久しぶりの一言は 10 回とも 12 秒以内に認識が返ること（Muse 側へのフォールバックが起きないこと）。
- 既存テスト `tools/echo-asr/pc/test_recognizer.py` が通り、受付と認識プロセスの生存管理（先読み起動、アイドル終了、busy、worker 死亡時の error）をテストで押さえる。テストは GPU 無しで動く（認識プロセスは偽物に差し替えられる作りにする）。

## 6. 文書

- `docs/ja/setup.md` 第 3 部を Windows ネイティブ版に書き直す。Linux/WSL 母艦の手順は付録として残す。
- `README.md` の「母艦（WSL の Whisper）」を「母艦（Windows の Whisper）」に。
- `docs/ja/acceptance.md` 項目 9 の差し替え、項目 4 の追記、省メモリ確認の追加。
- `tools/mothership/README.md` に Windows 経路を追記。
- `2026-11-08-ja-muse-show5-design.md` 5 章の母艦の行に「→ 本書で差し替え」を追記。

## 7. 実装者に任せること

- 受付と認識プロセスの間の通信方式（標準入出力のパイプ、`multiprocessing`、ローカルソケットなど）。
- 起動用スクリプトの形（`.ps1` / `.cmd`、`pythonw.exe` の利用など）。窓が出ないこと、PATH に DLL を通さなくても動くこと（コード側で `add_dll_directory` するため）を満たせばよい。
- ログの上限サイズ・世代数、タスクの再起動回数・間隔。
- 既存 venv の検出と再利用の判断、pip の出力の扱い。
- テスト用の偽の認識プロセスの作り方。
