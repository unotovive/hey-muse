import io
import json
import socket
import threading
import unittest
import unittest.mock
import recognizer as app

class FakeRecognizer:
    def transcribe(self, pcm, language):
        assert pcm == b'\x01\x00'*1600 and language == 'ja'
        return '日本語のテスト'

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
