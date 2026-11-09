"""日本語の読み上げ（Wyoming の synthesize 用）。

VOICEVOX エンジンを必要なときだけ起動し、一定時間使われなければ止める（受付プロセスは常駐するので、
ふだんは何もメモリを使わない）。VOICEVOX が無いか失敗したときは Windows の SAPI 音声で読む。
どちらも標準ライブラリだけで扱い、重いライブラリは読み込まない。
"""
import io
import json
import logging
import os
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import wave
from pathlib import Path

LOG = logging.getLogger('muse-speaker')
MAX_TEXT = 2000  # 一度に読む文字数の上限（端末の答えは短いが、暴走した答えで止まらないように）
RECENT = 24 * 3600  # この秒数以内に読み上げを頼まれていれば、会話の始まりにエンジンを温める
FAILED_FOR = 300  # VOICEVOX の起動に失敗したら、この秒数は試さずに次（SAPI）へ回す
NO_WINDOW = getattr(subprocess, 'CREATE_NO_WINDOW', 0)


class SpeechFailed(Exception):
    pass


_job = None


def tie_to_this_process(proc):
    """Windows で、子プロセスをこのプロセスの寿命に結びつける（受付が殺されても VOICEVOX を残さない）。

    閉じたら中のプロセスを殺すジョブに入れる。ジョブのハンドルはこのプロセスが終わると閉じる。
    失敗しても読み上げ自体は続ける（ログだけ残す）。
    """
    global _job
    if sys.platform != 'win32':
        return
    try:
        import ctypes
        from ctypes import wintypes
        k32 = ctypes.WinDLL('kernel32', use_last_error=True)
        k32.CreateJobObjectW.restype = wintypes.HANDLE
        if _job is None:
            job = k32.CreateJobObjectW(None, None)
            if not job:
                raise ctypes.WinError(ctypes.get_last_error())

            class Basic(ctypes.Structure):
                _fields_ = [('PerProcessUserTimeLimit', ctypes.c_int64), ('PerJobUserTimeLimit', ctypes.c_int64),
                            ('LimitFlags', wintypes.DWORD), ('MinimumWorkingSetSize', ctypes.c_size_t),
                            ('MaximumWorkingSetSize', ctypes.c_size_t), ('ActiveProcessLimit', wintypes.DWORD),
                            ('Affinity', ctypes.c_size_t), ('PriorityClass', wintypes.DWORD),
                            ('SchedulingClass', wintypes.DWORD)]

            class Io(ctypes.Structure):
                _fields_ = [(n, ctypes.c_uint64) for n in ('ReadOperationCount', 'WriteOperationCount', 'OtherOperationCount',
                                                           'ReadTransferCount', 'WriteTransferCount', 'OtherTransferCount')]

            class Extended(ctypes.Structure):
                _fields_ = [('BasicLimitInformation', Basic), ('IoInfo', Io), ('ProcessMemoryLimit', ctypes.c_size_t),
                            ('JobMemoryLimit', ctypes.c_size_t), ('PeakProcessMemoryUsed', ctypes.c_size_t),
                            ('PeakJobMemoryUsed', ctypes.c_size_t)]
            info = Extended()
            info.BasicLimitInformation.LimitFlags = 0x2000  # JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
            if not k32.SetInformationJobObject(wintypes.HANDLE(job), 9, ctypes.byref(info), ctypes.sizeof(info)):
                raise ctypes.WinError(ctypes.get_last_error())
            _job = job
        if not k32.AssignProcessToJobObject(wintypes.HANDLE(_job), wintypes.HANDLE(int(proc._handle))):
            raise ctypes.WinError(ctypes.get_last_error())
    except Exception as exc:
        LOG.warning('could not tie VOICEVOX engine to this process: %s', exc)


def read_wav(data):
    """WAV を (16 bit モノラル PCM, サンプリング周波数) にする。"""
    with wave.open(io.BytesIO(data)) as w:
        if w.getsampwidth() != 2 or w.getnchannels() != 1:
            raise SpeechFailed('expected 16-bit mono speech')
        return w.readframes(w.getnframes()), w.getframerate()


def default_engine(base=None):
    """VOICEVOX エンジンの場所（無ければ None）。公式インストーラの置き場所と、winget の置き場所を探す。"""
    base = base or os.environ.get('LOCALAPPDATA')
    if not base:
        return None
    root = Path(base)
    candidates = [root / 'Programs' / 'VOICEVOX' / 'vv-engine' / 'run.exe']
    candidates += sorted((root / 'Microsoft' / 'WinGet' / 'Packages').glob('HiroshibaKazuyuki.VOICEVOX*/VOICEVOX/vv-engine/run.exe'))
    for path in candidates:
        if path.is_file():
            return path
    return None


class Voicevox:
    """VOICEVOX エンジン（HTTP）。すでに誰かが同じポートで動かしていれば、それを使う（止めない）。"""

    def __init__(self, engine, port=50021, speaker=30, speed=1.1, idle_seconds=1800, start_timeout=30.0,
                 urlopen=urllib.request.urlopen, popen=subprocess.Popen, clock=time.monotonic, sleep=time.sleep):
        self.engine, self.port, self.speaker, self.speed = engine, port, speaker, speed
        self.idle_seconds, self.start_timeout = idle_seconds, start_timeout
        self._urlopen, self._popen, self._clock, self._sleep = urlopen, popen, clock, sleep
        self._lock = threading.Lock()
        self._proc = None
        self._last_used = clock()
        self._failed_until = None
        self._closed = threading.Event()
        if idle_seconds > 0:
            threading.Thread(target=self._reaper, name='voicevox-reaper', daemon=True).start()

    @property
    def base(self):
        return f'http://127.0.0.1:{self.port}'

    def _alive(self):
        # Windows は閉じたローカルポートへの接続を 2 秒ほど再試行するので、短く切る
        try:
            with self._urlopen(self.base + '/version', timeout=0.5) as r:
                return r.status == 200
        except (OSError, urllib.error.URLError, ValueError):
            return False

    def _ensure(self):
        """エンジンが応答する状態にする。呼び出し側がロックを持つ。"""
        self._last_used = self._clock()
        if self._alive():
            self._failed_until = None
            return
        if self._failed_until is not None and self._clock() < self._failed_until:
            raise SpeechFailed('VOICEVOX engine failed recently')
        try:
            self._start()
            self._failed_until = None
        except SpeechFailed:
            self._failed_until = self._clock() + FAILED_FOR
            raise

    def _start(self):
        if self._proc is None or self._proc.poll() is not None:
            if self.engine is None:
                raise SpeechFailed('VOICEVOX engine not installed')
            LOG.info('starting VOICEVOX engine %s', self.engine)
            self._proc = self._popen([str(self.engine), '--host', '127.0.0.1', '--port', str(self.port)],
                                     stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                     creationflags=NO_WINDOW)
            tie_to_this_process(self._proc)
        deadline = self._clock() + self.start_timeout
        while self._clock() < deadline:
            if self._proc.poll() is not None:
                self._proc = None
                raise SpeechFailed('VOICEVOX engine exited while starting')
            if self._alive():
                LOG.info('VOICEVOX engine ready')
                return
            self._sleep(0.5)
        self._stop_locked()
        raise SpeechFailed('VOICEVOX engine did not start in time')

    def warm(self):
        """会話の始まりに呼ぶ。エンジンの起動を裏で始めておき、答えの読み上げで待たないようにする。"""
        def run():
            if not self._lock.acquire(timeout=1):
                return
            try:
                self._ensure()
            except Exception as exc:
                LOG.warning('VOICEVOX warm-up failed: %s', exc)
            finally:
                self._lock.release()
        threading.Thread(target=run, name='voicevox-warm', daemon=True).start()

    def _post(self, path, params, body=None, timeout=30):
        url = f'{self.base}{path}?{urllib.parse.urlencode(params)}'
        req = urllib.request.Request(url, data=body if body is not None else b'', method='POST',
                                     headers={'Content-Type': 'application/json'})
        with self._urlopen(req, timeout=timeout) as r:
            return r.read()

    def synthesize(self, text, voice=None):
        speaker = int(voice) if voice and str(voice).isdigit() else self.speaker
        with self._lock:
            self._ensure()
            query = json.loads(self._post('/audio_query', {'text': text, 'speaker': speaker}))
            query['outputStereo'] = False
            query['speedScale'] = self.speed
            wav = self._post('/synthesis', {'speaker': speaker}, json.dumps(query).encode('utf-8'), timeout=60)
            self._last_used = self._clock()
        return read_wav(wav)

    def _stop_locked(self):
        proc, self._proc = self._proc, None
        if proc is not None and proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                proc.kill()

    def _reaper(self):
        while not self._closed.wait(30):
            with self._lock:
                if self._proc is not None and self._clock() - self._last_used > self.idle_seconds:
                    LOG.info('VOICEVOX idle for %s seconds, stopping', self.idle_seconds)
                    self._stop_locked()

    def close(self):
        self._closed.set()
        with self._lock:
            self._stop_locked()


SAPI_SCRIPT = r'''
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Speech
$s = New-Object System.Speech.Synthesis.SpeechSynthesizer
$v = $s.GetInstalledVoices() | Where-Object { $_.Enabled -and $_.VoiceInfo.Culture.Name -eq 'ja-JP' } | Select-Object -First 1
if ($v) { $s.SelectVoice($v.VoiceInfo.Name) }
$s.Rate = 1
$fmt = New-Object System.Speech.AudioFormat.SpeechAudioFormatInfo(22050, [System.Speech.AudioFormat.AudioBitsPerSample]::Sixteen, [System.Speech.AudioFormat.AudioChannel]::Mono)
$s.SetOutputToWaveFile($env:HEY_MUSE_WAV, $fmt)
$s.Speak($env:HEY_MUSE_TEXT)
$s.Dispose()
'''


class Sapi:
    """Windows の音声合成（SAPI）。読むたびに PowerShell を起動するので、常駐するものは無い。"""

    def __init__(self, run=subprocess.run, tempdir=None):
        self._run = run
        self._tempdir = tempdir

    def warm(self):
        pass

    def synthesize(self, text, voice=None):
        import tempfile
        fd, path = tempfile.mkstemp(suffix='.wav', dir=self._tempdir)
        os.close(fd)
        try:
            env = dict(os.environ, HEY_MUSE_TEXT=text, HEY_MUSE_WAV=path)
            result = self._run(['powershell.exe', '-NoProfile', '-NonInteractive', '-Command', SAPI_SCRIPT],
                               env=env, stdin=subprocess.DEVNULL, capture_output=True, timeout=60,
                               creationflags=NO_WINDOW)
            if result.returncode != 0:
                raise SpeechFailed('SAPI failed with exit code %s' % result.returncode)
            with open(path, 'rb') as f:
                return read_wav(f.read())
        finally:
            try:
                os.unlink(path)
            except OSError:
                pass

    def close(self):
        pass


class Speaker:
    """前から順に試し、最初に読めたものを返す。"""

    def __init__(self, engines, clock=time.monotonic):
        self.engines = list(engines)
        self._clock = clock
        self._asked_at = None

    def recently_used(self):
        return self._asked_at is not None and self._clock() - self._asked_at < RECENT

    def warm(self):
        if self.engines:
            self.engines[0].warm()

    def synthesize(self, text, voice=None):
        self._asked_at = self._clock()
        text = ' '.join(str(text).split())[:MAX_TEXT]
        if not text:
            raise SpeechFailed('nothing to say')
        last = None
        for engine in self.engines:
            try:
                return engine.synthesize(text, voice)
            except Exception as exc:
                LOG.warning('%s failed: %s', type(engine).__name__, exc)
                last = exc
        raise SpeechFailed(str(last) if last else 'no speech engine')

    def close(self):
        for engine in self.engines:
            engine.close()


def build(engine='auto', speaker=30, idle_seconds=1800):
    """既定の組み合わせ: VOICEVOX（見つかれば）→ Windows なら SAPI。"""
    engines = []
    if engine != 'off':
        path = default_engine() if engine == 'auto' else Path(engine)
        if path is not None:
            engines.append(Voicevox(path, speaker=speaker, idle_seconds=idle_seconds))
    if sys.platform == 'win32':
        engines.append(Sapi())
    return Speaker(engines)
