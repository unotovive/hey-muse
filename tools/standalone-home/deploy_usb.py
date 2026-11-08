"""Stage files over the serial-guarded Echo Show console without logging secrets."""
import argparse
import base64
import hashlib
import io
import json
from pathlib import Path
import secrets
import sqlite3
import sys
import tarfile
import tempfile
import time

BASE = Path.home() / "Documents/muse_on_echo"
HERE = Path(__file__).resolve().parent
DEVICE = "/data/misc/techo5/standalone-home"
sys.path.insert(0, str(BASE / "assets/hey-muse/tools"))
from techo5lib import Console, CONSOLE_TECHO5, SerialPort


def send(console, archive):
    assert console.find(), console.waiting_hint()
    size = archive.seek(0, 2)
    archive.seek(0)
    digest = hashlib.file_digest(archive, "sha256").hexdigest()
    archive.seek(0)
    port = SerialPort(console.port)

    def write(data):
        while data:
            count = port._wintypes.DWORD()
            if not port.k32.WriteFile(port.handle, data, len(data), port._ctypes.byref(count), None) or not count.value:
                raise OSError("USB write failed")
            data = data[count.value:]

    def until(marker, seconds):
        buf = b""
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            buf += port.read()
            if marker in buf:
                return buf
            time.sleep(.002)
        raise TimeoutError("Echo USB transfer timed out")

    tag = secrets.token_hex(6)
    try:
        port.write(b"\n")
        time.sleep(.2)
        while port.read():
            pass
        # Stage inside a root-only directory. Serial data is never echoed.
        cmd = (f"umask 077; mode=$(stty -g); stty raw -echo; printf '\\nR{tag}\\n'; "
               f"head -c {size} > {DEVICE}/stage.tar.gz; stty \"$mode\"; "
               f"sha256sum {DEVICE}/stage.tar.gz; printf '\\nD{tag}\\n'\n")
        write(cmd.encode())
        until(f"\nR{tag}\n".encode(), 8)
        while chunk := archive.read(32768):
            write(chunk)
        if digest.encode() not in until(f"D{tag}".encode(), 90):
            raise ValueError("USB checksum mismatch")
    finally:
        port.close()


def deploy(args):
    console = Console(args.serial, CONSOLE_TECHO5)
    def run(command, timeout=30):
        reply = console.run(f"({command}) && echo HOME_OK", timeout)
        if reply is None or "HOME_OK" not in reply.splitlines():
            raise RuntimeError("Echo command failed: " + str(reply))

    run(f"umask 077; mkdir -p {DEVICE}; chmod 700 {DEVICE}")
    # Spool secrets only in memory. The larger binary/audio archive contains no credentials.
    file = io.BytesIO() if args.part == "config" else tempfile.TemporaryFile()
    with file as archive, tarfile.open(fileobj=archive, mode="w:gz", compresslevel=2) as tar:
        def add(name, content, mode=0o600):
            info = tarfile.TarInfo(name)
            info.mode, info.size = mode, len(content)
            tar.addfile(info, io.BytesIO(content))

        if args.part == "config":
            bridge = BASE / "speech-bridge"
            sys.path.insert(0, str(bridge))
            import echo_home as live
            import hall_light as light
            cfg = json.loads((bridge / "home-local.json").read_text(encoding="utf-8-sig"))
            cfg.pop("codex", None)
            cfg["power_controls"] = {k: dict(label=v[0], icon=v[1], toggle=v[2], on=v[3], off=v[4])
                                     for k, v in live.POWER_NAMES.items()}
            if cfg.get("backend", "switchbot") in ("", "switchbot"):
                cfg["voice_devices"] = {k: dict(name=v[0], type=v[1]) for k, v in light.VOICE_DEVICES.items()}
                cfg["voice_scenes"] = {k: {op: name} for (k, op), name in light.VOICE_SCENES.items()}
                cfg["hall"] = dict(name="廊下の電気", type="Color Bulb", transport="cloud")
            # SwitchBot 以外では home-local.json の voice_devices / hall（Remo の nickname と type）をそのまま使う。
            add("home-local.json", json.dumps(cfg, ensure_ascii=False).encode())
            # 資格情報とカレンダー購読は存在するものだけ送る（Remo のみの構成では switchbot.env が無い）。
            for name, source in (("switchbot.env", Path.home() / "switchbot_token.env"),
                                 ("natureremo.env", args.remo_env or Path.home() / "natureremo.env"),
                                 ("calendar-feeds.txt", BASE / "calendar-feeds.txt")):
                if source.is_file():
                    add(name, source.read_bytes())
            with sqlite3.connect(f"file:{(bridge / 'home-requests.sqlite').as_posix()}?mode=ro", uri=True) as db:
                add("initial-power.json", json.dumps(dict(db.execute("select device,direction from power_last"))).encode())
        else:
            add("techo5.next", args.binary.read_bytes(), 0o755)
            for name in ("install.sh", "rollback.sh", "cleanup-legacy.sh"):
                add(name, (HERE / name).read_bytes().replace(b"\r\n", b"\n"), 0o700)
            if args.audio:
                files = list(args.audio.glob("*.pcm"))
                if not files:
                    raise ValueError("no prepared replies in audio directory")
                for path in files:
                    add("voice/" + path.name, path.read_bytes())
            # Read the previous service's SQLite using the PC's interpreter. The
            # installer verifies this snapshot again after the old writer stops.
            status = console.run(f"cd {DEVICE} && if test -f home-requests.sqlite && test ! -f power-last.json; then echo MIGRATE; else echo READY; fi")
            if status == "MIGRATE":
                encoded = console.run(f"cd {DEVICE} && test ! -s home-requests.sqlite-wal && test ! -s home-requests.sqlite-journal && base64 home-requests.sqlite", 40)
                if not encoded:
                    raise RuntimeError("could not read a consistent power-state snapshot")
                database = base64.b64decode("".join(encoded.split()), validate=True)
                with sqlite3.connect(":memory:") as db:
                    db.deserialize(database)
                    directions = dict(db.execute("select device,direction from power_last"))
                add("migration-power.json", json.dumps(directions).encode())
                add("migration-power.sha256", (hashlib.sha256(database).hexdigest() + "  home-requests.sqlite\n").encode())
            elif status != "READY":
                raise RuntimeError("could not inspect prior home settings")
        tar.close()
        send(console, archive)
        run(f"cd {DEVICE} && tar xzf stage.tar.gz && rm -f stage.tar.gz && sync", 65)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("part", choices=("config", "daemon"))
    parser.add_argument("--serial", required=True)
    for flag in ("audio", "binary", "remo-env"):
        parser.add_argument("--" + flag, type=Path, dest=flag.replace("-", "_"))
    args = parser.parse_args()
    if args.part == "daemon" and not args.binary:
        parser.error("daemon requires --binary")
    deploy(args)
    print("Staged and verified.")
