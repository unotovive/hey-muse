"""Private Wyoming STT service for this Echo; audio is processed in RAM only."""
import argparse
import json
import logging
import logging.handlers
import os
import queue
import socket
import socketserver
import subprocess
import sys
import threading
import time
from array import array
from pathlib import Path


MAX_AUDIO = 30 * 16000 * 2
MAX_HEADER = 16384
FORMAT = {'rate': 16000, 'width': 2, 'channels': 1}
LOG = logging.getLogger('muse-recognition')

def read_exact(stream, size):
    data = stream.read(size)
    if len(data) != size:
        raise ValueError('incomplete event')
    return data

def read_event(stream):
    line = stream.readline(MAX_HEADER + 1)
    if not line:
        raise EOFError()
    if len(line) > MAX_HEADER or not line.endswith(b'\n'):
        raise ValueError('header too large')
    header = json.loads(line)
    dn, pn = header.get('data_length', 0), header.get('payload_length', 0)
    if type(dn) is not int or type(pn) is not int or not 0 <= dn <= MAX_HEADER or not 0 <= pn <= MAX_AUDIO:
        raise ValueError('event too large')
    data = json.loads(read_exact(stream, dn)) if dn else header.get('data', {})
    if not isinstance(data, dict):
        raise ValueError('invalid event data')
    return header['type'], data, read_exact(stream, pn)

def send_event(stream, kind, data):
    body = json.dumps(data, ensure_ascii=False).encode('utf-8')
    header = json.dumps({'type': kind, 'data_length': len(body)}).encode()+b'\n'
    stream.write(header+body)
    stream.flush()

SILENCE_PEAK = 33  # 従来の 0.001 × 32768 の切り上げ

def is_silent(pcm):
    """無音判定。16 bit リトルエンディアン PCM を numpy なしで読む。サンプル数 1600 未満も無音扱い。"""
    samples = array('h')
    samples.frombytes(pcm[:len(pcm) // 2 * 2])
    if len(samples) < 1600:
        return True
    return max(max(samples), -min(samples)) < SILENCE_PEAK

class WorkerTimeout(Exception):
    """応答が response_timeout 内に来なかった。TimeoutError にしない（Handler に握りつぶされるため）。"""

class WorkerExited(Exception):
    """認識プロセスの stdout が EOF になった（終了コードは問わない）。"""

LOAD_TIMEOUT = 60.0  # 起動からこの秒数が過ぎても ready が来なければ、次の要求で起動し直す

class _Spawn:
    """起動 1 回分の状態。ワーカーを差し替えても古い行や書き込みと取り違えないよう、起動ごとに別物を持つ。"""
    def __init__(self, proc):
        self.proc = proc
        self.responses = queue.Queue()
        self.ready = threading.Event()
        self.started = time.monotonic()
        self.writer = None  # 最後に始めた書き込みスレッド（打ち切られても書き続けている場合がある）

class WorkerProxy:
    """認識プロセス（worker.py）を必要なときだけ起こし、stdin/stdout の JSON 行で文字起こしを頼む。"""
    def __init__(self, command, response_timeout=10.0, load_timeout=LOAD_TIMEOUT):
        self.command, self.response_timeout, self.load_timeout = command, response_timeout, load_timeout
        self.lock = threading.Lock()
        self._spawn_lock = threading.Lock()
        self.spawn, self.next_id = None, 0

    @property
    def proc(self):
        return self.spawn.proc if self.spawn else None

    @property
    def pid(self):
        return self.proc.pid if self.proc else None

    def prepare(self):
        """生きたワーカーが無ければ起動して戻る（ready は待たない）。"""
        self._ensure()

    def _ensure(self):
        """今の _Spawn を返す。差し替えは _spawn_lock で守る（self.lock は長く持たれるので流用しない）。"""
        with self._spawn_lock:
            spawn = self.spawn
            if spawn is not None and spawn.proc.poll() is None and not spawn.ready.is_set() \
                    and time.monotonic() - spawn.started > self.load_timeout:
                LOG.warning('worker not ready after %s seconds, restarting', self.load_timeout)
                self._stop_locked()
            if self.spawn is None or self.spawn.proc.poll() is not None:
                self._release()
                env = {**os.environ, 'PYTHONUTF8': '1'}  # ワーカーの stderr を UTF-8 に揃える（Windows の文字化け防止）
                proc = subprocess.Popen(self.command, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                        stderr=subprocess.PIPE, env=env)
                spawn = _Spawn(proc)
                threading.Thread(target=self._read_stdout, args=(proc.stdout, spawn), daemon=True).start()
                threading.Thread(target=self._relay_stderr, args=(proc.stderr,), daemon=True).start()
                self.spawn = spawn
            return self.spawn

    @staticmethod
    def _read_stdout(stream, spawn):
        try:
            for line in stream:
                try:
                    message = json.loads(line)
                except ValueError:
                    LOG.warning('worker stdout is not JSON')
                    continue
                if not isinstance(message, dict):
                    continue
                if message.get('event') == 'ready':
                    spawn.ready.set()
                elif message.get('event') == 'fatal':
                    LOG.error('worker fatal: %s', message.get('error'))
                spawn.responses.put(message)
        finally:
            stream.close()
            spawn.responses.put(None)  # EOF の目印

    @staticmethod
    def _relay_stderr(stream):
        try:
            for line in stream:
                LOG.info('worker: %s', line.decode('utf-8', 'replace').rstrip())
        finally:
            stream.close()

    @staticmethod
    def _write(spawn, data, previous, failed):
        """stdin に 1 要求を書く。前の要求の書き込みが残っていれば、混ざらないよう書き終わりを待つ。"""
        if previous is not None:
            previous.join()
        try:
            spawn.proc.stdin.write(data)
            spawn.proc.stdin.flush()
        except (OSError, ValueError):  # ValueError: 後始末で閉じられた stdin
            failed.set()

    def _release(self):
        """終了済みワーカーの後始末（パイプを閉じて回収する）。"""
        if self.spawn is not None:
            proc = self.spawn.proc
            proc.wait()  # 先に終わりを待つ（書き込みスレッドが詰まっていても EPIPE で抜けてから閉じる）
            if self.spawn.writer is not None:
                self.spawn.writer.join()
            if proc.stdin:
                try:
                    proc.stdin.close()
                except OSError:
                    pass
        self.spawn = None

    def _stop_locked(self):
        """_spawn_lock を持った状態で、今のワーカーを止めて回収する。"""
        if self.spawn is None:
            return
        proc = self.spawn.proc
        proc.terminate()
        try:
            proc.wait(3)
        except subprocess.TimeoutExpired:
            proc.kill()
        self._release()

    def _discard_if_hung(self, spawn):
        """ready 済みで応答が来ない＝固まったとみなし、止めて捨てる（次の要求で起動し直す）。
        読み込み中（ready 前）のタイムアウトでは生かしておく（仕様 4.2）。"""
        if not spawn.ready.is_set():
            return
        with self._spawn_lock:
            if self.spawn is spawn:
                LOG.warning('worker did not answer after ready, restarting on next request')
                self._stop_locked()

    def transcribe(self, pcm, language):
        if is_silent(pcm):
            return ''
        if not self.lock.acquire(timeout=3):
            raise ValueError('recognizer busy')
        try:
            # 起動を含め、ここからの待ちをすべて response_timeout に収める
            deadline = time.monotonic() + self.response_timeout
            spawn = self._ensure()  # 以後は手元の参照だけを使う（他スレッドの prepare で差し替わっても安全）
            self.next_id += 1
            rid = self.next_id
            header = json.dumps({'id': rid, 'language': language, 'pcm_length': len(pcm)}).encode() + b'\n'
            # 読み込み中のワーカーは stdin を読まないので、大きな PCM の書き込みは詰まりうる。別スレッドで書いて打ち切る
            failed = threading.Event()
            writer = threading.Thread(target=self._write, args=(spawn, header + pcm, spawn.writer, failed), daemon=True)
            spawn.writer = writer
            writer.start()
            writer.join(max(0, deadline - time.monotonic()))
            if failed.is_set():
                raise WorkerExited('worker exited')
            if writer.is_alive():
                self._discard_if_hung(spawn)
                raise WorkerTimeout('no response within %s seconds' % self.response_timeout)
            while True:
                try:
                    message = spawn.responses.get(timeout=max(0, deadline - time.monotonic()))
                except queue.Empty:
                    self._discard_if_hung(spawn)
                    raise WorkerTimeout('no response within %s seconds' % self.response_timeout) from None
                if message is None:
                    raise WorkerExited('worker exited')
                if message.get('event') == 'fatal':
                    raise RuntimeError(str(message.get('error')))
                if message.get('id') != rid:
                    continue  # ready や、タイムアウト後に遅れて届いた古い応答は捨てる
                if 'error' in message:
                    raise RuntimeError(message['error'])
                return message.get('text', '')
        finally:
            self.lock.release()

    def close(self):
        with self._spawn_lock:
            self._stop_locked()

class Handler(socketserver.StreamRequestHandler):
    def handle(self):
        self.connection.settimeout(10)
        language, started, requested = 'ja', False, False
        audio = bytearray()
        deadline = time.monotonic()+45
        try:
            while time.monotonic() < deadline:
                kind, data, payload = read_event(self.rfile)
                if kind == 'describe':
                    send_event(self.wfile, 'info', {'asr': [{'name': 'local-whisper-ja', 'description': 'Japanese speech recognition',
                        'installed': True, 'version': '1', 'models': [{'name': 'small', 'installed': True, 'languages': ['ja']}]}]})
                    return
                if kind == 'transcribe' and not requested:
                    language = data.get('language') or 'ja'
                    if language not in ('ja', 'ja-JP'):
                        raise ValueError('this service is configured for Japanese')
                    language, requested = 'ja', True
                    prepare = getattr(self.server.recognizer, 'prepare', None)
                    if prepare:
                        prepare()
                elif kind == 'audio-start' and requested and not started:
                    if any(data.get(k) != v for k, v in FORMAT.items()):
                        raise ValueError('expected 16 kHz 16-bit mono audio')
                    started = True
                elif kind == 'audio-chunk' and started:
                    if any(data.get(k) != v for k, v in FORMAT.items()) or len(payload) % 2:
                        raise ValueError('invalid audio chunk')
                    if len(audio)+len(payload) > MAX_AUDIO:
                        raise ValueError('audio too long')
                    audio.extend(payload)
                elif kind == 'audio-stop' and started:
                    text = self.server.recognizer.transcribe(bytes(audio), language)
                    send_event(self.wfile, 'transcript', {'text': text, 'language': language})
                    return
                else:
                    raise ValueError('unexpected event')
            raise ValueError('request timed out')
        except (EOFError, ConnectionError, TimeoutError):
            pass
        except Exception as exc:
            LOG.warning('request failed: %s', type(exc).__name__)
            try:
                send_event(self.wfile, 'error', {'text': 'Speech recognition failed', 'code': 'recognition-failed'})
            except OSError:
                pass

class Server(socketserver.ThreadingTCPServer):
    # Windows の SO_REUSEADDR は「使用中のポートの重ね取り」になるので付けない（代わりに排他で bind する）
    allow_reuse_address = os.name != 'nt'
    daemon_threads = True
    request_queue_size = 2

    def __init__(self, address, recognizer, allowed):
        self.recognizer, self.allowed = recognizer, set(allowed)
        super().__init__(address, Handler)

    def server_bind(self):
        if os.name == 'nt' and hasattr(socket, 'SO_EXCLUSIVEADDRUSE'):
            self.socket.setsockopt(socket.SOL_SOCKET, socket.SO_EXCLUSIVEADDRUSE, 1)
        super().server_bind()

    def verify_request(self, request, client_address):
        return client_address[0] in self.allowed

def parse_args(argv=None):
    parser = argparse.ArgumentParser()
    parser.add_argument('--bind', default='127.0.0.1')
    parser.add_argument('--port', type=int, default=50024)
    parser.add_argument('--allow', action='append', default=['127.0.0.1'], help='allowed Echo IPv4 address')
    parser.add_argument('--model', default='large-v3-turbo', help='faster-whisper model name')
    parser.add_argument('--model-dir', type=Path, default=None, help='local model directory (overrides --model)')
    parser.add_argument('--device', choices=('auto', 'cuda', 'cpu'), default='auto')
    parser.add_argument('--compute-type', default=None, help='default: float16 on cuda, int8 on cpu')
    parser.add_argument('--idle-unload-minutes', type=int, default=30, help='認識プロセスを下ろすまでの無音分数（0 で下ろさない）')
    parser.add_argument('--log-file', default=None, help='ログファイル（指定時はローテーションして書く）')
    return parser.parse_args(argv)

def cuda_devices():
    try:
        import ctranslate2
        return ctranslate2.get_cuda_device_count()
    except Exception:
        return 0

def model_settings(ns):
    device = ns.device
    if device == 'auto':
        device = 'cuda' if cuda_devices() > 0 else 'cpu'
    model = str(ns.model_dir) if ns.model_dir else ns.model
    compute_type = ns.compute_type or ('float16' if device == 'cuda' else 'int8')
    return model, device, compute_type

def worker_command(ns):
    cmd = [sys.executable, os.path.join(os.path.dirname(os.path.abspath(__file__)), 'worker.py'),
           '--model', ns.model, '--device', ns.device, '--idle-seconds', str(ns.idle_unload_minutes * 60)]
    if ns.model_dir:
        cmd += ['--model-dir', str(ns.model_dir)]
    if ns.compute_type:
        cmd += ['--compute-type', ns.compute_type]
    return cmd

def configure_logging(log_file):
    fmt = '%(asctime)s %(levelname)s %(message)s'
    if log_file is None:
        logging.basicConfig(level=logging.INFO, format=fmt)
        return
    # pythonw.exe には stderr が無いので、ファイルだけに出す
    handler = logging.handlers.RotatingFileHandler(log_file, maxBytes=5_000_000, backupCount=3, encoding='utf-8')
    handler.setFormatter(logging.Formatter(fmt))
    root = logging.getLogger()
    root.addHandler(handler)
    root.setLevel(logging.INFO)

def main():
    args = parse_args()
    configure_logging(args.log_file)
    proxy = WorkerProxy(worker_command(args), response_timeout=10.0)
    try:
        with Server((args.bind, args.port), proxy, args.allow) as server:
            LOG.info('ready bind=%s port=%s idle_unload_minutes=%s worker=%s',
                     args.bind, args.port, args.idle_unload_minutes, worker_command(args)[1])
            server.serve_forever()
    except KeyboardInterrupt:
        pass
    except Exception:
        LOG.exception('startup failed')  # pythonw では stderr が無いので、ログファイルに残す
        raise
    finally:
        proxy.close()

if __name__ == '__main__':
    main()
