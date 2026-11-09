# 母艦の Windows ネイティブ化 実装計画

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Whisper 音声認識（Wyoming、TCP 50024）を WSL の systemd から Windows ネイティブの Python へ移し、待機中は GPU を使わない 2 プロセス構成にする。

**Architecture:** `recognizer.py` は「受付」になり、listen とプロトコル処理だけを行い、`faster_whisper`/`ctranslate2`/`numpy` を import しない。認識は新しい `worker.py` を子プロセスとして起こし、標準入出力の JSON 行 + 生 PCM で受け渡す。子プロセスは 30 分（既定）使われなければ自分で終了する。Windows では `install-asr.ps1` が venv・モデル取得・ログオン時タスク `hey-muse ASR` を用意する。

**Tech Stack:** Python 3.13（Windows）/ 3.12（WSL、既存テスト）、標準ライブラリのみ（`socketserver`, `subprocess`, `threading`, `queue`, `array`, `logging.handlers`）、faster-whisper 1.2.1 + nvidia-cublas-cu12 + nvidia-cudnn-cu12（認識プロセスのみ）、PowerShell 5.1（`Register-ScheduledTask`）。

**Spec:** `docs/superpowers/specs/2026-11-09-windows-mothership-design.md`

## Global Constraints

- 受付プロセス（`recognizer.py` のモジュール読み込みと `main()`）は `faster_whisper`、`ctranslate2`、`numpy` を import しない（待機中 VRAM 0 の根拠）。
- 端末側（`echod/`）は変更しない。Wyoming のイベント名・形式・ポート 50024 は従来どおり。
- 既存テスト `tools/echo-asr/pc/test_recognizer.py` の `FakeRecognizer`（`transcribe(pcm, language)` だけを持つ）が引き続き `Server` に差し込める。
- 認識プロセスのアイドル終了の既定は **30 分**（`--idle-unload-minutes 30`、`0` で終了しない）。
- 受付の 1 要求の応答上限は **10 秒**。超えたら `error`（`recognition-failed`）を返すが、認識プロセスは殺さない。
- Windows のタスクは `--device cuda` 固定、`--bind 0.0.0.0`。Windows 側は WSL のファイルを参照しない（`%LOCALAPPDATA%\hey-muse\app\` にコピー）。
- 文書・コメント・コミットメッセージは日本語。コミット末尾に `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>` と `Claude-Session: https://claude.ai/code/session_013XoetZKpfRq13qk4th5rxf`。
- テストは WSL で `cd tools/echo-asr/pc && /usr/bin/python3 -m unittest -v`（`python3` は別の venv を指すので `/usr/bin/python3` を使う）。GPU も faster-whisper も不要で通ること。
- 端末 IP 192.168.0.124、母艦 LAN IP 192.168.0.77、端末シリアル・Wi-Fi 名・鍵は公開文書に書かない。

## Review Focus

1. **読み込みが 10 秒を超える冷えたディスク**: 受付は 10 秒で `error` を返し、認識プロセスはそのまま読み込みを続け、直後の要求は同じプロセスで速く返る（Task 2 `test_timeout_keeps_worker`）。
2. **認識プロセスが要求の途中で死ぬ**: その要求は `error`、次の要求で新しいプロセスが起きる（Task 2 `test_worker_death_then_respawn`）。
3. **無音・極短の音声**: 認識プロセスに渡さず空文字を返す（Task 2 `test_silence_short_circuits_worker`）。
4. **2 台目の要求が認識中に来る**: 従来どおり `recognizer busy`（Task 2 `test_busy_while_transcribing`）。
5. **`TimeoutError` を受付が握りつぶす**: 既存の `except (EOFError, ConnectionError, TimeoutError): pass` に吸われて端末に何も返らない事故を、専用例外 `WorkerTimeout` で避ける（Task 2 `test_timeout_reaches_client_as_error`）。

---

### Task 1: 認識プロセス `worker.py`

**Files:**
- Create: `tools/echo-asr/pc/worker.py`
- Create: `tools/echo-asr/pc/test_worker.py`
- （`recognizer.py` はこの Task では触らない。`Recognizer` クラスは `worker.py` に写し、`recognizer.py` 側の削除は Task 3 で `main()` を配線し直すときに行う。それまで `main()` は従来どおり動く）

**Interfaces:**
- Consumes: `recognizer.model_settings(ns) -> (model: str, device: str, compute_type: str)`、`recognizer.cuda_devices() -> int`（既存）。
- Produces（受付と偽ワーカーが従うプロトコル。以後「ワーカー・プロトコル」）:
  - 起動引数: `worker.py --model M [--model-dir DIR] --device {auto,cuda,cpu} [--compute-type T] --idle-seconds S [--check]`
  - stdin → ワーカー: 1 要求 = JSON 1 行 `{"id": <int>, "language": "ja", "pcm_length": <int>}` + 改行 + `pcm_length` バイトの PCM（16 kHz・16 bit・mono・LE）。
  - ワーカー → stdout: JSON 行のみ。モデル読み込み後に `{"event": "ready"}` を 1 回。要求ごとに `{"id": <int>, "text": "<str>"}` または `{"id": <int>, "error": "<str>"}`。読み込みに失敗したら `{"event": "fatal", "error": "<str>"}` を出して終了コード 1。
  - `--idle-seconds S` 秒（`0` なら無期限）要求が無ければ終了コード 0 で終わる。stdin が EOF でも終わる。
  - `--check`: モデルを読み込み `ready` を出して終了コード 0（インストーラの動作確認用）。
  - ログは stderr（受付が中継する）。
  - `add_cuda_dll_dirs() -> list[str]`: `sys.platform == 'win32'` のとき `nvidia.cublas` と `nvidia.cudnn` パッケージの `bin` ディレクトリを `os.add_dll_directory` に足し、足したパスを返す。パッケージが無い・win32 でないなら `[]`。`faster_whisper` の import より前に呼ぶ。

- [ ] **Step 1: 失敗するテストを書く（`test_worker.py`）**

```python
import io, json, sys, time, unittest
import worker as w

class DllDirsTest(unittest.TestCase):
    def test_noop_outside_windows_or_without_packages(self):
        self.assertEqual(w.add_cuda_dll_dirs(), [] if sys.platform != 'win32' else w.add_cuda_dll_dirs())

class ProtocolTest(unittest.TestCase):
    def test_parse_request_header(self):
        self.assertEqual(w.parse_request(b'{"id": 7, "language": "ja", "pcm_length": 4}\n'), (7, 'ja', 4))
        with self.assertRaises(ValueError):
            w.parse_request(b'{"id": 7, "language": "ja", "pcm_length": -1}\n')

    def test_serve_answers_each_request_and_exits_on_eof(self):
        class Model:
            def transcribe(self, pcm, language): return 'テキスト%d' % len(pcm)
        inp = (b'{"id": 1, "language": "ja", "pcm_length": 2}\n' + b'\x00\x00' +
               b'{"id": 2, "language": "ja", "pcm_length": 0}\n')
        out = w.serve(Model(), io.BytesIO(inp), io.BytesIO(), idle_seconds=0)
        lines = [json.loads(l) for l in out.getvalue().splitlines()]
        self.assertEqual(lines, [{'event': 'ready'}, {'id': 1, 'text': 'テキスト2'}, {'id': 2, 'text': 'テキスト0'}])

    def test_serve_exits_after_idle(self):
        class Model:
            def transcribe(self, pcm, language): return ''
        start = time.monotonic()
        w.serve(Model(), SlowEOF(), io.BytesIO(), idle_seconds=1)   # SlowEOF: read が 5 秒ブロックしてから EOF
        self.assertLess(time.monotonic() - start, 3)

class CliTest(unittest.TestCase):
    def test_parse_args_defaults(self):
        ns = w.parse_args(['--model', 'large-v3-turbo', '--device', 'cuda', '--idle-seconds', '1800'])
        self.assertEqual((ns.model, ns.device, ns.idle_seconds, ns.check), ('large-v3-turbo', 'cuda', 1800, False))
```

`serve(model, stdin: BinaryIO, stdout: BinaryIO, idle_seconds: int) -> BinaryIO` は stdout を返す（テストが読む）。`SlowEOF` はテスト内の小さなクラス（`readline` が `time.sleep(5)` して `b''` を返す）。

- [ ] **Step 2: 失敗を確認する**

Run: `cd tools/echo-asr/pc && /usr/bin/python3 -m unittest test_worker -v`
Expected: FAIL（`ModuleNotFoundError: worker`）

- [ ] **Step 3: `worker.py` を実装する**

- `add_cuda_dll_dirs() -> list[str]`
- `parse_request(line: bytes) -> tuple[int, str, int]`: `id`/`pcm_length` が int でない、`pcm_length` が `0..recognizer.MAX_AUDIO` 外、`language` が `ja`/`ja-JP` 以外なら `ValueError`。
- `class Recognizer`: `recognizer.py` のものを写す（`__init__(model, device, compute_type)` は `add_cuda_dll_dirs()` を呼んでから `from faster_whisper import WhisperModel`。`transcribe(pcm, language) -> str` は numpy で float32 に直して従来の引数で `self.model.transcribe`。無音判定は受付側に移すので入れない。`recognizer.py` 側の元のクラスはまだ残す）。
- `serve(model, stdin, stdout, idle_seconds)`: 読み取りスレッドが `stdin` から要求を読んで `queue.Queue` に入れ（EOF で `None`）、本体は `ready` を書いた後 `queue.get(timeout=idle_seconds or None)` で待つ。`queue.Empty` か `None` で戻る。要求ごとに `{"id", "text"}`、`transcribe` が例外なら `{"id", "error": 型名}`。各行の後で `flush`。
- `parse_args(argv=None)`: 上記の引数。`--idle-seconds` は int 必須。
- `main()`: `logging.basicConfig(stream=sys.stderr, level=INFO, format='%(asctime)s %(levelname)s %(message)s')`。`recognizer.model_settings` 相当で `auto` を解決（`cuda_devices()` の import はここで初めて起きる）。`Recognizer(...)` の構築に失敗したら `{"event":"fatal","error":...}` を stdout に書いて `sys.exit(1)`。`--check` なら `ready` を書いて終了。そうでなければ `serve(model, sys.stdin.buffer, sys.stdout.buffer, idle_seconds)`。

- [ ] **Step 4: テストが通ることを確認する**

Run: `cd tools/echo-asr/pc && /usr/bin/python3 -m unittest test_worker -v`
Expected: 全件 OK

- [ ] **Step 5: 既存テストも含めて通ることを確認する**

Run: `cd tools/echo-asr/pc && /usr/bin/python3 -m unittest -v`
Expected: `test_recognizer` の 8 件と `test_worker` が OK

- [ ] **Step 6: コミット**

```bash
git add tools/echo-asr/pc/worker.py tools/echo-asr/pc/test_worker.py
git commit -m "音声認識のモデル処理を行う認識プロセス worker.py を追加する"
```

---

### Task 2: 受付側の `WorkerProxy`・無音判定・先読み起動

**Files:**
- Modify: `tools/echo-asr/pc/recognizer.py`（`Handler.handle`、新クラス・関数の追加）
- Create: `tools/echo-asr/pc/fake_worker.py`（テスト専用。ワーカー・プロトコルを話す偽物）
- Modify: `tools/echo-asr/pc/test_recognizer.py`（`ProxyTests` を追加）

**Interfaces:**
- Consumes: ワーカー・プロトコル（Task 1）。
- Produces:
  - `is_silent(pcm: bytes) -> bool`: `array('h')` で読み、サンプル数 1600 未満、または絶対値の最大が 33 未満（従来の 0.001 × 32768）なら True。numpy を使わない。
  - `class WorkerTimeout(Exception)`、`class WorkerExited(Exception)`（`TimeoutError`/`OSError` の派生にしない）。
  - `class WorkerProxy`: `__init__(self, command: list[str], response_timeout: float = 10.0)`、`prepare() -> None`（生きたワーカーが無ければ `subprocess.Popen(command, stdin=PIPE, stdout=PIPE, stderr=PIPE)` を起こして戻る。ブロックしない）、`transcribe(pcm: bytes, language: str) -> str`、`close() -> None`（ワーカーを terminate して待つ）、属性 `response_timeout`（テストが差し替える）、プロパティ `pid -> int | None`（テスト用）。
  - `transcribe` の規則: `is_silent(pcm)` なら即 `''`（ワーカーに触れない）。`self.lock.acquire(timeout=3)` に失敗したら `ValueError('recognizer busy')`。`prepare()` → 要求を書く → `response_timeout` 秒以内に同じ `id` の応答が来なければ `WorkerTimeout`（ワーカーは殺さない。遅れて届いた古い `id` の応答は捨てる）。ワーカーの stdout が EOF になったら `WorkerExited`。応答が `error` なら `RuntimeError(error)`。
  - stdout は読み取りスレッドが JSON 行を `queue.Queue` に入れる（`ready` も入れる。`transcribe` は自分の `id` 以外を読み飛ばす）。stderr は別スレッドが 1 行ずつ `LOG.info('worker: %s', line)` で中継する。
  - `Handler.handle`: `transcribe` イベントで `getattr(self.server.recognizer, 'prepare', None)` があれば呼ぶ。`WorkerTimeout`/`WorkerExited`/`RuntimeError`/`ValueError` は既存の `except Exception` に落ちて `error`（`recognition-failed`）を返す。
  - `fake_worker.py` 引数: `--delay-ready S`（ready 前に待つ）、`--idle-seconds S`、`--die-on N`（N 件目の要求を受けたら答えずに終了コード 3）、`--slow S`（各応答の前に待つ）、`--stderr-line TEXT`（起動時に stderr に 1 行出す）。応答の `text` は `'ja:%d' % pcm_length`。

- [ ] **Step 1: 失敗するテストを書く（`test_recognizer.py` に追記）**

```python
# ファイル先頭に `import os, sys, time, pathlib, tempfile, logging, logging.handlers` を足す
FAKE = [sys.executable, str(pathlib.Path(__file__).with_name('fake_worker.py'))]
LOUD = (b'\x00\x10' * 1600)   # 4096 の振幅 × 1600 サンプル
SILENT = b'\x01\x00' * 1600   # 振幅 1

class SilenceTest(unittest.TestCase):
    def test_is_silent(self):
        self.assertTrue(app.is_silent(b''))
        self.assertTrue(app.is_silent(SILENT))
        self.assertTrue(app.is_silent(b'\x00\x10' * 1599))
        self.assertFalse(app.is_silent(LOUD))

class ProxyTests(unittest.TestCase):
    def tearDown(self): self.proxy.close()

    def test_round_trip_and_prepare_reuses_worker(self):
        self.proxy = app.WorkerProxy(FAKE + ['--idle-seconds', '0'])
        self.proxy.prepare(); pid = self.proxy.pid
        self.assertEqual(self.proxy.transcribe(LOUD, 'ja'), 'ja:3200')
        self.assertEqual(self.proxy.pid, pid)

    def test_silence_short_circuits_worker(self):
        self.proxy = app.WorkerProxy(['/nonexistent/python'])
        self.assertEqual(self.proxy.transcribe(SILENT, 'ja'), '')

    def test_timeout_keeps_worker(self):
        self.proxy = app.WorkerProxy(FAKE + ['--delay-ready', '2', '--idle-seconds', '0'], response_timeout=0.5)
        with self.assertRaises(app.WorkerTimeout):
            self.proxy.transcribe(LOUD, 'ja')
        pid = self.proxy.pid
        self.proxy.response_timeout = 10
        self.assertEqual(self.proxy.transcribe(LOUD, 'ja'), 'ja:3200')
        self.assertEqual(self.proxy.pid, pid)

    def test_worker_death_then_respawn(self):
        self.proxy = app.WorkerProxy(FAKE + ['--die-on', '2', '--idle-seconds', '0'])
        self.assertEqual(self.proxy.transcribe(LOUD, 'ja'), 'ja:3200'); pid = self.proxy.pid
        with self.assertRaises(app.WorkerExited):
            self.proxy.transcribe(LOUD, 'ja')
        self.assertEqual(self.proxy.transcribe(LOUD, 'ja'), 'ja:3200')
        self.assertNotEqual(self.proxy.pid, pid)

    def test_idle_exit_then_respawn(self):
        self.proxy = app.WorkerProxy(FAKE + ['--idle-seconds', '1'])
        self.proxy.prepare(); pid = self.proxy.pid
        time.sleep(2.5)
        self.assertEqual(self.proxy.transcribe(LOUD, 'ja'), 'ja:3200')
        self.assertNotEqual(self.proxy.pid, pid)

    def test_busy_while_transcribing(self):
        self.proxy = app.WorkerProxy(FAKE + ['--slow', '5', '--idle-seconds', '0'], response_timeout=10)
        t = threading.Thread(target=lambda: self.proxy.transcribe(LOUD, 'ja')); t.start(); time.sleep(0.5)
        with self.assertRaisesRegex(ValueError, 'busy'):
            self.proxy.transcribe(LOUD, 'ja')
        t.join()

    def test_worker_stderr_is_relayed(self):
        self.proxy = app.WorkerProxy(FAKE + ['--stderr-line', 'こんにちは', '--idle-seconds', '0'])
        with self.assertLogs(app.LOG, level='INFO') as cm:
            self.proxy.transcribe(LOUD, 'ja')
        self.assertTrue(any('worker: こんにちは' in m for m in cm.output))
```

`ProtocolTests` にも 2 件足す（`setUp` の `FakeRecognizer` は共通のまま）:

```python
    def test_transcribe_event_calls_prepare(self):
        rec = self.server.recognizer; rec.prepared = 0
        rec.prepare = lambda: setattr(rec, 'prepared', rec.prepared + 1)
        app.send_event(self.file, 'transcribe', {'language': 'ja'})
        app.send_event(self.file, 'audio-start', app.FORMAT)
        app.send_event(self.file, 'audio-stop', {})
        app.read_event(self.file)
        self.assertEqual(rec.prepared, 1)

    def test_timeout_reaches_client_as_error(self):
        class Slow:
            def transcribe(self, pcm, language): raise app.WorkerTimeout('slow')
        self.server.recognizer = Slow()
        app.send_event(self.file, 'transcribe', {'language': 'ja'})
        app.send_event(self.file, 'audio-start', app.FORMAT)
        app.send_event(self.file, 'audio-stop', {})
        kind, data, _ = app.read_event(self.file)
        self.assertEqual((kind, data['code']), ('error', 'recognition-failed'))
```

既存 `FakeRecognizer.transcribe` の `assert pcm == b'\x01\x00'*1600` は、無音判定を受付の `WorkerProxy` 内に置いたため `Server` 経由では影響しない（`FakeRecognizer` に `is_silent` は掛からない）。そのまま残す。

- [ ] **Step 2: 失敗を確認する**

Run: `cd tools/echo-asr/pc && /usr/bin/python3 -m unittest test_recognizer -v`
Expected: `SilenceTest`・`ProxyTests`・新しい 2 件が FAIL/ERROR、既存 8 件は OK

- [ ] **Step 3: `fake_worker.py` を書く**

ワーカー・プロトコル（Task 1 の Interfaces）どおりに stdin/stdout を扱う。`--delay-ready` は ready の前、`--slow` は各応答の前に `time.sleep`。`--die-on N` は N 件目の要求ヘッダーを読んだ直後に、何も書かずに `sys.exit(3)`。`--idle-seconds` は Task 1 と同じ意味（`queue.get(timeout)`）。`--stderr-line` は起動直後に stderr へ書いて flush。

- [ ] **Step 4: `recognizer.py` に `is_silent`・例外・`WorkerProxy` を実装し、`Handler` に `prepare` 呼び出しを足す**

Interfaces のとおり。`WorkerProxy.transcribe` は `id` をカウンターで振り、`queue.get(timeout=残り時間)` で自分の `id` を待つ。`prepare()` は `self.proc is None or self.proc.poll() is not None` のときだけ起動する。`close()` は `terminate()` → `wait(3)`。

- [ ] **Step 5: テストが通ることを確認する**

Run: `cd tools/echo-asr/pc && /usr/bin/python3 -m unittest -v`
Expected: 全件 OK（所要 15 秒程度）

- [ ] **Step 6: 受付が重いモジュールを import しないことを確認する**

Run: `cd tools/echo-asr/pc && /usr/bin/python3 -c "import sys, recognizer; bad=[m for m in ('faster_whisper','ctranslate2','numpy') if m in sys.modules]; print('bad imports:', bad); sys.exit(1 if bad else 0)"`
Expected: `bad imports: []`、終了コード 0

- [ ] **Step 7: コミット**

```bash
git add tools/echo-asr/pc/recognizer.py tools/echo-asr/pc/fake_worker.py tools/echo-asr/pc/test_recognizer.py
git commit -m "受付が認識プロセスを必要なときだけ起こし、無音は渡さず、10 秒で打ち切る"
```

---

### Task 3: CLI とログ（`--idle-unload-minutes`、`--log-file`、`main()` の配線）

**Files:**
- Modify: `tools/echo-asr/pc/recognizer.py`（`parse_args`、`main`、新関数）
- Modify: `tools/echo-asr/pc/test_recognizer.py`（`CliTests` を追加）

**Interfaces:**
- Consumes: `WorkerProxy(command, response_timeout)`（Task 2）、`worker.py` の引数（Task 1）。
- Produces:
  - `parse_args`: 追加 `--idle-unload-minutes`（int、既定 30）、`--log-file`（str、既定 None）。既存引数はそのまま。
  - `worker_command(ns) -> list[str]`: `[sys.executable, <recognizer.py と同じディレクトリ>/worker.py, '--model', ns.model, '--device', ns.device, '--idle-seconds', str(ns.idle_unload_minutes * 60)]` に、`ns.model_dir` があれば `['--model-dir', str(ns.model_dir)]`、`ns.compute_type` があれば `['--compute-type', ns.compute_type]` を足す。
  - `configure_logging(log_file: str | None) -> None`: `None` なら従来の `basicConfig(level=INFO, format=...)`。パスがあれば `logging.handlers.RotatingFileHandler(log_file, maxBytes=5_000_000, backupCount=3, encoding='utf-8')` を同じ format でルートに付ける。
  - `main()`: `configure_logging(args.log_file)` → `WorkerProxy(worker_command(args), response_timeout=10.0)` → `Server(...)` → `LOG.info('ready bind=%s port=%s idle_unload_minutes=%s worker=%s', ...)` → `serve_forever()`。`model_settings` は受付では呼ばない（`auto` の解決は認識プロセス）。`recognizer.py` の `Recognizer` クラスはここで削除する（Task 1 で `worker.py` に写してある）。受付は `print` を使わない（Windows では `pythonw.exe` で動き、stdout が無い）。

- [ ] **Step 1: 失敗するテストを書く**

```python
class CliTests(unittest.TestCase):
    def test_new_flags_and_defaults(self):
        ns = app.parse_args([])
        self.assertEqual((ns.idle_unload_minutes, ns.log_file), (30, None))
        ns = app.parse_args(['--idle-unload-minutes', '0', '--log-file', 'x.log'])
        self.assertEqual((ns.idle_unload_minutes, ns.log_file), (0, 'x.log'))

    def test_worker_command(self):
        ns = app.parse_args(['--device', 'cuda', '--idle-unload-minutes', '30', '--compute-type', 'float16'])
        cmd = app.worker_command(ns)
        self.assertEqual(cmd[0], sys.executable)
        self.assertTrue(cmd[1].endswith('worker.py'))
        self.assertEqual(cmd[2:], ['--model', 'large-v3-turbo', '--device', 'cuda', '--idle-seconds', '1800', '--compute-type', 'float16'])

    def test_rotating_log_file(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, 'asr.log')
            app.configure_logging(path)
            h = [h for h in logging.getLogger().handlers if isinstance(h, logging.handlers.RotatingFileHandler)][-1]
            self.assertEqual((h.baseFilename, h.maxBytes, h.backupCount), (path, 5_000_000, 3))
            logging.getLogger().removeHandler(h); h.close()
```

- [ ] **Step 2: 失敗を確認する**

Run: `cd tools/echo-asr/pc && /usr/bin/python3 -m unittest test_recognizer.CliTests -v`
Expected: 3 件 FAIL/ERROR

- [ ] **Step 3: 実装する**

Interfaces のとおり。`main()` から `Recognizer(...)` と `model_settings(args)` の呼び出しを外し、`Recognizer` クラスを `recognizer.py` から削除する。Task 2 の Step 6 の import 確認を再実行して `bad imports: []` のままであることも見る。

- [ ] **Step 4: 全テストが通ることを確認する**

Run: `cd tools/echo-asr/pc && /usr/bin/python3 -m unittest -v`
Expected: 全件 OK

- [ ] **Step 5: WSL 側で本物の worker を使った起動確認（GPU あり）**

Run: `cd tools/echo-asr/pc && LD_LIBRARY_PATH=$(~/.venvs/muse-asr/bin/python -c 'import nvidia.cublas.lib, nvidia.cudnn.lib; print(nvidia.cublas.lib.__path__[0]+":"+nvidia.cudnn.lib.__path__[0])') ~/.venvs/muse-asr/bin/python worker.py --model large-v3-turbo --device cuda --idle-seconds 0 --check`
Expected: stdout に `{"event": "ready"}`、終了コード 0（systemd の `muse-asr` が 50024 を使っているため受付の起動確認はしない）

- [ ] **Step 6: コミット**

```bash
git add tools/echo-asr/pc/recognizer.py tools/echo-asr/pc/test_recognizer.py
git commit -m "認識プロセスを下ろすまでの時間とログファイルを引数にし、受付からモデル処理を外す"
```

---

### Task 4: Windows インストーラと管理者前処理

**Files:**
- Create: `tools/mothership/windows/install-asr.ps1`
- Modify: `tools/mothership/windows/setup-mothership.ps1`（`-WindowsNative` スイッチ）
- Modify: `tools/mothership/README.md`（「WSL・systemd・自動起動」の表に 2 行追加、Windows ネイティブの実行順序）

**Interfaces:**
- Consumes: `worker.py --check`（Task 1）、`recognizer.py` の引数（Task 3）。
- Produces:
  - `install-asr.ps1 -Allow <IP>[,<IP>...] [-Port 50024] [-IdleMinutes 30] [-Model large-v3-turbo]`。非昇格で実行。`$ErrorActionPreference='Stop'`。
    1. `$app = "$env:LOCALAPPDATA\hey-muse"`、`$app\app`、`$app\logs` を作る。`$app\venv` が無ければ `py -3 -m venv "$app\venv"`。`"$app\venv\Scripts\python.exe" -m pip install --upgrade pip` → `-m pip install faster-whisper==1.2.1 nvidia-cublas-cu12 nvidia-cudnn-cu12`。
    2. `$PSScriptRoot\..\..\echo-asr\pc\recognizer.py` と `worker.py` を `$app\app\` へコピー（上書き）。
    3. `"$app\venv\Scripts\python.exe" "$app\app\worker.py" --model $Model --device cuda --idle-seconds 0 --check`。終了コードが 0 でなければ、出力を見せて `throw`（CUDA か DLL の問題）。
    4. タスク `hey-muse ASR` を `Register-ScheduledTask -Force` で登録: Action = `"$app\venv\Scripts\pythonw.exe"`、引数 = `"$app\app\recognizer.py" --bind 0.0.0.0 --port $Port --device cuda --idle-unload-minutes $IdleMinutes --log-file "$app\logs\asr.log"` に `-Allow` ごとの `--allow <IP>`、WorkingDirectory = `$app\app`。Trigger = `New-ScheduledTaskTrigger -AtLogOn -User <現在のユーザー>`。Principal = 現在のユーザー、`-LogonType Interactive`、`-RunLevel Limited`。Settings = `-ExecutionTimeLimit ([TimeSpan]::Zero) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -MultipleInstances IgnoreNew`。
    5. `Get-ScheduledTask -TaskName 'hey-muse WSL'` があれば `Unregister-ScheduledTask -Confirm:$false` を試み、失敗したら `$adminTodo` に `Unregister-ScheduledTask -TaskName 'hey-muse WSL' -Confirm:$false` を足す。
    6. `Get-NetFirewallRule -Name muse-asr` が無ければ `$adminTodo` に `powershell.exe -ExecutionPolicy Bypass -File <このディレクトリ>\setup-mothership.ps1 -WindowsNative -Port $Port` を足す。
    7. 既に動いている `hey-muse ASR` があれば `Stop-ScheduledTask`、その後 `Start-ScheduledTask`。最大 20 秒待って `Test-NetConnection 127.0.0.1 -Port $Port -InformationLevel Quiet` が `$true` になることを確認。ならなければ `$app\logs\asr.log` の末尾 20 行を見せて `throw`。
    8. 最後に、タスク名・ログの場所・`--allow` の値・`$adminTodo`（あれば「管理者 PowerShell で実行してください」と見出しを付けて）を表示する。
  - `setup-mothership.ps1 -WindowsNative`: ディストリビューションの確認、`.wslconfig`、Hyper-V 規則、タスク `hey-muse WSL` を行わない。受信規則 `muse-asr`（TCP `$Port`）と `powercfg /change standby-timeout-ac 0` だけを行い、「完了（Windows ネイティブ母艦）」と出す。スイッチ無しは従来どおり。

- [ ] **Step 1: 2 つのスクリプトを書く**

上記のとおり。コメントは日本語。`install-asr.ps1` の先頭コメントに使い方（`powershell.exe -ExecutionPolicy Bypass -File tools\mothership\windows\install-asr.ps1 -Allow 192.0.2.10`）と「非昇格で実行する」旨を書く。

- [ ] **Step 2: 構文を検査する**

Run（WSL から）: `cd tools/mothership/windows && for f in install-asr.ps1 setup-mothership.ps1; do powershell.exe -NoProfile -Command "\$t=\$null;\$e=\$null;[void][System.Management.Automation.Language.Parser]::ParseFile((Resolve-Path '$f'), [ref]\$t, [ref]\$e); if(\$e){\$e|%{\$_.Message}; exit 1}else{'OK $f'}"; done`
Expected: `OK install-asr.ps1`、`OK setup-mothership.ps1`

- [ ] **Step 3: README を更新する**

`tools/mothership/README.md` の表に `windows/install-asr.ps1`（非昇格、venv・モデル・タスク `hey-muse ASR`、`$adminTodo` の案内）と `setup-mothership.ps1 -WindowsNative`（FW と電源のみ）の行を足し、「実行の順序」の前に Windows ネイティブの順序（管理者で `-WindowsNative` → 通常権限で `install-asr.ps1 -Allow 端末IP`）を書く。既存の WSL の順序は「Linux/WSL 母艦の場合」として残す。

- [ ] **Step 4: コミット**

```bash
git add tools/mothership/windows/install-asr.ps1 tools/mothership/windows/setup-mothership.ps1 tools/mothership/README.md
git commit -m "Windows ネイティブ母艦のインストーラと、FW・電源だけを行う -WindowsNative を追加する"
```

---

### Task 5: 手順書・README・受け入れ記録

**Files:**
- Modify: `docs/ja/setup.md`（第 3 部を書き直す）
- Modify: `README.md`（5 行目の「母艦（WSL の Whisper）」）
- Modify: `docs/ja/acceptance.md`（表、項目 4、項目 9、項目 11 を追加）

**Interfaces:**
- Consumes: Task 4 のスクリプト名・引数、Task 3 の引数名、`%LOCALAPPDATA%\hey-muse\logs\asr.log`。

- [ ] **Step 1: `setup.md` 第 3 部を Windows ネイティブ版に書き直す**

見出し「第 3 部 母艦（Windows で音声認識を常駐させる）」。構成:
0. 前提（Windows 11、Python 3.13 `py -3`、NVIDIA ドライバー、PC は常時起動、ログオン後に動く）
1. 管理者の前処理（管理者 PowerShell で `setup-mothership.ps1 -WindowsNative`。期待: 規則 `muse-asr`、「完了（Windows ネイティブ母艦）」）
2. インストール（通常権限で `install-asr.ps1 -Allow 端末のIP`。初回はモデル約 1.6 GB の取得。期待: 最後に `--allow` とログの場所が出て、管理者コマンドの案内があればそれを実行）
3. 確認（`Get-ScheduledTask -TaskName 'hey-muse ASR' | Select State` が `Running`、`Test-NetConnection 母艦のLAN_IP -Port 50024` が True、`Get-Content $env:LOCALAPPDATA\hey-muse\logs\asr.log -Tail 5` に `ready`）
4. 再起動して確認（ログオン 2 分後に 3 と同じ。**`wsl --shutdown` しても変わらないこと**）
5. メモリの振る舞い（話しかけた直後は `nvidia-smi` の使用量が約 2 GB 増え、30 分使わないと認識プロセスが消えて戻る。30 分以上あけた後の最初の一言は 3〜6 秒遅い。変えたいときは `install-asr.ps1 -IdleMinutes N` で再実行）
6. 設定を変える・更新する（`install-asr.ps1` を引数を変えて再実行。recognizer.py/worker.py の更新も同じ）
7. うまくいかないとき（`asr.log` の `fatal`/`worker:` 行、`--check` の手動実行、FW、`hey-muse WSL` が残っていないか）
8. 付録 A: Linux/WSL を母艦にする場合（旧第 3 部の 1〜4 を「mirrored、install.sh、hey-muse WSL」として短く残す。`setup-mothership.ps1` はスイッチ無し）
9. 付録 B: WSL 母艦からの移行（`sudo systemctl disable --now muse-asr`、`.wslconfig` の `networkingMode=mirrored` を消して `wsl --shutdown`（任意）、`hey-muse WSL` タスクの削除）

第 7 部で母艦のアドレスに触れている箇所があれば「母艦の LAN アドレス（Windows の IP）」と揃える。

- [ ] **Step 2: `README.md` を直す**

「母艦（WSL の Whisper）」→「母艦（Windows の Whisper。Linux でも可）」。

- [ ] **Step 3: `acceptance.md` を直す**

- 表に `| 11 | 母艦の省メモリ（待機中は GPU を使わない） | 未実施（実機待ち） |` を足す。
- 項目 4 に「久しぶりの一言（認識プロセスが居ない状態）」と「連続の一言」の 2 行を表に足し、合否基準「久しぶりの一言は 10 回とも 12 秒以内に母艦で認識（ログに `muse: using local speech recognition`）」を書く。
- 項目 9 を Windows 版に差し替え（再起動 → ログオン 2 分 → `Get-ScheduledTask 'hey-muse ASR'` Running、`Test-NetConnection 母艦のLAN_IP -Port 50024` True、端末から `nc -zv 母艦のLAN_IP 50024`、`wsl --shutdown` 後も同じ）。2026-11-09 の WSL 版の記録は「旧構成での記録」として 1 段落残す。
- 項目 11 の手順（`nvidia-smi --query-gpu=memory.used --format=csv` を、話しかけた直後と 30 分後に比べる。`Get-Process python,pythonw | Select Name,WS` で受付のみ残ること）。

- [ ] **Step 4: 公開文書に個人情報が無いことを確認する**

Run: `git diff | grep -nE "G0913|192\.168|TP-Link|takum" || echo clean`
Expected: `clean`（母艦・端末の IP は「母艦のLAN_IP」「端末のIP」と書く）

- [ ] **Step 5: コミット**

```bash
git add docs/ja/setup.md README.md docs/ja/acceptance.md
git commit -m "手順書の第 3 部を Windows ネイティブ母艦に書き直し、受け入れ 4・9・11 を更新する"
```

---

### Task 6: 施主による導入と受け入れ（Claude は手順の提示と確認）

**Files:**
- Modify: `docs/ja/acceptance.md`（項目 9・11 の結果）
- Modify: `.superpowers/sdd/2026-11-09-ja-muse-show5/owner-todo.md`（C を Windows 版に）

**Interfaces:**
- Consumes: Task 4・5 の成果物。

- [ ] **Step 1: 施主が管理者 PowerShell で前処理を実行する**

Run（施主）: `powershell.exe -ExecutionPolicy Bypass -File \\wsl.localhost\Ubuntu\home\unotovive\projects\musekusa\hey-muse\tools\mothership\windows\setup-mothership.ps1 -WindowsNative`
Expected: 「完了（Windows ネイティブ母艦）」

- [ ] **Step 2: 施主が通常権限でインストーラを実行する**

Run（施主）: `powershell.exe -ExecutionPolicy Bypass -File \\wsl.localhost\Ubuntu\home\unotovive\projects\musekusa\hey-muse\tools\mothership\windows\install-asr.ps1 -Allow 192.168.0.124`
Expected: `--check` が通り、タスク `hey-muse ASR` が起動し、`127.0.0.1:50024` が True。管理者コマンドの案内（`hey-muse WSL` の削除）が出たら施主が管理者 PowerShell で実行。

- [ ] **Step 3: WSL 側の `muse-asr` を止める（施主、別ターミナル）**

Run（施主）: `sudo systemctl disable --now muse-asr`
Expected: `systemctl is-active muse-asr` が `inactive`。その後 Claude が WSL から `timeout 5 bash -c 'echo > /dev/tcp/192.168.0.77/50024' && echo open` で Windows 側の待ち受けを確認。

- [ ] **Step 4: 端末から母艦へ届くことを確認する（Claude）**

Run: `ssh -i ~/.ssh/id_rsa root@192.168.0.124 'nc -zv 192.168.0.77 50024 2>&1 || wget -qO- --timeout=3 http://192.168.0.77:50024/ 2>&1 | head -1'`
Expected: 接続成功（`open`/`succeeded`、または HTTP として拒否されても TCP は届いている）

- [ ] **Step 5: 省メモリの確認（Claude、項目 11）**

Run: `powershell.exe -NoProfile -Command "& 'C:\Windows\System32\nvidia-smi.exe' --query-gpu=memory.used --format=csv,noheader; Get-Process python,pythonw -ErrorAction SilentlyContinue | Select-Object Name,Id,@{n='MB';e={[int]($_.WS/1MB)}} | Format-Table -AutoSize | Out-String"`
Expected: 待機中は `pythonw` 1 本（数十 MB）だけ。話しかけた直後（または `install-asr.ps1` 直後の `--check` の後）は増え、30 分後に戻る。結果を `acceptance.md` 項目 11 に記録。

- [ ] **Step 6: Windows 再起動と項目 9（施主が再起動、Claude が確認）**

施主が Windows を再起動してログオン、2 分待って Claude Code を開く。Claude が Step 3 の TCP 確認と Step 4 を再実行し、さらに施主が `wsl --shutdown` した後に同じ確認。結果を `acceptance.md` 項目 9 に記録し、`owner-todo.md` C を「完了」に更新。

- [ ] **Step 7: コミット**

```bash
git add docs/ja/acceptance.md
git commit -m "受け入れ 9（再起動後の母艦）と 11（待機中の省メモリ）の結果を記録する"
```
