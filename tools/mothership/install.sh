#!/usr/bin/env bash
# 母艦（WSL の Ubuntu）へ muse-asr を systemd サービスとして入れる。何度実行しても安全。
# 使い方: bash tools/mothership/install.sh [--with-bridge]
# sudo はスクリプト内で必要な所だけ使う。sudo を付けて起動してもよい。
set -euo pipefail

with_bridge=0
for arg in "$@"; do
  case "$arg" in
    --with-bridge) with_bridge=1 ;;
    *) echo "不明な引数: $arg" >&2; exit 2 ;;
  esac
done

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
run_user="${SUDO_USER:-$(id -un)}"
run_home="$(getent passwd "$run_user" | cut -d: -f6)"
if [ "$run_user" = root ] || [ -z "$run_home" ]; then
  echo "root ではなく、サービスを動かす通常ユーザーで実行してください。" >&2
  exit 1
fi

sudo_cmd=""
[ "$(id -u)" -eq 0 ] || sudo_cmd="sudo"

if ! grep -qs '^systemd *= *true' /etc/wsl.conf; then
  echo "/etc/wsl.conf の [boot] に systemd=true がありません。追記して wsl --shutdown 後に再実行してください。" >&2
  exit 1
fi

venv_py="$run_home/.venvs/muse-asr/bin/python"
if [ ! -x "$venv_py" ]; then
  echo "仮想環境がありません: $venv_py（tools/echo-asr/README.md の手順で作ってください）" >&2
  exit 1
fi

conf_dir="$run_home/.config/hey-muse"
asr_env="$conf_dir/asr.env"
mkdir -p "$conf_dir"

# asr.env は無いときだけ雛形を作る（既存の値は上書きしない）。
if [ ! -e "$asr_env" ]; then
  ld_path="$("$venv_py" -c 'import nvidia.cublas.lib, nvidia.cudnn.lib; print(nvidia.cublas.lib.__path__[0]+":"+nvidia.cudnn.lib.__path__[0])' 2>/dev/null || true)"
  cat > "$asr_env" <<ENVEOF
# muse-asr の設定。--allow には端末（Echo）の IP を入れる（下は例のダミー値）。
ASR_ARGS="--allow 192.0.2.10"
LD_LIBRARY_PATH=$ld_path
ENVEOF
  chmod 600 "$asr_env"
  echo "雛形を作りました: $asr_env（--allow を端末の IP に直してください）"
fi
if [ "$run_user" != "$(id -un)" ]; then chown "$run_user" "$conf_dir" "$asr_env" 2>/dev/null || true; fi

render() { # $1=元ユニット $2=出力先
  sed -e "s|@USER@|$run_user|g" -e "s|@HOME@|$run_home|g" -e "s|@REPO@|$repo|g" "$1" | $sudo_cmd tee "$2" >/dev/null
}
units=(muse-asr)
[ "$with_bridge" -eq 1 ] && units+=(muse-bridge)
for u in "${units[@]}"; do
  render "$here/systemd/$u.service" "/etc/systemd/system/$u.service"
done

$sudo_cmd systemctl daemon-reload
$sudo_cmd systemctl enable --now muse-asr
if [ "$with_bridge" -eq 1 ]; then
  if [ -r "$conf_dir/bridge.env" ]; then
    $sudo_cmd systemctl enable --now muse-bridge
  else
    echo "muse-bridge は入れましたが、$conf_dir/bridge.env が無いため起動していません。" >&2
  fi
fi
systemctl is-active muse-asr
