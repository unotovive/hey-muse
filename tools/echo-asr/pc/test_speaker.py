"""読み上げエンジン（speaker.py）のテスト。VOICEVOX と PowerShell は偽物に差し替える。"""
import io
import json
import pathlib
import tempfile
import unittest
import urllib.error
import wave

import speaker


def wav(frames=b'\x01\x00' * 100, rate=24000, channels=1):
    out = io.BytesIO()
    with wave.open(out, 'wb') as w:
        w.setnchannels(channels)
        w.setsampwidth(2)
        w.setframerate(rate)
        w.writeframes(frames)
    return out.getvalue()


class Resp(io.BytesIO):
    status = 200

    def __enter__(self):
        return self

    def __exit__(self, *a):
        return False


class FakeEngine:
    """VOICEVOX の HTTP の偽物。started になるまで /version は失敗する。"""

    def __init__(self, up=False):
        self.up = up
        self.requests = []

    def urlopen(self, req, timeout=None):
        url = req if isinstance(req, str) else req.full_url
        self.requests.append(url)
        if not self.up:
            raise urllib.error.URLError('refused')
        if url.endswith('/version'):
            return Resp(b'"0.26.2"')
        if '/audio_query' in url:
            return Resp(json.dumps({'speedScale': 1.0, 'outputStereo': True}).encode())
        if '/synthesis' in url:
            self.query = json.loads(req.data)
            return Resp(wav())
        raise AssertionError(url)


class FakeProc:
    def __init__(self, engine):
        self.engine, self.code, self.terminated = engine, None, False
        engine.up = True

    def poll(self):
        return self.code

    def terminate(self):
        self.terminated, self.code = True, 0
        self.engine.up = False

    def wait(self, timeout=None):
        return self.code


class VoicevoxTests(unittest.TestCase):
    def make(self, engine, **kw):
        procs = []

        def popen(cmd, **_):
            self.assertIn('--port', cmd)
            procs.append(FakeProc(engine))
            return procs[-1]
        v = speaker.Voicevox(pathlib.Path('run.exe'), urlopen=engine.urlopen, popen=popen, sleep=lambda s: None,
                             idle_seconds=0, **kw)
        return v, procs

    def test_starts_engine_on_demand_and_reads_mono(self):
        engine = FakeEngine()
        v, procs = self.make(engine, speaker=30, speed=1.1)
        pcm, rate = v.synthesize('こんにちは')
        self.assertEqual((len(pcm), rate), (200, 24000))
        self.assertEqual(len(procs), 1)
        self.assertIn('speaker=30', [u for u in engine.requests if '/synthesis' in u][0])
        self.assertEqual((engine.query['outputStereo'], engine.query['speedScale']), (False, 1.1))
        v.synthesize('もう一度')
        self.assertEqual(len(procs), 1)  # 動いている間は起動し直さない

    def test_uses_an_engine_someone_else_runs(self):
        engine = FakeEngine(up=True)
        v, procs = self.make(engine)
        v.synthesize('はい')
        self.assertEqual(procs, [])
        v.close()  # 他人のエンジンは止めない
        self.assertTrue(engine.up)

    def test_numeric_voice_selects_speaker(self):
        engine = FakeEngine(up=True)
        v, _ = self.make(engine)
        v.synthesize('はい', '3')
        self.assertIn('speaker=3', [u for u in engine.requests if '/synthesis' in u][0])

    def test_idle_engine_is_stopped(self):
        engine = FakeEngine()
        now = [0.0]
        v, procs = self.make(engine, clock=lambda: now[0])
        v.idle_seconds = 1800
        v.synthesize('はい')
        now[0] = 1801
        with v._lock:
            if v._proc is not None and v._clock() - v._last_used > v.idle_seconds:
                v._stop_locked()
        self.assertTrue(procs[0].terminated)
        v.synthesize('また')
        self.assertEqual(len(procs), 2)

    def test_missing_engine_fails(self):
        v = speaker.Voicevox(None, urlopen=FakeEngine().urlopen, idle_seconds=0)
        with self.assertRaises(speaker.SpeechFailed):
            v.synthesize('はい')

    def test_engine_that_never_answers_times_out_and_is_stopped(self):
        engine = FakeEngine()
        now = [0.0]

        def popen(cmd, **_):
            p = FakeProc(engine)
            engine.up = False  # 起動したが応答しない
            procs.append(p)
            return p
        procs = []

        def sleep(s):
            now[0] += s
        v = speaker.Voicevox(pathlib.Path('run.exe'), urlopen=engine.urlopen, popen=popen, sleep=sleep,
                             clock=lambda: now[0], start_timeout=5, idle_seconds=0)
        with self.assertRaises(speaker.SpeechFailed):
            v.synthesize('はい')
        self.assertTrue(procs[0].terminated)


class FailureMemoryTests(unittest.TestCase):
    def test_failed_start_is_not_retried_for_a_while(self):
        engine = FakeEngine()
        now = [0.0]
        procs = []

        def popen(cmd, **_):
            p = FakeProc(engine)
            engine.up = False  # 起動したが応答しない
            procs.append(p)
            return p

        def sleep(s):
            now[0] += s
        v = speaker.Voicevox(pathlib.Path('run.exe'), urlopen=engine.urlopen, popen=popen, sleep=sleep,
                             clock=lambda: now[0], start_timeout=5, idle_seconds=0)
        with self.assertRaises(speaker.SpeechFailed):
            v.synthesize('はい')
        with self.assertRaises(speaker.SpeechFailed):
            v.synthesize('もう一度')
        self.assertEqual(len(procs), 1)  # 失敗を覚えている間は起動し直さない
        now[0] += speaker.FAILED_FOR
        with self.assertRaises(speaker.SpeechFailed):
            v.synthesize('しばらく後')
        self.assertEqual(len(procs), 2)


class SpeakerTests(unittest.TestCase):
    def test_recently_used(self):
        now = [0.0]

        class Works:
            def synthesize(self, text, voice=None):
                return b'', 24000
        s = speaker.Speaker([Works()], clock=lambda: now[0])
        self.assertFalse(s.recently_used())
        s.synthesize('はい')
        self.assertTrue(s.recently_used())
        now[0] += speaker.RECENT
        self.assertFalse(s.recently_used())

    def test_falls_back_in_order(self):
        class Broken:
            def synthesize(self, text, voice=None):
                raise speaker.SpeechFailed('down')

        class Works:
            def synthesize(self, text, voice=None):
                return b'\x00\x00', 22050
        s = speaker.Speaker([Broken(), Works()])
        self.assertEqual(s.synthesize('はい'), (b'\x00\x00', 22050))

    def test_text_is_tidied_and_bounded(self):
        seen = []

        class Works:
            def synthesize(self, text, voice=None):
                seen.append(text)
                return b'', 22050
        speaker.Speaker([Works()]).synthesize('一行目\n  二行目' + 'あ' * 5000)
        self.assertTrue(seen[0].startswith('一行目 二行目'))
        self.assertEqual(len(seen[0]), speaker.MAX_TEXT)

    def test_empty_text_fails(self):
        with self.assertRaises(speaker.SpeechFailed):
            speaker.Speaker([]).synthesize('  ')

    def test_stereo_wav_rejected(self):
        with self.assertRaises(speaker.SpeechFailed):
            speaker.read_wav(wav(frames=b'\x00\x00' * 4, channels=2))


class SapiTests(unittest.TestCase):
    def test_text_goes_by_environment_and_file_is_removed(self):
        seen = {}

        def run(cmd, env, **_):
            seen['text'], seen['path'] = env['HEY_MUSE_TEXT'], env['HEY_MUSE_WAV']
            pathlib.Path(env['HEY_MUSE_WAV']).write_bytes(wav(rate=22050))

            class R:
                returncode = 0
            return R()
        with tempfile.TemporaryDirectory() as d:
            pcm, rate = speaker.Sapi(run=run, tempdir=d).synthesize('"引用" と $env:X')
            self.assertEqual(rate, 22050)
            self.assertEqual(seen['text'], '"引用" と $env:X')
            self.assertFalse(pathlib.Path(seen['path']).exists())


class DefaultEngineTests(unittest.TestCase):
    def test_finds_winget_layout(self):
        with tempfile.TemporaryDirectory() as d:
            self.assertIsNone(speaker.default_engine(d))
            run = pathlib.Path(d, 'Microsoft/WinGet/Packages/HiroshibaKazuyuki.VOICEVOX.CPU_x/VOICEVOX/vv-engine/run.exe')
            run.parent.mkdir(parents=True)
            run.write_bytes(b'')
            self.assertEqual(speaker.default_engine(d), run)


if __name__ == '__main__':
    unittest.main()
