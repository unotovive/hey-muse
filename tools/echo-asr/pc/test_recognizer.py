import io
import json
import logging
import logging.handlers
import os
import pathlib
import socket
import sys
import tempfile
import threading
import time
import unittest
import unittest.mock
import recognizer as app

class FakeRecognizer:
    def transcribe(self, pcm, language):
        assert pcm == b'\x01\x00'*1600 and language == 'ja'
        return '日本語のテスト'

FAKE = [sys.executable, str(pathlib.Path(__file__).with_name('fake_worker.py'))]
LOUD = (b'\x00\x10' * 1600)   # 4096 の振幅 × 1600 サンプル
LOUD2 = b'\x00\x10' * 3200   # 6400 バイト（古い応答との取り違え検出用）
SILENT = b'\x01\x00' * 1600   # 振幅 1

class ProtocolTests(unittest.TestCase):
    def setUp(self):
        self.server = app.Server(('127.0.0.1', 0), FakeRecognizer(), ['127.0.0.1'])
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.sock = socket.create_connection(self.server.server_address, timeout=3)
        self.file = self.sock.makefile('rwb')

    def tearDown(self):
        self.file.close()
        self.sock.close()
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()

    def test_round_trip(self):
        app.send_event(self.file, 'transcribe', {'language': 'ja'})
        app.send_event(self.file, 'audio-start', app.FORMAT)
        payload = b'\x01\x00'*1600
        self.file.write(json.dumps({'type':'audio-chunk','data':app.FORMAT,'payload_length':len(payload)}).encode()+b'\n'+payload)
        app.send_event(self.file, 'audio-stop', {})
        kind, data, _ = app.read_event(self.file)
        self.assertEqual((kind, data['text']), ('transcript', '日本語のテスト'))

    def test_wrong_sample_rate_rejected(self):
        app.send_event(self.file, 'transcribe', {})
        app.send_event(self.file, 'audio-start', {**app.FORMAT, 'rate':48000})
        self.assertEqual(app.read_event(self.file)[0], 'error')

    def test_oversized_event_rejected_before_body(self):
        self.file.write(json.dumps({'type':'audio-chunk','payload_length':app.MAX_AUDIO+1}).encode()+b'\n')
        self.file.flush()
        self.assertEqual(app.read_event(self.file)[0], 'error')

    def test_negative_length_rejected(self):
        with self.assertRaises(ValueError):
            app.read_event(io.BytesIO(b'{"type":"audio-chunk","payload_length":-1}\n'))

    def test_peer_allowlist(self):
        self.assertFalse(self.server.verify_request(None, ('192.168.0.99', 10000)))

    def test_transcribe_event_calls_prepare(self):
        rec = self.server.recognizer; rec.prepared = 0
        rec.prepare = lambda: setattr(rec, 'prepared', rec.prepared + 1)
        app.send_event(self.file, 'transcribe', {'language': 'ja'})
        app.send_event(self.file, 'audio-start', app.FORMAT)
        payload = b'\x01\x00'*1600
        self.file.write(json.dumps({'type':'audio-chunk','data':app.FORMAT,'payload_length':len(payload)}).encode()+b'\n'+payload)
        app.send_event(self.file, 'audio-stop', {})
        kind, data, _ = app.read_event(self.file)
        self.assertEqual((kind, data['text']), ('transcript', '日本語のテスト'))
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

    def test_concurrent_prepare_spawns_one_worker(self):
        self.proxy = app.WorkerProxy(FAKE + ['--idle-seconds', '0'])
        pids = []
        def run(): self.proxy.prepare(); pids.append(self.proxy.pid)
        threads = [threading.Thread(target=run) for _ in range(8)]
        for t in threads: t.start()
        for t in threads: t.join()
        self.assertEqual(len(set(pids)), 1)
        self.assertEqual(self.proxy.transcribe(LOUD, 'ja'), 'ja:3200')

    def test_silence_short_circuits_worker(self):
        self.proxy = app.WorkerProxy(['/nonexistent/python'])
        self.assertEqual(self.proxy.transcribe(SILENT, 'ja'), '')

    def test_timeout_keeps_worker(self):
        self.proxy = app.WorkerProxy(FAKE + ['--delay-ready', '2', '--idle-seconds', '0'], response_timeout=0.5)
        with self.assertRaises(app.WorkerTimeout):
            self.proxy.transcribe(LOUD, 'ja')
        pid = self.proxy.pid
        self.proxy.response_timeout = 10
        # 遅れて届く旧 id の 'ja:3200' を捨て、2 回目の 'ja:6400' を返す
        self.assertEqual(self.proxy.transcribe(LOUD2, 'ja'), 'ja:6400')
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

if __name__ == '__main__':
    unittest.main()
