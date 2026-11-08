# 日本語 Muse Echo Show 5 カスタム版 実装計画

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 友人版 hey-muse（2357gi、本体連携まで取り込み済み）を上流 TECHO5 v1.6.30 に追従させ、施主の環境（Nature Remo、Google カレンダー、GPU 母艦）向けの機能を足し、Echo Show 5 第 1 世代（checkers）へ導入する。

**Architecture:** 端末の Go デーモン `echod` が音声・画面・Muse クライアント・家電・予定・地震情報を担い、母艦（WSL2）は Whisper の音声認識だけを提供する。家電と予定は友人の `lib/standalone` を拡張して端末内で完結させる。リリースは施主自身の GitHub リポジトリと署名鍵で `v1.6.30-ja.N` として配る。

**Tech Stack:** Go 1.27（ARMv7、CGO 無効）、gorilla/websocket、Python 3.12（母艦: faster-whisper/CUDA、OAuth 同意ツール）、Alpine rootfs ビルド（QEMU user）、systemd、Windows PowerShell（usbipd、タスクスケジューラ）。

**Spec:** `docs/superpowers/specs/2026-11-08-ja-muse-show5-design.md`

## Global Constraints

- 上流追従は「タグ `v1.6.30` のマージ」。リベース禁止。Go は上流に合わせ 1.27。
- 端末ビルドは `GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0`。モジュールパスは `github.com/HuskerMinion/techo5/echod` のまま。
- Go のテストは `cd echod && go test ./... && go test -tags dot ./... && go test -tags spot ./...`。Python は `cd tools/mothership && python3 -m unittest` と `cd tools/echo-asr/pc && python3 -m unittest`。既存テストを壊さない。
- 版名は `v1.6.30-ja.N`。初回 `v1.6.30-ja.1`、機能追加後 `v1.6.30-ja.2`。
- 第 1 世代用ブートイメージは上流 v1.6.30 の `techo5-boot-checkers-v1.6.30.img` を `techo5-boot-checkers-<版名>.img` の名前で同梱。LineageOS は `lineage-18.1-checkers-v0.7`（zip 名 `lineage-18.1-20260904-UNOFFICIAL-checkers.zip`）。
- 秘密情報の置き場: 母艦 `~/.config/hey-muse/`（`asr.env`、`google-client.json`、`google-token.json`）、署名鍵 `~/.config/techo5-release/sign.key`（0600）、端末 `/data/misc/techo5/standalone-home/`（`natureremo.env`、`google-token.json`、`home-local.json`）。どれもコミットしない。
- 会話・コメント・コミットメッセージは日本語。ブランチは `feature/ja-muse-show5`。コミット末尾に `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>` と `Claude-Session: https://claude.ai/code/session_013XoetZKpfRq13qk4th5rxf`。
- 仕様の定数: 文末記号 `。！？!?` と改行で区切る。相槌は録音終了から 5 秒。地震 551 は `maxScale >= 30`（震度 3）または設定地域で 90 秒、556 は 60 秒で「参考情報」、552 は 90 秒、561 は無視。WS は `wss://api.p2pquake.net/v2/ws`、端末だけが接続。Remo のキャッシュ 60 秒。カレンダーは今から 8 日後まで、24 件上限、`maxResults=100`、スコープ `calendar.readonly`。母艦不在の音声認識は連続 2 回失敗で 60 秒間 Muse へ直行。
- ダウンロードした zip・tar・img は `~/downloads-untrusted/<名前>/` の空ディレクトリに置き、そこで解釈系を起動しない。

## Review Focus

1. Muse の `ReplyText` 途中経過で既に喋った文より前の文字が書き換わる。喋った文を繰り返さず、末尾だけ確定時に喋ること（Task 13 のテスト `TestMuseIgnoresRewrittenPrefix`）。
2. 地震 WebSocket が切断・不正 JSON・未知コードを返す。再接続し、表示チェーンが壊れないこと（Task 15 の `TestClientReconnectsAndDelivers`、`TestClientSkipsBadFrames`、`TestParseIgnoresOtherCodes`）。
3. Nature Remo API が 429 やトークン欠落を返す。家電は動かさず「確認できませんでした」と返し、POST を送らないこと（Task 9 の `TestRemoRateLimitedSendsNothing`）。
4. Google のトークン更新が `invalid_grant` を返す。前回キャッシュを「更新待ち」で表示し、無限に再試行しないこと（Task 11 の `TestGoogleRefreshFailureUsesCache`）。
5. 母艦が落ちている。毎ターン 12 秒待たず、2 回失敗したら 60 秒は Muse の音声認識へ直行すること（Task 12 の `TestMuseSkipsLocalSTTAfterFailures`）。

---

### Task 1: ビルド環境と現状の基線

**Files:**
- なし（環境構築と計測のみ）

**Interfaces:**
- Produces: Go 1.27 が `go` で使える。友人版 HEAD（`ac9f8fac`）の Go/Python テスト結果の基線がコミットメッセージ用メモとして残る。

- [ ] **Step 1: Go 1.27 を入れる**

```bash
cd ~/downloads-untrusted && mkdir -p go && cd go
curl -fsSLO https://go.dev/dl/go1.27.3.linux-amd64.tar.gz
curl -fsSL https://go.dev/dl/?mode=json | python3 -I -c "import json,sys; print([f['sha256'] for v in json.load(sys.stdin) for f in v['files'] if f['filename']=='go1.27.3.linux-amd64.tar.gz'])"
sha256sum go1.27.3.linux-amd64.tar.gz   # 上と一致すること
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.27.3.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> ~/.profile && export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin
go version
```
Expected: `go version go1.27.3 linux/amd64`（1.27.x なら可。上流の build.yml が 1.27.3 を指す）

- [ ] **Step 2: 友人版 HEAD の Go テストを流し、基線を記録する**

Run: `cd /home/unotovive/projects/musekusa/hey-muse/echod && go test ./... 2>&1 | tail -20`
Expected: 全パッケージ `ok`（友人の文書では全テスト通過）。失敗があればパッケージ名と理由をメモし、Task 2 のマージ後比較に使う。

- [ ] **Step 3: ARMv7 ビルドが通ることを確認する**

Run: `cd echod && GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -o /tmp/echod-arm-baseline ./cmd/echod && ls -l /tmp/echod-arm-baseline`
Expected: バイナリができる。

- [ ] **Step 4: Python テストの基線**

Run: `cd tools/mothership && python3 -m unittest -q 2>&1 | tail -3; cd ../echo-asr/pc && python3 -m unittest -q 2>&1 | tail -3`
Expected: 両方 `OK`（mothership は 31 件、skipped=1）。

---

### Task 2: 上流 v1.6.30 のマージ

**Files:**
- Modify: 衝突 36 ファイル（`git merge-tree --write-tree --name-only HEAD v1.6.30 | grep '^CONFLICT'` の一覧。主なもの: `.gitattributes`、`NOTICE`、`README.md`、`docs/building.md`、`docs/install.md`、`echod/go.mod`、`echod/go.sum`、`echod/internal/config/brain.go`、`config/store.go`、`feature/assistant/assistant.go`、`feature/display/{display,display_spot,drawn,render,render_spot,render_styles,render_styles_spot,render_weather,sheet_spot,sheet_voice,sheet_widgets,splash,turn_style}.go`、`feature/setup/{brain,page}.go`、`feature/voice/{backend,conversation,reply}.go`、`lib/bluez/gatt.go` と `gatt_test.go`、`lib/locale/locale.go`、`lib/triggers/triggers.go`、`tools/linux/{deploy-rootfs,mkrootfs,techo5-lib}.sh`）
- Modify: 衝突しないが上流の API 変更でコンパイルが壊れる友人側ファイル（ビルドで判明）

**Interfaces:**
- Consumes: Task 1 の Go 1.27。
- Produces: `v1.6.30` を祖先に持つ `feature/ja-muse-show5`。`echod/go.mod` は `go 1.27.0` と友人の `github.com/teambition/rrule-go v1.8.2` を含む。以後の Task はこの木を前提にする。

- [ ] **Step 1: 衝突一覧と、各衝突ファイルで友人が変えた範囲を把握する**

```bash
cd /home/unotovive/projects/musekusa/hey-muse
git merge-tree --write-tree --name-only HEAD v1.6.30 | grep '^CONFLICT' | sed 's/.*in //' > /tmp/conflicts.txt; wc -l /tmp/conflicts.txt
for f in $(cat /tmp/conflicts.txt); do echo "== $f"; git diff --stat 8a4d96cd HEAD -- "$f" | tail -1; done
```
Expected: 36 行。各ファイルの友人側変更量が分かる（`8a4d96cd` が merge-base）。

- [ ] **Step 2: マージを始める（コミットしない）**

Run: `git merge --no-ff --no-commit v1.6.30 2>&1 | grep -c CONFLICT`
Expected: 36。`.gitattributes:46` の「upstream/main is not a valid attribute name」警告は無害。

- [ ] **Step 3: 方針別に衝突を解く**

解き方（ファイル群ごと）。各ファイルで `git diff 8a4d96cd HEAD -- <file>` が友人の変更、`git diff 8a4d96cd v1.6.30 -- <file>` が上流の変更:
- `echod/go.mod`、`go.sum`: 上流側を採り、`github.com/teambition/rrule-go v1.8.2` の require を戻す。解決後 `cd echod && go mod tidy`。
- `.gitattributes`、`NOTICE`、`docs/building.md`、`docs/install.md`: 上流側を採り、友人が足した段落（Muse、日本語、Noto CJK、hey-muse のリポジトリ名）を戻す。`README.md` は友人の日本語導入部を先頭に保ち、上流の本文更新を取り込む。
- `config/brain.go`、`config/store.go`: 友人の `Muse` 構造体・`validate()`・`BrainMuse` を保ち、上流の新フィールドを足す。
- `feature/display/*`（13）: 上流側を土台に、友人の変更（日本語描画 `text_*` の呼び出し、`render_voice_panel`、`standby_home` の表示、天気の日本語、Muse の設定行）を当て直す。上流で入った減光・次の予定の時計表示・夜間赤時計・スライドショーは残す。
- `feature/voice/{backend,conversation,reply}.go`: 上流側を土台に、友人の Muse バックエンド登録・サイドチャット・先行音声認識（`early_stt.go` との接点）・分割読み上げの接点を当て直す。
- `lib/bluez/gatt.go` と `gatt_test.go`: 友人の Muse BLE ペアリング用 GATT を上流の GATT 実装の上に戻す。
- `lib/locale/locale.go`、`lib/triggers/triggers.go`: 日本語（友人）と上流の追加言語の和集合。
- `feature/setup/{brain,page}.go`、`feature/assistant/assistant.go`: 上流側を土台に、友人の Muse 設定 UI と日本語を戻す。
- `tools/linux/{deploy-rootfs,mkrootfs,techo5-lib}.sh`: 上流側を採り、友人が足したフォント・リポジトリ名（`wobsoriano/hey-muse` の attestation 行）を戻す。

Run（都度）: `git diff --name-only --diff-filter=U | wc -l`
Expected: 0 になるまで続ける。

- [ ] **Step 4: ビルドとテストが通るまで直す**

```bash
cd echod && go build ./... && go vet ./... && go test ./... && go test -tags dot ./... && go test -tags spot ./...
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -o /tmp/echod-arm-merged ./cmd/echod
cd ../tools/mothership && python3 -m unittest -q && cd ../echo-asr/pc && python3 -m unittest -q
```
Expected: すべて成功。衝突しなかったファイルのコンパイルエラー（上流の API 変更が友人コードに及んだもの）はここで直す。Task 1 の基線で失敗していたものは同じ失敗なら可。

- [ ] **Step 5: マージをコミットする**

```bash
cd /home/unotovive/projects/musekusa/hey-muse && git add -A && git commit -F - <<'EOF'
上流 TECHO5 v1.6.30 をマージ

- 衝突 36 ファイルを解消（画面描画 13、音声 3、ビルドスクリプト 3 ほか）
- 友人版の日本語化・Muse 対応・本体連携を上流の最新に載せ替え
- Go 1.27、rrule-go を維持。Go 全テスト、Python テスト、ARMv7 ビルドを確認

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_013XoetZKpfRq13qk4th5rxf
EOF
```

---

### Task 3: リポジトリの付け替え、署名鍵、版名

**Files:**
- Create: GitHub リポジトリ `unotovive/hey-muse`（公開。端末が認証なしでリリース資産を取るため）
- Modify: `echod/internal/update/releases_cronos.go:6`、`echod/internal/update/trust.go:18`、`tools/techo5lib.py:30`、`tools/install-show.py:59,278`、`tools/linux/deploy-rootfs.sh`（`gh attestation verify ... --repo`）、`.github/workflows/build.yml`（コメントの repo 名）、`.github/workflows/dashcast.yml`、`docs/releasing-the-fork.md`
- Test: `echod/internal/update/version_ja_test.go`（新規）、`tools/test_install_show_version.py`（新規）

**Interfaces:**
- Produces: `releases = "https://github.com/unotovive/hey-muse/releases"`。`releaseKey` と `RELEASE_KEY` は新鍵の公開側。`show_version()` が `v1.6.30-ja.1` を `(1,6,30,1)` 相当に並べる。`update.Newer("v1.6.30-ja.2","v1.6.30-ja.1") == true`。

- [ ] **Step 1: リポジトリを作り、リモートを付け替える**

```bash
cd /home/unotovive/projects/musekusa/hey-muse
gh repo create unotovive/hey-muse --public --description "Echo Show 5 を日本語の Muse 端末にする hey-muse/TECHO5 派生（個人・非商用）"
git remote rename origin friend && git remote add origin https://github.com/unotovive/hey-muse.git && git remote -v
```
Expected: `origin` が unotovive、`friend` が 2357gi、`upstream` が HuskerMinion。

- [ ] **Step 2: 署名鍵を作る**

`echod/cmd/mkmanifest/main.go` の `sign()` が `-sign-key` をどう読むか確認する（友人の文書: 32 バイト ed25519 シードの base64）。その形式で:
```bash
mkdir -p -m 700 ~/.config/techo5-release
python3 -I -c "
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from cryptography.hazmat.primitives import serialization as s
import base64
k=Ed25519PrivateKey.generate()
seed=k.private_bytes(s.Encoding.Raw,s.PrivateFormat.Raw,s.NoEncryption())
pub=k.public_key().public_bytes(s.Encoding.Raw,s.PublicFormat.Raw)
open('$HOME/.config/techo5-release/sign.key','w').write(base64.b64encode(seed).decode())
open('$HOME/.config/techo5-release/sign.pub','w').write(base64.b64encode(pub).decode())
print(base64.b64encode(pub).decode())"
chmod 600 ~/.config/techo5-release/sign.key
```
Expected: 44 文字の公開鍵が表示される。施主に「この鍵をこの PC 以外にも控えること」を最終報告で伝える。

- [ ] **Step 3: 失敗するテストを書く（Go の版比較）**

`echod/internal/update/version_ja_test.go`:
```go
func TestNewerRanksJaSuffix(t *testing.T) {
	for _, c := range []struct{ offered, running string; want bool }{
		{"v1.6.30-ja.2", "v1.6.30-ja.1", true},
		{"v1.6.31-ja.1", "v1.6.30-ja.2", true},
		{"v1.6.30-ja.1", "v1.6.30-ja.2", false},
		{"v1.6.30-ja.10", "v1.6.30-ja.9", true},
	} {
		if got := Newer(c.offered, c.running); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.offered, c.running, got, c.want)
		}
	}
	if !ValidVersion("v1.6.30-ja.1") {
		t.Error("v1.6.30-ja.1 should be a valid version")
	}
}
```

- [ ] **Step 4: 走らせる**

Run: `cd echod && go test ./internal/update/ -run TestNewerRanksJaSuffix -v`
Expected: 既存の `comparePrerelease` が数値フィールドを数として比べるなら PASS。FAIL なら `version.go` を直す（`ja.10 > ja.9`）。

- [ ] **Step 5: 失敗するテストを書く（Python の版解釈）**

`tools/test_install_show_version.py`:
```python
import importlib.util, pathlib, unittest
spec = importlib.util.spec_from_file_location('install_show', pathlib.Path(__file__).with_name('install-show.py'))
mod = importlib.util.module_from_spec(spec); spec.loader.exec_module(mod)

class ShowVersionTest(unittest.TestCase):
    def test_ja_suffix_orders_after_upstream_and_by_number(self):
        self.assertLess(mod.show_version('v1.6.30'), mod.show_version('v1.6.30-ja.1'))
        self.assertLess(mod.show_version('v1.6.30-ja.1'), mod.show_version('v1.6.30-ja.2'))
        self.assertLess(mod.show_version('v1.6.30-ja.2'), mod.show_version('v1.6.31-ja.1'))
    def test_muse_suffix_still_parses(self):
        self.assertIsNotNone(mod.show_version('v1.0.0-muse.1'))
```
`install-show.py` が import 時に副作用（引数解析や adb 呼び出し）を持つ場合は `if __name__ == '__main__'` 配下であることを確認してから進める。

- [ ] **Step 6: 走らせて失敗を確認**

Run: `cd tools && python3 -m unittest test_install_show_version -v`
Expected: FAIL（`-ja.` を解釈できず None）。

- [ ] **Step 7: 文字列と鍵と正規表現を置き換える**

- `releases_cronos.go`: `https://github.com/unotovive/hey-muse/releases`
- `trust.go` の `releaseKey`、`techo5lib.py` の `RELEASE_KEY`: Step 2 の公開鍵
- `install-show.py`: `REPO = 'unotovive/hey-muse'`、`show_version` の正規表現を `^v(\d+)\.(\d+)\.(\d+)(?:-(?:muse|ja)\.(\d+))?$`
- `deploy-rootfs.sh`、`build.yml` のコメント、`docs/releasing-the-fork.md`: `wobsoriano/hey-muse` → `unotovive/hey-muse`（`grep -rn "wobsoriano/hey-muse" --exclude-dir=.git` が 0 件になるまで）
- `dashcast.yml`: job に `if: github.repository == 'HuskerMinion/techo5'` を足す（上流のコンテナレジストリへ push できず毎タグ失敗するため。設計者判断）

- [ ] **Step 8: テスト**

Run: `cd tools && python3 -m unittest test_install_show_version -v && cd ../echod && go test ./internal/update/ ./cmd/mkmanifest/`
Expected: PASS。`update` のテストに固定鍵で署名検証するものがあれば、テスト用鍵を使っているか確認し、`releaseKey` 差し替えで壊れないこと。

- [ ] **Step 9: コミットして push**

```bash
git add -A && git commit -m "リリース先を unotovive/hey-muse に付け替え、新しい署名鍵と -ja.N 版名に対応

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_013XoetZKpfRq13qk4th5rxf"
git push -u origin feature/ja-muse-show5
```
Expected: GitHub Actions（build.yml）はタグ push 時のみ走る。ブランチ push では走らない。

---

### Task 4: 初回リリース `v1.6.30-ja.1`

**Files:**
- Create: `docs/ja/release.md`（WSL でのリリース手順。`docs/releasing-the-fork.md` の内容を日本語で引き継ぎ、Mac 固有を WSL に置換）
- Modify: `NOTICE`（ブートイメージのカーネルが GPL-2.0 である旨と上流ソースの URL が無ければ追記）
- 成果物（コミットしない）: `bin/echod-arm`、`bin/techo5-rootfs-v1.6.30-ja.1.tar.gz`、`bin/techo5-boot-checkers-v1.6.30-ja.1.img`、`bin/manifest.json`、`bin/manifest.json.sig`、`bin/SHA256SUMS`

**Interfaces:**
- Consumes: Task 3 の鍵とリポジトリ。
- Produces: `https://github.com/unotovive/hey-muse/releases/latest/download/manifest.json` が `"version": "v1.6.30-ja.1"` を返し、資産に `techo5-boot-checkers-v1.6.30-ja.1.img` がある。Task 6 のインストーラーはこれを使う。

- [ ] **Step 1: rootfs ビルドの前提を入れる**

`docs/building.md` の Linux/WSL 節に従う:
```bash
sudo apt install -y git python3 qemu-user-static binfmt-support uidmap
cd /home/unotovive/projects/musekusa/hey-muse && python3 tools/fetch-inputs.py   # inputs/ に apk.static 等
mkdir -p ~/apk && cp inputs/apk.static ~/apk/apk.static
ls /proc/sys/fs/binfmt_misc/qemu-arm
```
Expected: `qemu-arm` が存在。

- [ ] **Step 2: 上流 v1.6.30 の資産を取り、ヘルパーとブートイメージを取り出す**

```bash
mkdir -p ~/downloads-untrusted/techo5-v1.6.30 && cd ~/downloads-untrusted/techo5-v1.6.30
gh release download v1.6.30 --repo HuskerMinion/techo5 -p 'techo5-rootfs-v1.6.30.tar.gz' -p 'techo5-boot-checkers-v1.6.30.img' -p manifest.json -p manifest.json.sig -p SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
mkdir x && tar -xzf techo5-rootfs-v1.6.30.tar.gz -C x ./usr/local/bin/techo5-pico ./usr/local/bin/techo5-aec ./usr/local/bin/techo5-librespot ./usr/local/bin/techo5-ffmpeg
cd /home/unotovive/projects/musekusa/hey-muse && mkdir -p bin
for h in pico aec librespot ffmpeg; do cp ~/downloads-untrusted/techo5-v1.6.30/x/usr/local/bin/techo5-$h bin/techo5-$h-arm; done
cp ~/downloads-untrusted/techo5-v1.6.30/techo5-boot-checkers-v1.6.30.img bin/techo5-boot-checkers-v1.6.30-ja.1.img
```
Expected: `SHA256SUMS: OK`。tar の中のパス表記（`./usr/...` か `usr/...`）は `tar -tzf | head` で確認して合わせる。友人版の mkrootfs が要求するフォント等の追加入力は `tools/linux/mkrootfs.sh` の inputs 参照を読んで揃える。

- [ ] **Step 3: タグを打ち、CI でテストと ARMv7 ビルドを通す**

```bash
git tag v1.6.30-ja.1 && git push origin v1.6.30-ja.1
gh run watch --repo unotovive/hey-muse $(gh run list --repo unotovive/hey-muse --workflow build.yml -L1 --json databaseId -q '.[0].databaseId')
gh run download --repo unotovive/hey-muse -n techo5-v1.6.30-ja.1 -D bin
gh attestation verify bin/echod-arm --repo unotovive/hey-muse --source-ref refs/tags/v1.6.30-ja.1
```
Expected: ワークフロー成功、attestation 検証成功。CI が失敗したら直してタグを打ち直す（`git tag -d` と `git push origin :refs/tags/v1.6.30-ja.1`）。

- [ ] **Step 4: rootfs を作る**

```bash
PREBUILT_DAEMON=bin/echod-arm tools/linux/deploy-rootfs.sh --out bin/techo5-rootfs-v1.6.30-ja.1.tar.gz --version v1.6.30-ja.1
tar -tzf bin/techo5-rootfs-v1.6.30-ja.1.tar.gz | grep -E '^(\./)?vendor/.' && echo "STOP: vendor tree inside" || echo "vendor なし"
```
Expected: tarball ができ、`vendor` ツリーを含まない（Amazon の配布物を同梱しないため）。

- [ ] **Step 5: マニフェストを作って署名する**

```bash
V=v1.6.30-ja.1; R=https://github.com/unotovive/hey-muse/releases
(cd echod && go run ./cmd/mkmanifest -version $V -title "hey-muse $V" -notes "上流 TECHO5 v1.6.30 に友人版の日本語化・Muse 対応を載せた初回リリース。第 1 世代 (checkers) 用ブートイメージは上流 v1.6.30 のもの（カーネル GPL-2.0、ソースは上流リポジトリ参照）。" \
  -release-url $R/tag/$V -from $R/download/$V \
  -arm ../bin/echod-arm -rootfs-arm ../bin/techo5-rootfs-$V.tar.gz -asset ../bin/techo5-boot-checkers-$V.img \
  -out ../bin/manifest.json -sign-key ~/.config/techo5-release/sign.key)
(cd bin && sha256sum echod-arm techo5-rootfs-$V.tar.gz techo5-boot-checkers-$V.img manifest.json manifest.json.sig > SHA256SUMS)
python3 -I -c "import sys; sys.path.insert(0,'tools'); import techo5lib, json; print(techo5lib.RELEASE_KEY)"
```
Expected: `manifest.json` と `manifest.json.sig` ができる。

- [ ] **Step 6: 公開して外から確認する**

```bash
gh release create $V --repo unotovive/hey-muse --title $V --latest --notes-file <(echo "上流 v1.6.30 追従の初回リリース。詳細は docs/ja/release.md。") \
  bin/echod-arm bin/techo5-rootfs-$V.tar.gz bin/techo5-boot-checkers-$V.img bin/manifest.json bin/manifest.json.sig bin/SHA256SUMS
curl -sL $R/latest/download/manifest.json | grep '"version"'
```
Expected: `"version": "v1.6.30-ja.1"`。さらに `tools/techo5lib.py` の Release 取得関数（`install-show.py` が `Release(REPO, tag, work)` で使うもの）で署名検証付きに取れることを 1 行の Python で確認する。

- [ ] **Step 7: 手順書を書いてコミット**

`docs/ja/release.md` に Step 1〜6 を手順として書く（鍵の保管、`--latest` の意味、ブートイメージの由来と GPL 表記）。
```bash
git add docs/ja/release.md NOTICE && git commit -m "リリース手順（WSL）を日本語で追加し、ブートイメージの由来を NOTICE に記す

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_013XoetZKpfRq13qk4th5rxf" && git push
```

---

### Task 5: 端末のブートローダー解除（amonet、LineageOS v0.7）

**Files:**
- Create: `docs/ja/setup.md`（第 1 部「端末の解除」。Windows/WSL の USB 受け渡し、amonet、TWRP、LineageOS、各段階の確認コマンドと「ここで止まったら」）

**Interfaces:**
- Produces: `fastboot getvar product` が `CHECKERS`、`unlock_status: true`、TWRP 3.7 系、LineageOS checkers v0.7 が入った端末。Task 6 が使う。

- [ ] **Step 1: Windows 側に usbipd を入れる（PowerShell 管理者）**

WSL から: `powershell.exe -Command "Start-Process powershell -Verb RunAs -ArgumentList 'winget install --id dorssel.usbipd-win -e --accept-source-agreements --accept-package-agreements'"`
Run: `powershell.exe -Command "usbipd --version"`
Expected: 版が出る。

- [ ] **Step 2: WSL 側の道具と USB シリアルの対応を確認する**

```bash
sudo apt install -y android-sdk-platform-tools python3-serial python3-pyusb
zcat /proc/config.gz | grep -E 'CONFIG_USB_ACM=|CONFIG_USB_SERIAL=|CONFIG_USB_SERIAL_OPTION='
adb version && fastboot --version
```
Expected: `CONFIG_USB_ACM=y`（または `m`）。無ければ amonet は WSL で動かないので、Step 4 を Windows ネイティブ Python（MediaTek VCOM ドライバー）で行う判断にする。

- [ ] **Step 3: Echo を WSL に渡す**

端末を USB データケーブルで PC に接続して:
```bash
powershell.exe -Command "usbipd list"
# BUSID を控えてから（管理者）:
powershell.exe -Command "Start-Process powershell -Verb RunAs -ArgumentList 'usbipd bind --busid <BUSID>'"
powershell.exe -Command "usbipd attach --wsl --busid <BUSID> --auto-attach"
lsusb
```
Expected: `lsusb` に Amazon または MediaTek のデバイスが見える。`--auto-attach` は端末が BROM/fastboot/TWRP で再列挙されても付け直すために必須（このコマンドは繋ぎっぱなしで動かす。別端末のシェルで実行）。

- [ ] **Step 4: amonet で解除する**

```bash
mkdir -p ~/downloads-untrusted/amonet && cd ~/downloads-untrusted/amonet
git clone -b mt8163-checkers --depth 1 https://github.com/R0rt1z2/amonet .
cat README.md   # checkers の手順と、BROM に入れる操作（Qiita の事例では分解・はんだ付け不要、電源+音量上下の長押しで FASTBOOT）
```
README の手順どおり `bootrom-step.sh` → 端末の再起動 → `fastboot-step.sh` を実行する。各コマンドの出力をログに残す（`script -q ~/amonet-$(date +%F).log`）。
Expected: README が「done」相当を出し、端末が TWRP で起動する。**途中で USB を抜かない・電源を切らない**（LK/preloader を書き換えるため中断でブリック）。

- [ ] **Step 5: 解除を確認する**

```bash
fastboot devices && fastboot getvar product 2>&1 | head -2 && fastboot getvar unlock_status 2>&1 | head -1
```
Expected: `product: CHECKERS`、`unlock_status: true`。

- [ ] **Step 6: LineageOS 18.1 checkers v0.7 を入手する**

XDA スレッド（https://xdaforums.com/t/rom-unofficial-11-checkers-lineageos-18-1-for-the-amazon-echo-show-5-2019.4763475/）または bengris32 の GitHub リリース `lineage-18.1-checkers-v0.7` から `lineage-18.1-20260904-UNOFFICIAL-checkers.zip` を `~/downloads-untrusted/lineage/` に置く。XDA のダウンロードにアカウントが要る場合は施主に依頼し、ファイルを受け取る。
Run: `python3 -I -c "import zipfile,sys; z=zipfile.ZipFile(sys.argv[1]); print(len(z.namelist()))" ~/downloads-untrusted/lineage/lineage-18.1-20260904-UNOFFICIAL-checkers.zip`
Expected: エントリ数が表示される（破損していない）。`tools/install-show.py` の `lineage_board()` が `checkers` と判定することは Task 6 のインストーラーが確認する。

- [ ] **Step 7: 手順書に書いてコミット**

`docs/ja/setup.md` の第 1 部に Step 1〜6 と失敗時の対処（`usbipd attach` が外れる、BROM に入らない、`restricted on locked hw`）を書く。
```bash
git add docs/ja/setup.md && git commit -m "導入手順書（第 1 部: 端末の解除）を追加

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_013XoetZKpfRq13qk4th5rxf" && git push
```

---

### Task 6: 端末への初回インストールと受け入れ 1・2

**Files:**
- Modify: `docs/ja/setup.md`（第 2 部「TECHO5 の導入」）
- Create: `docs/ja/acceptance.md`（受け入れ確認の記録。1・2 をここで埋める）

**Interfaces:**
- Consumes: Task 4 のリリース、Task 5 の端末。
- Produces: `v1.6.30-ja.1` が動く端末。SSH（dropbear）で `root@<端末IP>` に入れる（鍵は installer が `--key` で置く）。

- [ ] **Step 1: TWRP からインストールする**

端末を TWRP で起動し（`adb reboot recovery` または amonet 後の状態）、`adb devices` が `recovery` を示すことを確認してから:
```bash
cd /home/unotovive/projects/musekusa/hey-muse
python3 tools/install-show.py --lineage-zip ~/downloads-untrusted/lineage/lineage-18.1-20260904-UNOFFICIAL-checkers.zip --wifi "<SSID>"
```
Expected: `checkers` を認識し、`v1.6.30-ja.1` のマニフェストを署名検証して取り、`techo5-boot-checkers-v1.6.30-ja.1.img` を焼き、rootfs をスロット a に入れ、再起動後に時計の待機画面が出る（受け入れ 1）。Wi-Fi パスワードは対話で入力。

- [ ] **Step 2: 到達性を確認する**

```bash
ssh -o StrictHostKeyChecking=no root@<端末IP> 'cat /etc/techo5-release 2>/dev/null; techo5 --version; slotctl status'
```
Expected: `v1.6.30-ja.1`、スロット a が `good` または `trial`。5 分後に再度 `slotctl status` で `good`。

- [ ] **Step 3: 停電復帰の確認（受け入れ 2）**

電源ケーブルを抜き、30 秒待って挿し直す。3 分以内に待機画面へ戻るか見る。
Expected: 戻る。戻らず FASTBOOT 表示で止まる場合は、`fastboot continue` で起動するかを試し、現象・ログ・回避策（Qiita の事例 https://qiita.com/amadanaxxx/items/65f4758d6c8904682182 を参照）を `docs/ja/acceptance.md` に書く。

- [ ] **Step 4: 記録してコミット**

`docs/ja/acceptance.md` に 10 項目の表を作り、1・2 の結果（日時、版、所要時間、現象）を記入。`docs/ja/setup.md` 第 2 部を書く。
```bash
git add docs/ja && git commit -m "導入手順書（第 2 部: インストール）と受け入れ記録 1・2 を追加

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_013XoetZKpfRq13qk4th5rxf" && git push
```

---

### Task 7: 音声認識サーバーの GPU 対応（recognizer.py）

**Files:**
- Modify: `tools/echo-asr/pc/recognizer.py`（`Recognizer.__init__`、`main()` の引数）
- Test: `tools/echo-asr/pc/test_recognizer.py`
- Modify: `tools/echo-asr/README.md`（GPU の起動例）

**Interfaces:**
- Produces: `model_settings(args) -> (model: str, device: str, compute_type: str)`（モジュール関数）。`Recognizer(model, device, compute_type)`。CLI: `--model`（既定 `large-v3-turbo`）、`--model-dir`（任意。指定時はそのパスを model に使う）、`--device {auto,cuda,cpu}`（既定 `auto`）、`--compute-type`（既定: cuda なら `float16`、cpu なら `int8`）。`auto` は `ctranslate2.get_cuda_device_count() > 0` で cuda。

- [ ] **Step 1: 失敗するテストを書く**

`test_recognizer.py` に追加:
```python
class ModelSettingsTest(unittest.TestCase):
    def test_defaults_follow_device(self):
        ns = app.parse_args(['--device', 'cuda'])
        self.assertEqual(app.model_settings(ns), ('large-v3-turbo', 'cuda', 'float16'))
        ns = app.parse_args(['--device', 'cpu'])
        self.assertEqual(app.model_settings(ns), ('large-v3-turbo', 'cpu', 'int8'))
    def test_model_dir_wins_and_explicit_compute_type_kept(self):
        ns = app.parse_args(['--device', 'cpu', '--model-dir', '/m/small', '--compute-type', 'int8_float16'])
        self.assertEqual(app.model_settings(ns), ('/m/small', 'cpu', 'int8_float16'))
    def test_auto_uses_cuda_when_available(self):
        ns = app.parse_args(['--device', 'auto'])
        with unittest.mock.patch.object(app, 'cuda_devices', return_value=1):
            self.assertEqual(app.model_settings(ns)[1:], ('cuda', 'float16'))
        with unittest.mock.patch.object(app, 'cuda_devices', return_value=0):
            self.assertEqual(app.model_settings(ns)[1:], ('cpu', 'int8'))
```

- [ ] **Step 2: 失敗を確認**

Run: `cd tools/echo-asr/pc && python3 -m unittest test_recognizer.ModelSettingsTest -v`
Expected: FAIL（`parse_args`/`model_settings` が無い）。

- [ ] **Step 3: 実装する**

`recognizer.py`: `def parse_args(argv=None)`（既存 `main()` の parser を切り出し、`--model-dir` を `required=False` に、`--model`、`--device`、`--compute-type` を追加）、`def cuda_devices() -> int`（`ctranslate2` の import 失敗は 0）、`def model_settings(ns)`、`Recognizer.__init__(self, model, device, compute_type)` が `WhisperModel(model, device=device, compute_type=compute_type, cpu_threads=6 if device=='cpu' else 0, ...)` を呼ぶ。`main()` は `model_settings` の結果で `Recognizer` を作る。既存の `Recognizer(model_dir)` 呼び出しがテストにあれば新しい引数に直す。

- [ ] **Step 4: テスト**

Run: `cd tools/echo-asr/pc && python3 -m unittest -v`
Expected: 既存と新規がすべて PASS。

- [ ] **Step 5: 母艦に GPU 版 faster-whisper を入れ、実音声で確認する**

```bash
python3 -m venv ~/.venvs/muse-asr && ~/.venvs/muse-asr/bin/pip install faster-whisper nvidia-cublas-cu12 nvidia-cudnn-cu12
~/.venvs/muse-asr/bin/python -c "import ctranslate2; print(ctranslate2.get_cuda_device_count())"
LD_LIBRARY_PATH=$(~/.venvs/muse-asr/bin/python -c 'import nvidia.cublas.lib, nvidia.cudnn.lib, os; print(os.path.dirname(nvidia.cublas.lib.__file__)+":"+os.path.dirname(nvidia.cudnn.lib.__file__))') \
  ~/.venvs/muse-asr/bin/python tools/echo-asr/pc/recognizer.py --bind 0.0.0.0 --allow <端末IP> --device cuda &
```
Expected: `1`（GPU 1 枚）。サーバーが起動し、初回に `large-v3-turbo` をダウンロードする。`tools/echo-asr/README.md` にあるクライアント例か `test_recognizer.py` のソケット手順で日本語の WAV を 1 本送り、日本語の文字列が返る。

- [ ] **Step 6: コミット**

```bash
git add tools/echo-asr && git commit -m "音声認識サーバーに --device/--compute-type/--model を追加し GPU 既定にする

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_013XoetZKpfRq13qk4th5rxf"
```

---

### Task 8: 母艦の基盤（WSL ネットワーク、systemd、Windows 自動起動）

**Files:**
- Create: `tools/mothership/systemd/muse-asr.service`、`tools/mothership/systemd/muse-bridge.service`（任意起動）、`tools/mothership/install.sh`、`tools/mothership/windows/setup-mothership.ps1`
- Modify: `tools/mothership/README.md`（WSL/systemd/自動起動の節）、`docs/ja/setup.md`（第 3 部「母艦」）

**Interfaces:**
- Consumes: Task 7 の `recognizer.py` と venv `~/.venvs/muse-asr`。
- Produces: `systemctl is-active muse-asr` が `active`。端末から `<PCのLAN IP>:50024` に TCP で届く。Windows 再起動後も同じ。

- [ ] **Step 1: WSL を mirrored にし、ファイアウォールを開ける（PowerShell）**

```bash
powershell.exe -Command "Add-Content -Path \$env:USERPROFILE\.wslconfig -Value \"`n[wsl2]`nnetworkingMode=mirrored`n\""
powershell.exe -Command "Get-Content \$env:USERPROFILE\.wslconfig"
powershell.exe -Command "Start-Process powershell -Verb RunAs -ArgumentList 'New-NetFirewallRule -DisplayName muse-asr -Direction Inbound -Protocol TCP -LocalPort 50024 -Action Allow; New-NetFirewallHyperVRule -Name muse-asr -DisplayName muse-asr -Direction Inbound -VMCreatorId \"{40E0AC32-46A5-438A-A0B2-2B479E8F2E90}\" -Protocol TCP -LocalPorts 50024; powercfg /change standby-timeout-ac 0'"
```
既存の `.wslconfig` に `[wsl2]` 節があれば二重にせず追記する。その後 `powershell.exe -Command "wsl --shutdown"` を実行し、WSL を開き直す。
Run: `ip -4 addr show | grep -v 127.0.0.1 | grep inet`
Expected: Windows と同じ LAN アドレス（mirrored）。

- [ ] **Step 2: systemd ユニットと install.sh を書く**

`muse-asr.service`: `ExecStart=%h/.venvs/muse-asr/bin/python %h/projects/musekusa/hey-muse/tools/echo-asr/pc/recognizer.py --bind 0.0.0.0 --device auto $ASR_ARGS`、`EnvironmentFile=%h/.config/hey-muse/asr.env`（`ASR_ARGS="--allow <端末IP>"` と `LD_LIBRARY_PATH`）、`Restart=on-failure`、`RestartSec=5`、`WantedBy=multi-user.target`。`User=` は install.sh で実ユーザーに置換。`muse-bridge.service` は `speech_bridge.py --bind 0.0.0.0 --allowed-network <LAN>/24` に `SPEECH_BRIDGE_TOKEN` を `bridge.env` から渡す（有効化は任意）。
`install.sh`: ユニットを `/etc/systemd/system/` へコピー（`sudo`）、`~/.config/hey-muse/asr.env` が無ければ雛形を作る、`systemctl daemon-reload && systemctl enable --now muse-asr`。

Run: `bash tools/mothership/install.sh && systemctl is-active muse-asr && journalctl -u muse-asr -n 5 --no-pager`
Expected: `active`、ログにモデル読み込み完了。

- [ ] **Step 3: Windows ログオン時に WSL を起こし、起こしたままにする**

`tools/mothership/windows/setup-mothership.ps1`: `schtasks /Create /F /TN "hey-muse WSL" /SC ONLOGON /RL HIGHEST /TR "wsl.exe -d <distro> --exec /bin/sh -c 'sleep infinity'"`（セッションを 1 つ保ち、WSL のアイドル終了を防ぐ。`<distro>` は `wsl -l -q` の値で置換）。実行: `powershell.exe -ExecutionPolicy Bypass -File tools/mothership/windows/setup-mothership.ps1`。
Run: `powershell.exe -Command "schtasks /Query /TN 'hey-muse WSL' /V /FO LIST | Select-String 'Task To Run'"`
Expected: 登録済み。

- [ ] **Step 4: 再起動して確認（受け入れ 9 の前半）**

Windows を再起動し、ログオン後 2 分待って WSL から `systemctl is-active muse-asr`。別の LAN 機器（端末が無ければスマホの TCP テストアプリか、Windows の `Test-NetConnection <LAN IP> -Port 50024`）で到達を見る。
Expected: `active`、`TcpTestSucceeded : True`。

- [ ] **Step 5: 文書とコミット**

`tools/mothership/README.md` と `docs/ja/setup.md` 第 3 部に Step 1〜4 を書く。`asr.env` の例は値をダミーで。
```bash
git add tools/mothership docs/ja && git commit -m "母艦の systemd ユニットと導入スクリプト、WSL mirrored と Windows 自動起動の手順を追加

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_013XoetZKpfRq13qk4th5rxf"
```

---

### Task 9: 家電バックエンドの抽象化と Nature Remo 実装（端末内 Go）

**Files:**
- Create: `echod/internal/lib/standalone/backend.go`、`echod/internal/lib/standalone/natureremo.go`
- Modify: `echod/internal/lib/standalone/home.go`（`settings` に `Backend`、`Calendars` はそのまま。`Dashboard` の `environment` と `Appliance`/`CheckTargets` をバックエンド経由に）、`echod/internal/lib/standalone/switchbot.go`（`Appliance`・環境・検査の本体を `switchBot` 型へ移す）、`echod/internal/cli/echod/home.go`（バックエンド名を表示）、`tools/standalone-home/deploy_usb.py`（`natureremo.env` があれば転送、`switchbot.env` が無くても進む）、`tools/standalone-home/README.md`
- Test: `echod/internal/lib/standalone/natureremo_test.go`（新規）、`home_test.go`（既存を通す）

**Interfaces:**
- Produces:
  ```go
  // backend.go
  type backend interface {
      appliance(ctx context.Context, cfg settings, key, action string, value int) error
      environment(ctx context.Context, cfg settings) (*Environment, error)
      check(ctx context.Context, cfg settings) (int, error)
  }
  func (h *Home) backend(cfg settings) backend   // cfg.Backend: "" か "switchbot" → switchBot、"nature_remo" → natureRemo、他はエラーを返す errBackend
  func (h *Home) Appliance(ctx context.Context, key, action string, value int) error   // 公開 API。value は温度（0 で未指定）
  ```
  `settings.Backend string `json:"backend"``。`settings.RemoLocal bool `json:"remo_local"``（ローカル API の枠。v1 では読み取って `home-check` に表示するだけで、送信は Cloud のみ）。Remo の `voice_devices` は `{"living": {"name": "<Remo の nickname>", "type": "LIGHT"}, "ac": {"name": "...", "type": "AC"}}`。
  Remo の `action`: `on`/`off`（LIGHT は `button=on|off`、AC の off は `button=power-off`、AC の on は直近の `settings.mode`（無ければ `auto`）で `operation_mode` 送信）、`cool`/`heat`（`operation_mode=cool|warm`、`value>0` なら `temperature`）、`temp`（現在の mode を保ち `temperature=value`）。
  Remo の資格情報: `<dir>/natureremo.env` の `NATURE_REMO_TOKEN=...`。API ベース `https://api.nature.global`、`Authorization: Bearer`。`GET /1/appliances` と `GET /1/devices` は 60 秒キャッシュ。429 は `Retry-After`（無ければ 300 秒）だけ送信を止める。
  Environment: `GET /1/devices` の最初の `newest_events` から `te.val`→Temperature、`hu.val`→Humidity。CO2/Battery は nil。
  Task 10 は `Appliance(ctx, key, action, value)` を呼ぶ。

- [ ] **Step 1: 失敗するテストを書く**

`natureremo_test.go`（`home_test.go` の `transportFunc`/`response` を再利用）:
```go
func remoHome(t *testing.T, onPost func(path, body string)) (*Home, *int) {
	gets := 0
	h := &Home{dir: t.TempDir(), client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer remo-token" { t.Fatal("missing bearer") }
		if r.Method == "POST" { b, _ := io.ReadAll(r.Body); onPost(r.URL.Path, string(b)); return response(`{}`), nil }
		gets++
		switch r.URL.Path {
		case "/1/appliances":
			return response(`[{"id":"L1","type":"LIGHT","nickname":"リビング照明","light":{"state":{"power":"on"}}},{"id":"A1","type":"AC","nickname":"エアコン","settings":{"mode":"cool","temp":"26"}}]`), nil
		case "/1/devices":
			return response(`[{"id":"D1","newest_events":{"te":{"val":23.5},"hu":{"val":55}}}]`), nil
		}
		t.Fatalf("unexpected GET %s", r.URL.Path); return nil, nil
	})}}
	os.WriteFile(filepath.Join(h.dir, "natureremo.env"), []byte("NATURE_REMO_TOKEN=remo-token\n"), 0600)
	os.WriteFile(filepath.Join(h.dir, "home-local.json"), []byte(`{"backend":"nature_remo","calendars":[],"voice_devices":{"living":{"name":"リビング照明","type":"LIGHT"},"ac":{"name":"エアコン","type":"AC"}}}`), 0600)
	return h, &gets
}

func TestRemoLightAndAirconCommands(t *testing.T) {
	var paths, bodies []string
	h, gets := remoHome(t, func(p, b string) { paths = append(paths, p); bodies = append(bodies, b) })
	ctx := context.Background()
	for _, c := range []struct{ key, action string; value int; path, body string }{
		{"living", "off", 0, "/1/appliances/L1/light", "button=off"},
		{"ac", "off", 0, "/1/appliances/A1/aircon_settings", "button=power-off"},
		{"ac", "cool", 26, "/1/appliances/A1/aircon_settings", "operation_mode=cool&temperature=26"},
		{"ac", "heat", 0, "/1/appliances/A1/aircon_settings", "operation_mode=warm"},
		{"ac", "temp", 24, "/1/appliances/A1/aircon_settings", "operation_mode=cool&temperature=24"},
	} {
		if err := h.Appliance(ctx, c.key, c.action, c.value); err != nil { t.Fatalf("%v: %v", c, err) }
		if paths[len(paths)-1] != c.path || bodies[len(bodies)-1] != c.body { t.Errorf("%v: got %s %s", c, paths[len(paths)-1], bodies[len(bodies)-1]) }
	}
	if *gets != 1 { t.Errorf("appliances fetched %d times, want 1 (60 s cache)", *gets) }
}

func TestRemoRateLimitedSendsNothing(t *testing.T) // 最初の GET が 429 (Retry-After: 60) → Appliance はエラー、POST 数 0、二度目の呼び出しも GET せずエラー
func TestRemoMissingTokenFails(t *testing.T)      // natureremo.env 無し → エラー、HTTP 呼び出し 0
func TestRemoEnvironmentAndCheck(t *testing.T)    // Dashboard(ctx,"environment","") → Temperature 23.5, Humidity 55, CO2 nil; CheckTargets → 2
func TestUnknownBackendIsRefused(t *testing.T)    // backend:"xyz" → Appliance がエラー
```

- [ ] **Step 2: 失敗を確認**

Run: `cd echod && go test ./internal/lib/standalone/ -run 'Remo|UnknownBackend' -v`
Expected: コンパイルエラー（`Appliance` の引数数、`backend` 不在）。

- [ ] **Step 3: SwitchBot を `switchBot` 型に移し、`backend` を導入する**

`backend.go` に interface と `(h *Home) backend(cfg)`。`switchbot.go` の `Appliance` 本体を `func (b switchBot) appliance(ctx, cfg, key, action string, value int) error` に（`value != 0` や `cool/heat/temp` は `errors.New("SwitchBot backend does not support this action")`）。`Dashboard` の `environment` 分岐と `CheckTargets` の家電部分をバックエンドへ。シーン・電源ボタン（`power`/`scene`）は SwitchBot 固有のまま `Home` に残す（Remo では `cfg.Scenes`/`Controls` が空で何も出ない）。
Run: `go test ./internal/lib/standalone/ -run SwitchBot -v`
Expected: 既存 `TestSwitchBotDispatch` が PASS（呼び出し側を新しい `Appliance(ctx, key, action, 0)` に直す）。

- [ ] **Step 4: `natureRemo` を実装する**

`natureremo.go`: `type natureRemo struct{ h *Home }`、`Home` に `remoMu sync.Mutex; remoAppliances []remoAppliance; remoAppliancesAt time.Time; remoDevices []remoDevice; remoDevicesAt time.Time; remoRetryAfter time.Time` を足す。`func (b natureRemo) api(ctx, method, path string, form url.Values, dst any) error`（`application/x-www-form-urlencoded`、6 秒タイムアウト、429 処理）。`appliance()` は上の Interfaces の規則で `url.Values` を組み立てる（キー順は `operation_mode`、`temperature`、`button` の順で固定し、テストの文字列と一致させる。`url.Values.Encode()` はキーをソートするのでそのままで一致する）。

- [ ] **Step 5: テスト**

Run: `cd echod && go test ./internal/lib/standalone/ -v && go vet ./...`
Expected: 全 PASS。

- [ ] **Step 6: home-check、deploy_usb.py、README**

`cli/echod/home.go`: 最初に `Backend: <name>` を出力。`deploy_usb.py` の `config`: `~/natureremo.env`（または `--remo-env` で指定）があれば `natureremo.env` として送る。`switchbot.env` と `calendar-feeds.txt` は存在するときだけ送る。`tools/standalone-home/README.md` に Remo の `home-local.json` 例とトークン取得先（https://home.nature.global/）を書く。
Run: `python3 -I -m py_compile tools/standalone-home/deploy_usb.py`
Expected: エラーなし。

- [ ] **Step 7: コミット**

```bash
git add echod tools/standalone-home && git commit -m "家電バックエンドを抽象化し Nature Remo（Cloud API）を端末内に実装

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_013XoetZKpfRq13qk4th5rxf"
```

---

### Task 10: エアコンの定型句（冷房・暖房・温度）

**Files:**
- Modify: `echod/internal/feature/voice/appliances.go`（`applianceCommand` に `value int`、`parseAppliance`）、`echod/internal/feature/voice/hall.go:116`（`standalone.Device.Appliance(ctx, command.target, command.action, command.value)`、母艦経路の payload に `value`）
- Test: `echod/internal/feature/voice/appliances_test.go`

**Interfaces:**
- Consumes: Task 9 の `Appliance(ctx, key, action string, value int)`。
- Produces: `applianceCommand{target, action, label string; value int}`。新しい action `cool`、`heat`、`temp`。ラベルは `エアコンの冷房`、`エアコンの暖房`、`エアコンの26度設定`（受け付け文は既存どおり「…を受け付けました。」）。

- [ ] **Step 1: 失敗するテストを書く**

`appliances_test.go` の `TestApplianceCommands` の肯定表に追加（既存の `[2]string` を `struct{target, action string; value int}` に広げる）:
```go
"エアコンを冷房にして": {"ac", "cool", 0}, "冷房つけて": {"ac", "cool", 0}, "暖房をつけて": {"ac", "heat", 0},
"エアコンを暖房にして": {"ac", "heat", 0}, "エアコンを26度にして": {"ac", "temp", 26}, "エアコン２４度にして": {"ac", "temp", 24},
"冷房を消して": {"ac", "off", 0}, "暖房消して": {"ac", "off", 0},
```
否定表に追加: `"エアコンを26度にしないで"`, `"冷房にしてって言われた"`, `"「暖房をつけて」と言って"`, `"エアコンを何度にする？"`, `"エアコンを100度にして"`（16〜30 度の範囲外は命令にしない）。

- [ ] **Step 2: 失敗を確認**

Run: `cd echod && go test ./internal/feature/voice/ -run TestApplianceCommands -v`
Expected: FAIL。

- [ ] **Step 3: 実装**

`parseAppliance`: 既存の正規化（句点・「ください」除去、否定・引用・疑問の除外）を通した後に、`^(?:エアコン(?:を|の)?)?(冷房|暖房)(?:に(?:して|切り替えて)|を?(?:つけて|入れて))$` → cool/heat、`^エアコン(?:を)?([0-9０-９]{2})度に(?:して|設定して)$` → temp（全角数字を半角に、16〜30 のみ）、`^(冷房|暖房)(?:を)?(消して|切って|止めて)$` → off。

- [ ] **Step 4: テスト**

Run: `cd echod && go test ./internal/feature/voice/ -run 'Appliance|Hall' -v && go build ./...`
Expected: PASS。

- [ ] **Step 5: コミット**

```bash
git add echod/internal/feature/voice && git commit -m "エアコンの冷房・暖房・温度の定型句を家電操作に追加

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_013XoetZKpfRq13qk4th5rxf"
```

---

### Task 11: Google Calendar API（端末内 Go）と OAuth 同意ツール

**Files:**
- Create: `echod/internal/lib/standalone/google.go`、`tools/standalone-home/google_auth.py`、`tools/standalone-home/test_google_auth.py`、`tools/standalone-home/requirements.txt`（`google-auth-oauthlib`、`google-api-python-client`）
- Modify: `echod/internal/lib/standalone/calendar.go`（`refresh()` の取得元切り替え）、`echod/internal/lib/standalone/home.go`（`settings.Calendars` に `ID`）、`tools/standalone-home/deploy_usb.py`（`google-token.json` の転送）、`tools/standalone-home/README.md`、`docs/ja/setup.md`（第 4 部「Google カレンダー」: Cloud プロジェクト作成、OAuth クライアント（デスクトップ）、テストユーザー登録、同意、トークン転送）
- Test: `echod/internal/lib/standalone/google_test.go`

**Interfaces:**
- Produces:
  ```go
  type googleToken struct {
      ClientID     string `json:"client_id"`
      ClientSecret string `json:"client_secret"`
      RefreshToken string `json:"refresh_token"`
  }
  func (h *Home) googleAccess(ctx context.Context, tok googleToken) (string, error)   // POST https://oauth2.googleapis.com/token、メモリに期限 60 秒前までキャッシュ
  func (h *Home) googleEvents(ctx context.Context, tok googleToken, calendarID, name string, from, to time.Time) ([]Event, error)
  ```
  `settings.Calendars []struct{ ID, Name string }`。`refresh()`: `<dir>/google-token.json` が読めれば全カレンダーを API で取得（ID 必須。空ならエラー）、無ければ従来の `calendar-feeds.txt`。
  API: `GET https://www.googleapis.com/calendar/v3/calendars/{url.PathEscape(id)}/events?singleEvents=true&orderBy=startTime&timeMin=<RFC3339>&timeMax=<RFC3339>&maxResults=100[&pageToken=]`。`items[]` の `status=="cancelled"` は捨てる。`start.date` があれば終日（`Day=true`、Start/End はその日の `time.Local` 0 時）。`summary` 空は「予定」。
  `google_auth.py`: `--client <OAuth クライアント JSON>`、`--out`（既定 `~/.config/hey-muse/google-token.json`）。`InstalledAppFlow.run_local_server(port=0)` で同意し、`token_payload(creds) -> dict` を書く。終わりに `calendarList.list` の `id` と `summary` を表示する。

- [ ] **Step 1: 失敗するテストを書く（Go）**

`google_test.go`:
```go
func googleHome(t *testing.T, tokenStatus int, tokenBody string, pages map[string]string) (*Home, *int) {
	refreshes := 0
	h := &Home{dir: t.TempDir(), client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "oauth2.googleapis.com" {
			refreshes++
			r.ParseForm()
			if r.PostForm.Get("grant_type") != "refresh_token" || r.PostForm.Get("refresh_token") != "rt" || r.PostForm.Get("client_id") != "cid" { t.Fatal("bad refresh form") }
			return &http.Response{StatusCode: tokenStatus, Body: io.NopCloser(strings.NewReader(tokenBody)), Header: make(http.Header)}, nil
		}
		if r.Header.Get("Authorization") != "Bearer at" { t.Fatal("missing access token") }
		q := r.URL.Query()
		if q.Get("singleEvents") != "true" || q.Get("maxResults") != "100" { t.Fatal("missing query") }
		body, ok := pages[r.URL.Path+"?pageToken="+q.Get("pageToken")]
		if !ok { t.Fatalf("unexpected %s", r.URL) }
		return response(body), nil
	})}}
	os.WriteFile(filepath.Join(h.dir, "google-token.json"), []byte(`{"client_id":"cid","client_secret":"cs","refresh_token":"rt"}`), 0600)
	os.WriteFile(filepath.Join(h.dir, "home-local.json"), []byte(`{"calendars":[{"id":"a@group.calendar.google.com","name":"家族"},{"id":"me@gmail.com","name":"自分"}]}`), 0600)
	return h, &refreshes
}

func TestGoogleAgendaMapsTimedAllDayAndPages(t *testing.T) // 2 カレンダー、1 つは pageToken で 2 ページ。終日 (start.date) は Day=true、cancelled は除外、Start 順、refreshes==1
func TestGoogleRefreshFailureUsesCache(t *testing.T)      // agenda-cache.json を未来の Until で置き、token 400 {"error":"invalid_grant"} → Result.OK=true, Stale=true, キャッシュの件数。キャッシュ無しならエラー
func TestGoogleTokenIsReusedUntilExpiry(t *testing.T)     // expires_in 3600 → 2 回の agenda で refreshes==1
```

- [ ] **Step 2: 失敗を確認**

Run: `cd echod && go test ./internal/lib/standalone/ -run Google -v`
Expected: コンパイルエラー。

- [ ] **Step 3: 実装**

`google.go` に型と 2 関数、`Home` に `googleMu sync.Mutex; googleAccessToken string; googleUntil time.Time`。`calendar.go` の `refresh()`: `google-token.json` を `h.read` できれば `googleEvents` を各カレンダーで呼ぶ（1 つでも失敗したら全体エラー → `agenda()` が既存どおりキャッシュに落ちる）。既存の iCal 経路は無変更。`home.go` の `settings.Calendars` に `ID string`。

- [ ] **Step 4: テスト**

Run: `cd echod && go test ./internal/lib/standalone/ -v`
Expected: Google 3 件と既存 `TestCalendarAtomicRefreshAndRecurrence`（iCal）が PASS。

- [ ] **Step 5: 失敗するテストを書く（Python）**

`tools/standalone-home/test_google_auth.py`:
```python
import unittest, types, google_auth
class PayloadTest(unittest.TestCase):
    def test_payload_has_only_what_the_device_needs(self):
        creds = types.SimpleNamespace(client_id='cid', client_secret='cs', refresh_token='rt', token='access-should-not-ship')
        self.assertEqual(google_auth.token_payload(creds), {'client_id': 'cid', 'client_secret': 'cs', 'refresh_token': 'rt'})
    def test_missing_refresh_token_is_an_error(self):
        with self.assertRaises(SystemExit):
            google_auth.token_payload(types.SimpleNamespace(client_id='cid', client_secret='cs', refresh_token=None))
```
Run: `cd tools/standalone-home && python3 -m unittest test_google_auth -v` → FAIL（モジュール無し）。

- [ ] **Step 6: `google_auth.py` を実装し、テスト**

`google_auth.py`: `SCOPES = ['https://www.googleapis.com/auth/calendar.readonly']`、`token_payload(creds)`（`refresh_token` が無ければ `sys.exit('同意画面で「オフラインアクセス」が付与されませんでした。prompt=consent で再実行してください')`）、`main()` は `InstalledAppFlow.from_client_secrets_file(args.client, SCOPES).run_local_server(port=0, prompt='consent', access_type='offline')`、`--out` へ 0600 で JSON 保存、`googleapiclient.discovery.build('calendar','v3',credentials=creds).calendarList().list().execute()` の `id`/`summary` を表示。google ライブラリの import は `main()` 内で行い、テストは依存なしで動くようにする。
Run: `cd tools/standalone-home && python3 -m unittest test_google_auth -v`
Expected: PASS。

- [ ] **Step 7: 施主の同意とトークン配置（施主の操作が要る）**

`docs/ja/setup.md` 第 4 部の手順（Google Cloud でプロジェクト → OAuth 同意画面「外部」+ テストユーザーに自分 → 認証情報「デスクトップアプリ」→ JSON を `~/.config/hey-muse/google-client.json` に保存）を書き、施主に依頼する。その後:
```bash
python3 -m venv ~/.venvs/muse-tools && ~/.venvs/muse-tools/bin/pip install -r tools/standalone-home/requirements.txt
~/.venvs/muse-tools/bin/python tools/standalone-home/google_auth.py --client ~/.config/hey-muse/google-client.json
```
Expected: ブラウザで同意後、`~/.config/hey-muse/google-token.json` ができ、カレンダー一覧が出る。施主に使うカレンダーを選んでもらい、`home-local.json` の `calendars` に `id`/`name` を書く。

- [ ] **Step 8: deploy_usb.py、README、コミット**

`deploy_usb.py config`: `~/.config/hey-muse/google-token.json` があれば `google-token.json` として送る。README に Google 経路の説明。
```bash
git add echod tools/standalone-home docs/ja && git commit -m "Google Calendar API を端末内の予定取得元として追加し、OAuth 同意ツールを用意

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_013XoetZKpfRq13qk4th5rxf"
```

---

### Task 12: 母艦不在時の音声認識フォールバック

**Files:**
- Modify: `echod/internal/feature/voice/muse.go`（`ask()` の `idle-home.json` 分岐を削除、連続失敗の遮断器）、`echod/internal/lib/wyoming/wyoming.go:33`（`dialTimeout` を 2 秒に）
- Test: `echod/internal/feature/voice/muse_local_test.go`

**Interfaces:**
- Produces: `viaMuse` に `sttFailures int; sttSkipUntil time.Time; now func() time.Time`（`newViaMuse` で `time.Now`）。定数 `sttFailuresToSkip = 2`、`sttSkipFor = time.Minute`。ローカル認識が失敗（`ctx` 起因以外）するたび `sttFailures++`、2 回で `sttSkipUntil = now+60s`。成功で 0 に戻す。`sttSkipUntil` 内は `m.transcribe` を呼ばず `m.link.Ask`（音声ノート）へ直行。失敗時は常に音声ノートへ（聞き返しは廃止）。

- [ ] **Step 1: 失敗するテストを書く**

`muse_local_test.go` に追加（既存テストの `viaMuse` 組み立てと偽 `transcribe`/`link` を流用）:
```go
func TestMuseSkipsLocalSTTAfterFailures(t *testing.T) {
	// transcribe は常にエラー。link.Ask（音声ノート）の呼び出し回数を数える。
	// 1 回目: transcribe 1 回 + Ask 1 回。2 回目: transcribe 2 回目 + Ask。3 回目（60 秒以内）: transcribe は増えず Ask。
	// now を 61 秒進めた 4 回目: transcribe が再び呼ばれる。transcribe が成功したら sttFailures が 0 に戻る。
}
func TestMuseFallsBackToVoiceNoteEvenWithIdleHome(t *testing.T) {
	// transcribe がエラーのとき、返事が「聞き取れませんでした…」ではなく link.Ask の結果になる。
}
```
各アサーションは `transcribeCalls`、`askCalls` の整数比較で書く。

- [ ] **Step 2: 失敗を確認**

Run: `cd echod && go test ./internal/feature/voice/ -run 'TestMuseSkipsLocalSTT|TestMuseFallsBackToVoiceNote' -v`
Expected: FAIL（3 回目で transcribe が呼ばれる、または聞き返し文が返る）。

- [ ] **Step 3: 実装**

`ask()`: `if m.now().Before(m.sttSkipUntil) { skip }`。`os.Stat("/data/misc/techo5/idle-home.json")` の分岐を削除し、`os` の import が不要なら外す。`wyoming.go` の `dialTimeout = 2 * time.Second`（`wyoming` のテストが時間に依存していないことを確認）。

- [ ] **Step 4: テスト**

Run: `cd echod && go test ./internal/feature/voice/ ./internal/lib/wyoming/ -race`
Expected: PASS。

- [ ] **Step 5: コミット**

```bash
git add echod && git commit -m "母艦不在時は録音を Muse へ送り、連続失敗後 60 秒はローカル認識を飛ばす

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_013XoetZKpfRq13qk4th5rxf"
```

---

### Task 13: 文単位ストリーミング読み上げと相槌

**Files:**
- Create: `echod/internal/feature/voice/sentences.go`、`echod/internal/feature/voice/sentences_test.go`、`echod/internal/feature/voice/tts_queue.go`、`echod/internal/feature/voice/tts_queue_test.go`
- Modify: `echod/internal/feature/voice/muse.go`（`answer()` と `say()`）、`echod/internal/feature/feedback/feedback.go`（`Thinking()`）、`echod/internal/hardware/speaker/tones.go`（`ThinkingSound()`）
- Test: `echod/internal/feature/voice/muse_test.go`

**Interfaces:**
- Produces:
  ```go
  // sentences.go
  type sentenceCursor struct{ spoken int; last string }
  // Next は全文の途中経過 full から、まだ返していない完結文を順に返す。final なら末尾の未完文も返す。
  // full の先頭 spoken 文字が last と異なる（サーバーが喋った部分を書き換えた）ときは何も返さない。
  func (c *sentenceCursor) Next(full string, final bool) []string
  func splitSentences(s string) (complete []string, rest string)   // 区切り: [。！？!?]+ と直後の閉じ括弧・引用符、または改行の連なり

  // tts_queue.go
  type ttsQueue struct{ ... }
  // fetch は 1 文を音声にして out へ流す（speakMuse と同じ形）。emit は再生側（evStreamAudio を post する）。
  func newTTSQueue(ctx context.Context, fetch func(ctx context.Context, text string, out func([]int16) error) error, emit func([]int16) error) *ttsQueue
  func (q *ttsQueue) Push(text string)              // 直列に並べる
  func (q *ttsQueue) Close() error                  // 全文の再生完了を待ち、最初のエラーを返す
  func (q *ttsQueue) FirstAudio() time.Time         // 最初の音声サンプルが emit された時刻。ゼロ値なら未再生
  ```
  先読みは 1 文: 文 n の再生中に文 n+1 の fetch だけ先行し、文 n+2 は文 n の再生が終わるまで始めない。
  `feedback.Thinking()`: `config.Quiet()` でなければ `speaker.Sound().Chime(speaker.ThinkingSound())`。`ThinkingSound()` は短い 2 音（`CancelSound` の定義に倣う）。
  `viaMuse` に `thinking func()`（既定 `feedback.Thinking`）と `thinkingAfter time.Duration`（既定 5 秒）。録音終了（`answer` 開始）から `thinkingAfter` 内に最初の音声が出なければ 1 回だけ呼ぶ。

- [ ] **Step 1: 失敗するテストを書く（文分割）**

`sentences_test.go`:
```go
func TestSplitSentences(t *testing.T) {
	for in, want := range map[string]struct{ complete []string; rest string }{
		"今日は晴れです。気温は": {[]string{"今日は晴れです。"}, "気温は"},
		"いいですね！「本当？」と言った。終わり": {[]string{"いいですね！", "「本当？」と言った。"}, "終わり"},
		"一行目\n二行目。":           {[]string{"一行目", "二行目。"}, ""},
		"3.5倍です。":            {[]string{"3.5倍です。"}, ""},
		"途中":                  {nil, "途中"},
	} { ... }
}
func TestSentenceCursorReturnsEachSentenceOnce(t *testing.T) {
	var c sentenceCursor
	got := c.Next("今日は晴れです。", false)            // ["今日は晴れです。"]
	got = c.Next("今日は晴れです。気温は20度", false)     // nil
	got = c.Next("今日は晴れです。気温は20度。明日は", false) // ["気温は20度。"]
	got = c.Next("今日は晴れです。気温は20度。明日は雨", true) // ["明日は雨"]
}
func TestSentenceCursorIgnoresRewrittenPrefix(t *testing.T) {
	var c sentenceCursor
	c.Next("今日は晴れです。", false)
	if got := c.Next("今日は雨です。気温は20度。", false); got != nil { t.Errorf("rewritten prefix spoke %q", got) }
}
```

- [ ] **Step 2: 失敗を確認 → 実装 → PASS**

Run: `cd echod && go test ./internal/feature/voice/ -run 'TestSplitSentences|TestSentenceCursor' -v`
Expected: FAIL → `sentences.go` 実装（`speech.Spoken` は呼び出し側で先に通す）→ PASS。

- [ ] **Step 3: 失敗するテストを書く（キュー）**

`tts_queue_test.go`（偽 fetch は文ごとにチャネルで「開始」「完了」を外から制御できるようにする）:
```go
func TestTTSQueuePlaysInOrderAndPrefetchesOne(t *testing.T)
// Push("A"),Push("B"),Push("C"): A の fetch 中に B の fetch が始まる。A の再生（emit）が終わるまで C の fetch は始まらない。emit の順序は A,B,C の順で混ざらない。
func TestTTSQueueStopsOnError(t *testing.T)     // B の fetch がエラー → Close() がそのエラーを返し、C は fetch されない
func TestTTSQueueStopsOnCancel(t *testing.T)    // ctx cancel → Close() が ctx.Err()
func TestTTSQueueFirstAudio(t *testing.T)       // 最初の emit 前はゼロ値、後は非ゼロ
```

- [ ] **Step 4: 失敗を確認 → 実装 → PASS**

Run: `cd echod && go test ./internal/feature/voice/ -run TestTTSQueue -race -v`
Expected: FAIL → `tts_queue.go` 実装 → PASS。

- [ ] **Step 5: 失敗するテストを書く（Muse のターン）**

`muse_test.go` に追加（既存 `TestMuseShowsReplyBeforeSettleAndSpeech` の道具を使う。偽 link が `ReplyText` → `ReplyText` → `Settled` → 返り値の順でイベントを出す）:
```go
func TestMuseSpeaksSentencesAsTheyArrive(t *testing.T) {
	// ReplyText "今日は晴れです。" → speak が "今日は晴れです。" で呼ばれる（Settled より前）
	// ReplyText "今日は晴れです。気温は20度" → speak は増えない
	// Settled "今日は晴れです。気温は20度。" → speak "気温は20度。"
	// 投稿イベントの順序は evHeard, evReplyPreview..., evReplyText, evStreamAudio..., evStreamEnd, evRunEnd
}
func TestMuseIgnoresRewrittenPrefix(t *testing.T) {
	// ReplyText "今日は晴れです。" 後に ReplyText "今日は雨です。気温は20度。" → "今日は晴れです。" のみ話し、"今日は雨です。" は話さない。Settled の全文で残りだけ話す
}
func TestMuseThinkingSoundAfterDelay(t *testing.T) {
	// thinkingAfter=10ms、link が 50ms 待ってから返す → thinking が 1 回。link が即答 → 0 回。
}
```

- [ ] **Step 6: 失敗を確認**

Run: `cd echod && go test ./internal/feature/voice/ -run 'TestMuseSpeaksSentences|TestMuseIgnoresRewritten|TestMuseThinking' -v`
Expected: FAIL。

- [ ] **Step 7: `answer()` を書き換える**

`ReplyText`: `for _, s := range cursor.Next(speech.Spoken(v.Text), false) { q.Push(s) }`。`MessageReady`: メッセージ完成時点の全文（既存の `spoken+"\n\n"+part` 相当）を `cursor.Next(…, true)` で確定。`Settled` と最終 `reply.Text`: `cursor.Next(speech.Spoken(text), true)`。`q.Close()` の後に既存の終了イベント。`say()` のチャンク化（`museChunk`）は `emit` 側に移す。表示用 `evReplyText` は従来どおり全文スナップショットを post する。既存 `TestMuseTurnIsReportedInOrder` などが期待するイベント順を崩さない。相槌は `time.AfterFunc(m.thinkingAfter, …)` で `q.FirstAudio().IsZero()` のときだけ、終了時に `Stop()`。

- [ ] **Step 8: テスト**

Run: `cd echod && go test ./internal/feature/voice/ -race -count=3 && go test ./internal/feature/feedback/ ./internal/hardware/speaker/`
Expected: 全 PASS、競合なし。

- [ ] **Step 9: コミット**

```bash
git add echod && git commit -m "Muse の返答を文単位で先読み読み上げし、5 秒超で相槌音を鳴らす

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_013XoetZKpfRq13qk4th5rxf"
```

---

### Task 14: 手のひらで読み上げを止める

**Files:**
- Create: `echod/internal/feature/display/cover.go`、`echod/internal/feature/display/cover_test.go`
- Modify: `echod/internal/feature/display/presence_screen.go:68-76`（`watchRoom`）
- Modify: `docs/ja/setup.md`（在室検知・ジェスチャー・消灯時間の設定場所）

**Interfaces:**
- Produces: `func coverStops(endRing func() bool, turnBusy func() bool, cancelTurn func()) bool`。鳴動があれば止めて true。無ければターン中なら `cancelTurn()` で true。どちらも無ければ false。`watchRoom` は `coverStops(ring.End, voice.Get().Busy, voice.Get().Cancel)` が true のとき `d.wake()`。

- [ ] **Step 1: 失敗するテストを書く**

`cover_test.go`:
```go
func TestCoverStopsRingBeforeTurn(t *testing.T) {
	canceled := 0
	if !coverStops(func() bool { return true }, func() bool { return true }, func() { canceled++ }) || canceled != 0 { t.Fatal("ring should take the cover") }
	if !coverStops(func() bool { return false }, func() bool { return true }, func() { canceled++ }) || canceled != 1 { t.Fatal("turn should be canceled") }
	if coverStops(func() bool { return false }, func() bool { return false }, func() { canceled++ }) || canceled != 1 { t.Fatal("nothing to stop") }
}
```

- [ ] **Step 2: 失敗 → 実装 → PASS**

Run: `cd echod && go test ./internal/feature/display/ -run TestCoverStops -v`
Expected: FAIL → `cover.go` と `watchRoom` の差し替え → PASS。`display` は既に `feature/voice` を import しているので循環しない。

- [ ] **Step 3: 設定画面の確認と文書**

`sheet.go:924` の `presence` 行と `SetGestures`/`SetScreenOff` の行を読み、設定画面で在室検知・ジェスチャー・消灯分数が切り替えられることを確認して `docs/ja/setup.md` に書く（上流 v1.6.30 の減光設定の場所も）。

- [ ] **Step 4: コミット**

```bash
git add echod docs/ja && git commit -m "カメラを手で覆うと鳴動または Muse の読み上げを止める

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_013XoetZKpfRq13qk4th5rxf"
```

---

### Task 15: 地震情報の割り込み表示

**Files:**
- Create: `echod/internal/lib/p2pquake/message.go`、`message_test.go`、`client.go`、`client_test.go`、`testdata/551.json`、`testdata/552.json`、`testdata/556.json`、`testdata/561.json`（P2P 地震情報 API v2 仕様の例から作る）
- Create: `echod/internal/config/quake.go`、`echod/internal/feature/quake/quake.go`、`quake_test.go`、`echod/internal/feature/display/quake_page.go`、`quake_page_test.go`、`echod/internal/cli/echod/quake.go`（`techo5 tools quake-test --code 551|552|556` が `testdata` 相当のサンプルを `quake.Get().Offer` に渡す。実機の受け入れ 7 用）
- Modify: `echod/internal/config/config.go`（`Quake Quake` フィールド）、`echod/internal/component/all/all.go`（import）、`echod/internal/feature/display/render.go`（状態フィールドと優先チェーン、赤時計の条件）、`echod/internal/feature/display/display.go`（スナップショット、タップ、`Changed` の監視）、`echod/internal/feature/display/sheet.go`（地震情報のオン／オフ行）
- Modify: `docs/ja/setup.md`（地震情報の設定と免責）

**Interfaces:**
- Produces:
  ```go
  // lib/p2pquake
  type Alert struct {
      ID        string
      Code      int        // 551, 552, 556
      Issued    time.Time  // "time" (2006/01/02 15:04:05.000, JST)
      Place     string     // earthquake.hypocenter.name（551/556）
      MaxScale  int        // 551: earthquake.maxScale（10..70、-1 は不明）
      Magnitude float64    // 551
      Depth     int        // 551（km、-1 不明）
      Prefs     []string   // 551: points[].pref の重複除去、552: areas[].name、556: areas[].pref
      Grades    []string   // 552: areas[].grade
      Cancelled bool       // 552/556
  }
  func Parse(data []byte) (Alert, bool, error)   // ok=false は対象外コード（561 など）。JSON 不正は error
  func ScaleLabel(scale int) string               // 30→"3", 45→"5弱", 50→"5強", 55→"6弱", 60→"6強", 70→"7", -1→"不明"
  var backoffBase = time.Second                   // テストで短縮
  func Run(ctx context.Context, url string, on func(Alert))  // gorilla/websocket で接続し、切断時 1,2,4…最大 60 秒で再接続。ctx で終了

  // config
  type Quake struct { On bool; MinScale int; Prefs []string; Chime bool }   // json: quake / on, min_scale, prefs, chime
  func (q Quake) QuakeMinScale() int  // 0 なら 30
  // Writer: Quake().On(bool), MinScale(int), Prefs([]string), Chime(bool)

  // feature/quake
  func Get() *Feature
  func (f *Feature) Offer(a p2pquake.Alert)                     // フィルタ・重複除去・表示開始・Changed.Emit・Chime
  func (f *Feature) Showing() (p2pquake.Alert, time.Time, bool) // 表示中の警報と消える時刻
  func (f *Feature) Dismiss()
  func shows(cfg config.Quake, a p2pquake.Alert) bool
  func holdFor(a p2pquake.Alert) time.Duration                  // 551: 90s, 552: 90s, 556: 60s
  func Sample(code int) p2pquake.Alert                           // quake-test 用の固定サンプル（埋め込み JSON を Parse）
  ```
  `shows`: `Cancelled` は false。551 は `MaxScale >= cfg.QuakeMinScale()` または `Prefs` と `cfg.Prefs` に共通があるとき。552 と 556 は常に true。重複除去は `ID` を 24 時間保持。
  表示: `render.go` の状態に `quake p2pquake.Alert; showQuake bool`。`display.go` のスナップショット（`s.reminder, s.showReminder = remind.Get().Showing()` の隣）で `s.quake, _, s.showQuake = quake.Get().Showing()`。優先チェーンは `if s.ring.any()` の直後に `if s.showQuake { r.quakePage(s); return }`。赤時計の早期 return 条件に `!s.showQuake` を足す。タップ（`display.go:902` 付近）で `quake.Get().Dismiss()`。`quake.Get().Changed.Listen(func(struct{}) { d.wake() })`。
  `quakePage`: 1 行目に種別（「地震情報 震度X」「緊急地震速報（警報）※参考情報」「津波予報」）、2 行目に震源地と発生時刻、3 行目に M と深さ（551）または地域（552/556）、下端に「タップで閉じる」と「出典: P2P地震情報」。556 には「気象庁の警報そのものではありません」を添える。

- [ ] **Step 1: 失敗するテストを書く（Parse）**

`message_test.go`:
```go
func TestParse551(t *testing.T)  // testdata/551.json: Code 551, MaxScale 40, Place, Magnitude, Depth, Prefs に県名が重複なく、Issued が JST の時刻
func TestParse552And556(t *testing.T)
func TestParseIgnoresOtherCodes(t *testing.T) // 561 → ok=false, err=nil。`{"code":` の壊れた JSON → err
func TestScaleLabel(t *testing.T)
```
`testdata/*.json` は https://www.p2pquake.net/develop/json_api_v2/ の各コードの例を元に、個人情報を含まない形で作る。

- [ ] **Step 2: 失敗 → 実装 → PASS**

Run: `cd echod && go test ./internal/lib/p2pquake/ -run 'TestParse|TestScaleLabel' -v`

- [ ] **Step 3: 失敗するテストを書く（Run）**

`client_test.go`: `httptest.NewServer` に `websocket.Upgrader` を置き、1 回目の接続で `testdata/551.json` を送って閉じ、2 回目で `testdata/556.json` を送る。`backoffBase = 10 * time.Millisecond`。
```go
func TestClientReconnectsAndDelivers(t *testing.T) // on が 551 と 556 を順に受け、接続回数が 2 以上。ctx cancel で Run が返る
func TestClientSkipsBadFrames(t *testing.T)        // 不正 JSON と 561 を挟んでも on は 551 だけ受ける
```

- [ ] **Step 4: 失敗 → 実装 → PASS**

Run: `cd echod && go test ./internal/lib/p2pquake/ -race -v`

- [ ] **Step 5: 失敗するテストを書く（config と feature）**

`config` の既存テストの流儀で `TestQuakeDefaults`（`QuakeMinScale()==30`、Writer の往復）。
`quake_test.go`（`Feature` を `now func() time.Time` と `chime func()` 差し替え可能に）:
```go
func TestShows(t *testing.T)  // 表: 551/30 と MinScale 30 → true、551/20 → false、551/20 で Prefs 一致 → true、556 → true、552 → true、Cancelled → false
func TestOfferDedupesAndExpires(t *testing.T) // 同 ID 2 回で Changed 1 回。Showing は 551 で 90 秒後に消える。Chime は cfg.Chime のときだけ 1 回
func TestDismiss(t *testing.T)
```

- [ ] **Step 6: 失敗 → 実装 → PASS**

Run: `cd echod && go test ./internal/config/ ./internal/feature/quake/ -v`
`feature/quake` の `Run(ctx)` は `config.Get().Quake.On` を監視し、オンの間だけ `p2pquake.Run(ctx, "wss://api.p2pquake.net/v2/ws", f.Offer)` を動かす（`remind` の `init()`/`Get()` の登録方法に倣う）。

- [ ] **Step 7: 表示を足す**

`cli/echod/quake.go` に `quake-test` を足し（`tools.go` の `AddCommand` に登録）、`quake_page.go` を実装し、`quake_page_test.go` で `display` 内の既存描画テスト（`faces_test.go` 等）と同じ道具を使って、551 と 556 のサンプルを描画してパニックせず背景以外のピクセルがあることを確認する。`render.go`、`display.go`、`sheet.go` を Interfaces のとおり変更。
Run: `cd echod && go test ./internal/feature/display/ -run 'Quake|Faces' -v && go build ./... && GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -o /tmp/echod-arm ./cmd/echod`
Expected: PASS、ビルド成功。

- [ ] **Step 8: 全体テストとコミット**

Run: `cd echod && go test ./... && go test -tags dot ./... && go test -tags spot ./...`
```bash
git add echod docs/ja && git commit -m "P2P 地震情報を端末が直接受信し、待機画面に割り込み表示する

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_013XoetZKpfRq13qk4th5rxf"
```

---

### Task 16: リリース `v1.6.30-ja.2`、端末設定の配置、受け入れ 3〜10

**Files:**
- Modify: `docs/ja/setup.md`（第 5 部「端末設定の配置」、第 6 部「Muse の契約とペアリング」、推奨する Muse への指示文、地震・在室の設定）、`docs/ja/acceptance.md`（3〜10）、`README.md`（日本語の導入部をこの派生の説明に）
- 成果物: Task 4 と同じ構成の `v1.6.30-ja.2`

**Interfaces:**
- Consumes: Task 7〜15 のすべて。
- Produces: 端末が `v1.6.30-ja.2` で動き、`/data/misc/techo5/standalone-home/` に `enabled`、`home-local.json`（`backend: "nature_remo"`、`calendars` に ID）、`natureremo.env`（機種判明まで空でも可）、`google-token.json` が置かれている。受け入れ 3〜10 の記録。

- [ ] **Step 1: リリース `v1.6.30-ja.2`**

`docs/ja/release.md` の手順で Task 4 と同じく作る（ブートイメージは同じ上流のものを `techo5-boot-checkers-v1.6.30-ja.2.img` として同梱）。
Run: `curl -sL https://github.com/unotovive/hey-muse/releases/latest/download/manifest.json | grep '"version"'`
Expected: `v1.6.30-ja.2`。

- [ ] **Step 2: 端末を更新する**

端末の設定画面の更新確認で `v1.6.30-ja.2` を入れる。または `HOST=<端末IP> tools/linux/deploy-rootfs.sh --version v1.6.30-ja.2 --install --reboot`。
Run: `ssh root@<端末IP> 'techo5 --version; slotctl status'`
Expected: `v1.6.30-ja.2`、再起動後 5 分で `good`。

- [ ] **Step 3: 端末設定を置く**

`~/.config/hey-muse/home-local.json` を `tools/mothership/home-local.example.json` と `tools/standalone-home/README.md` に従って作り（`backend`、`calendars`、`voice_devices`、`key` は空でよい）、`tools/standalone-home/deploy_usb.py config --serial <SERIAL>`（Task 9・11 で拡張済み）で転送。端末の USB コンソールで `touch /data/misc/techo5/standalone-home/enabled` と `techo5 tools home-check`。
Expected: `Backend: nature_remo`、`Calendar refreshed: N events`、（Remo トークン未設定なら）家電の検査はエラーで止まるので、その行までの出力を記録。

- [ ] **Step 4: 受け入れ 5〜8（Muse 無しでできるもの）**

- 5: Task 9 と Task 10 のテスト結果を記録（`go test -run 'Remo|ApplianceCommands' -v` の出力）。
- 6: 待機画面に予定が出ること。終日・繰り返し予定を 1 つずつ作って翌取得（最大 5 分）で確認。
- 7: 端末の USB コンソールか SSH で `techo5 tools quake-test --code 551`（Task 15）を実行し、割り込み表示と 90 秒後の復帰、`--code 556` で 60 秒と「参考情報」の表記を確認。
- 8: 設定画面で在室検知オン・消灯 5 分にして無人 5 分で消灯、近づいて点灯。アラームを鳴らして手で覆い止まることを確認。
各結果を `docs/ja/acceptance.md` に記録。

- [ ] **Step 5: Muse の契約とペアリング（施主）**

`docs/ja/setup.md` 第 6 部に、Muse 契約 → gadgets.muse.ai で SDK トークン → 端末の設定画面でトークン入力 → Muse アプリから BLE ペアリング、の手順を友人の `docs/muse.md` を元に日本語で書く。Muse への推奨指示文（「結論を先に、2〜3 文で、日本語で」）も載せる。施主に実施を依頼し、完了を待つ。

- [ ] **Step 6: 受け入れ 3・4・9・10**

- 3: 母艦の `muse-asr` を止め（`systemctl stop muse-asr`）、「Hey Muse、今日の天気は」で回答が読み上げられる。ログに `local recognition unavailable` と音声ノート送信があること。再開後は `using local speech recognition`。
- 4: 母艦ありで 10 回、録音終了から最初の音声までの時間を `journalctl`／端末ログの `first_audio_ms` から取り、中央値を記録。比較用に `thinkingAfter` などは触らず、ストリーミング無効は Task 13 以前の版（`v1.6.30-ja.1`）の値を使ってよい。
- 9: Task 8 Step 4 の結果に加え、再起動後に端末からの認識が通ることを 3 に準じて確認。
- 10: `cd echod && go test ./... && go test -tags dot ./... && go test -tags spot ./...`、Python 各ディレクトリの `python3 -m unittest`、ARMv7 ビルド、GitHub Actions の `v1.6.30-ja.2` の run が成功。

- [ ] **Step 7: 記録と README、コミット**

```bash
git add docs README.md && git commit -m "導入手順書と受け入れ記録を完成させ、README をこの派生の説明にする

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_013XoetZKpfRq13qk4th5rxf" && git push
```

- [ ] **Step 8: 施主への最終報告**

署名鍵のバックアップ依頼、Nature Remo の機種判明後に `natureremo.env` と `voice_devices` を置けば動く旨、未実施の受け入れ項目（あれば）と理由、次の上流タグへの追従手順（`git fetch upstream --tags && git merge v1.6.X`、Task 2 と同じ）を書く。
