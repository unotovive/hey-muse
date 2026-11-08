# リリース手順（WSL）

このフォーク `unotovive/hey-muse` のリリースを、WSL（Ubuntu）で作る手順です。英語の `docs/releasing-the-fork.md` は Mac 前提で、鍵の場所・リモート名・版名の付け方が古くなっています。この文書が現行の手順です。

初回リリースは `v1.6.30-ja.2` です。上流 TECHO5 v1.6.30 に、このフォークの日本語化と Muse 対応を載せています。

注記: `v1.6.30-ja.1` は、最初のタグの CI が失敗したため成果物を公開していません。そのため初回は `v1.6.30-ja.2` です。タグを打ち直せないときは版名を 1 つ繰り上げるので、版名は飛ぶことがあります。

## 版名

`vX.Y.Z-ja.N` と付けます。`vX.Y.Z` は土台にした上流のリリース、`N` はこのフォークのリリースごとに 1 つずつ上げます。端末は `v1.6.30-ja.3` を `v1.6.30-ja.2` より新しいと判断し、`v1.6.31-ja.1` はどちらよりも新しいと判断します（`echod/internal/update/version.go`）。

## 署名鍵の保管

- 秘密鍵は `~/.config/techo5-release/sign.key` に置きます。32 バイトの ed25519 シードを base64 にした 1 行のファイルで、`mkmanifest -sign-key` がこれを読みます。ディレクトリは 0700、ファイルは 0600 にします。
- 公開鍵は同じ場所の `sign.pub` です。同じ鍵が `tools/techo5lib.py` の `RELEASE_KEY` と `echod/internal/update/trust.go` に入っています。端末と `install-show.py` は、この鍵で署名されたマニフェストしか受け付けません。
- 鍵は WSL の外にもバックアップしてください（パスワードマネージャーや暗号化した USB など）。失くすと、インストール済みの端末が受け取れる更新を出せなくなります。新しい鍵に替えるには、全端末へ手作業で入れ直すしかありません。
- 鍵はリポジトリにコミットしません。GitHub Actions の secret にもしません。署名は自分のコンピューターだけで行います。

## 事前準備（初回だけ）

Ubuntu（WSL）に、rootfs を作るためのパッケージを入れます。

```bash
sudo apt install -y git python3 qemu-user-static binfmt-support uidmap
cd ~/projects/musekusa/hey-muse
python3 tools/fetch-inputs.py          # inputs/ に apk.static、Alpine の最小 rootfs、ウェイクワードのモデルなど
mkdir -p ~/apk && cp inputs/apk.static ~/apk/apk.static
ls /proc/sys/fs/binfmt_misc/qemu-arm   # 存在すれば ARM のエミュレーションが効いている
```

`fetch-inputs.py` は、固定された版の Alpine パッケージが配布から消えていると、近い版に置き換えて続行します（その旨を表示します）。

Go は `~/sdk/go` にあります。シェルごとに次を実行します。

```bash
export PATH=$HOME/sdk/go/bin:$HOME/go/bin:$PATH
```

## リリースに入るもの

| ファイル | 読む人 | 作り方 |
|---|---|---|
| `echod-arm` | マニフェストの `binaries.arm` | タグの CI（`build.yml`）が作り、attestation が付く |
| `techo5-rootfs-<版>.tar.gz` | 端末（空きスロットの更新）、`install-show.py` | `deploy-rootfs.sh` が WSL で作る |
| `techo5-boot-checkers-<版>.img` | `install-show.py`（新しい端末のみ） | 上流 v1.6.30 のものをそのまま使う |
| `manifest.json`、`manifest.json.sig` | 端末と `install-show.py` | `mkmanifest` |
| `SHA256SUMS` | 人が手で確かめる | `sha256sum` |

これらは `bin/` に置きます。`bin/` は `.gitignore` に入っていて、コミットしません。

## 手順

以降 `V` は版名です（例: `V=v1.6.30-ja.2`）。

### 1. 上流の資産を取り、ヘルパーとブートイメージを取り出す

rootfs に入る C/Rust 製のヘルパー（`techo5-aec`、`techo5-librespot`、`techo5-ffmpeg`）は、WSL では作らず、上流のリリースの rootfs から取り出します。ダウンロードしたものは信頼できないデータとして扱い、専用のディレクトリに置いて `SHA256SUMS` を確かめます。

```bash
mkdir -p ~/downloads-untrusted/techo5-v1.6.30 && cd ~/downloads-untrusted/techo5-v1.6.30
gh release download v1.6.30 --repo HuskerMinion/techo5 \
  -p 'rootfs-v1.6.30.tar.gz' -p 'techo5-boot-checkers-v1.6.30.img' \
  -p manifest.json -p manifest.json.sig -p SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing      # すべて OK になること
tar -tzf rootfs-v1.6.30.tar.gz | grep 'usr/local/bin/techo5-'   # パスが ./usr/... か usr/... かを確認
mkdir x && tar -xzf rootfs-v1.6.30.tar.gz -C x \
  ./usr/local/bin/techo5-aec ./usr/local/bin/techo5-librespot ./usr/local/bin/techo5-ffmpeg
cd ~/projects/musekusa/hey-muse && mkdir -p bin
for h in aec librespot ffmpeg; do cp ~/downloads-untrusted/techo5-v1.6.30/x/usr/local/bin/techo5-$h bin/techo5-$h-arm; done
cp ~/downloads-untrusted/techo5-v1.6.30/techo5-boot-checkers-v1.6.30.img bin/techo5-boot-checkers-$V.img
```

注意すること。

- 第 1 世代（checkers）向けの上流 rootfs の資産名は `rootfs-v1.6.30.tar.gz` です（`techo5-rootfs-v1.6.30.tar.gz` ではありません）。athena 用と skylight 用は別名で別物です。
- 上流 v1.6.30 の rootfs には `techo5-pico` が入っていません。内蔵の音声ヘルパー（pico）は入らないまま作ります。必要になったら `tools/linux/build-pico.sh`（zig が要ります）で作り、`bin/techo5-pico-arm` に置きます。
- `deploy-rootfs.sh` は、`techo5-shairport-sync`、`techo5-nqptp`、`tailscaled` も必須とし、無いと止まります。同じ要領で上流の rootfs から `bin/techo5-shairport-sync-arm`、`bin/techo5-nqptp-arm`、`bin/tailscaled-arm64` に取り出します。`tailscaled` のライセンス（上流 rootfs の `usr/share/licenses/tailscale/LICENSE`）は `bin/tailscaled-arm64.LICENSE` に置きます。

### 2. タグを打ち、CI でテストと ARMv7 ビルドを通す

```bash
git tag $V && git push origin $V
gh run watch --repo unotovive/hey-muse $(gh run list --repo unotovive/hey-muse --workflow build.yml -L1 --json databaseId -q '.[0].databaseId')
gh run download --repo unotovive/hey-muse -n techo5-$V -D bin
gh attestation verify bin/echod-arm --repo unotovive/hey-muse --source-ref refs/tags/$V
```

ワークフローは、テストを通常・`dot`・`dot,donut`・`spot`・`panel` の各ビルドで動かしたあと、`echod-arm` を作って attestation を付けます。ワークフローも attestation も成功することを確かめます。

CI が失敗したら、直してコミットし、タグを打ち直します。まだ公開していない版なら次のようにします。

```bash
git tag -d $V && git push origin :refs/tags/$V
git tag $V && git push origin $V
```

手元で先に、CI と同じ構成を流しておくと無駄が出ません（`cd echod` してから）。

```bash
go test ./... && go test -tags dot ./... && go test -tags dot,donut ./... \
  && go test -tags spot ./... && go test -tags panel ./...
```

`dot` と `panel` は通常のビルドと条件が違うため、通常のテストだけ通っても、ここで落ちることがあります。

### 3. rootfs を作る

`PREBUILT_DAEMON` に `bin/echod-arm` そのものを指定すると、スクリプトが同じファイルを `cp` して止まります。CI の成果物を別の場所（例: `bin/ci/`）にコピーして指定します。実行権限がないので `bash` 経由で起動します。

```bash
mkdir -p bin/ci && cp bin/echod-arm bin/ci/echod-arm
PREBUILT_DAEMON=bin/ci/echod-arm bash tools/linux/deploy-rootfs.sh --out bin/techo5-rootfs-$V.tar.gz --version $V
tar -tzf bin/techo5-rootfs-$V.tar.gz | grep -E '^(\./)?vendor/.' && echo "STOP: vendor tree inside" || echo "vendor なし"
```

`PREBUILT_DAEMON` に CI の `echod-arm` を指定すると、手元でビルドし直さず、CI が attestation した同じバイナリが rootfs に入ります。`vendor` ツリー（LineageOS の `/vendor`）は Amazon の配布物なので、入っていたら公開してはいけません。各端末が自分のものを使います。

### 4. マニフェストを作って署名する

```bash
R=https://github.com/unotovive/hey-muse/releases
(cd echod && go run ./cmd/mkmanifest -version $V -title "hey-muse $V" -notes "<変更点>" \
  -release-url $R/tag/$V -from $R/download/$V \
  -arm ../bin/echod-arm -rootfs-arm ../bin/techo5-rootfs-$V.tar.gz -asset ../bin/techo5-boot-checkers-$V.img \
  -out ../bin/manifest.json -sign-key ~/.config/techo5-release/sign.key)
(cd bin && sha256sum echod-arm techo5-rootfs-$V.tar.gz techo5-boot-checkers-$V.img manifest.json manifest.json.sig > SHA256SUMS)
python3 -I -c "import sys; sys.path.insert(0,'tools'); import techo5lib; print(techo5lib.RELEASE_KEY)"
```

最後の行は、`techo5lib.py` に入っている公開鍵を表示します。`~/.config/techo5-release/sign.pub` と一致していることを目で確かめます。

### 5. 公開して外から確認する

```bash
gh release create $V --repo unotovive/hey-muse --title $V --latest --notes "<変更点>" \
  bin/echod-arm bin/techo5-rootfs-$V.tar.gz bin/techo5-boot-checkers-$V.img \
  bin/manifest.json bin/manifest.json.sig bin/SHA256SUMS
curl -sL $R/latest/download/manifest.json | grep '"version"'
```

`"version"` が `$V` と一致することを確かめます。

`--latest` は、そのリリースを「最新」として扱う指定です。端末の安定版チャンネルは `releases/latest/download/manifest.json` を読みます。GitHub はプレリリースを最新とは数えないので、`--latest` を付けないと端末が新しい版を見つけられません。逆に、古い版を後から作り直すときに `--latest` を付けると、端末が古い版を最新と見なします。

`install-show.py` が使う署名検証付きの取得は、`tools/techo5lib.py` の `Release` クラスで確かめられます（`install-show.py` は `Release(REPO, tag, work)` で使います）。

## ブートイメージの由来と GPL 表記

第 1 世代 Echo Show 5（checkers）のブートイメージ `techo5-boot-checkers-<版>.img` は、上流 TECHO5 v1.6.30 のものをそのまま使い、このフォークではカーネルを変えていません。中身は Linux 4.9.337（arm64）で、GPL-2.0 です。

- 上流のイメージを再配布するので、このフォークの署名鍵で署名したマニフェストの `-asset` に載せます。取得時に上流の `SHA256SUMS` と照らして、改ざんがないことを確かめます。
- カーネルのソースの場所、設定、パッチは `NOTICE` の「Linux kernel」の項に書いてあります。ソースは `github.com/amazon-oss/android_kernel_amazon_mt8163`（ブランチ `cronos/lineage-18.1`、コミット `8d928c5176cc`）と、上流リポジトリの `tools/linux/patches` です。上流のリリースには、サードパーティのソースをまとめた `techo5-third-party-sources-<版>.tar.gz` も付いています。
- このフォークが新しくカーネルを変えたり、別のイメージを配布したりするときは、`NOTICE` のその項を更新してから公開します。

## 確認チェックリスト

- 上流の資産が `SHA256SUMS` と一致した。
- `build.yml` が成功し、`gh attestation verify` が成功した。
- rootfs に `vendor` ツリーが入っていない。
- `manifest.json` の署名が `techo5lib` で検証できる。
- `releases/latest/download/manifest.json` の `"version"` が新しい版になっている。
- 鍵のバックアップが WSL の外にある。
