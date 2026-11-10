"""受付ポートで受ける読み上げ（synthesize）と、答え手（LLM）への HTTP 中継のテスト。"""
import http.client
import http.server
import json
import socket
import threading
import unittest

import llm_door
import recognizer as app


class FakeSpeaker:
    def __init__(self):
        self.warmed = 0
        self.asked = []

    def warm(self):
        self.warmed += 1

    def synthesize(self, text, voice=None):
        self.asked.append((text, voice))
        return b'\x01\x00' * 40000, 24000  # 80000 バイト（3 回に分けて送られる）


class FakeRecognizer:
    def transcribe(self, pcm, language):
        return 'テスト'


class FakeOllama(http.server.BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'
    seen = []

    def do_POST(self):
        body = self.rfile.read(int(self.headers['Content-Length']))
        FakeOllama.seen.append((self.path, self.headers['Host'], json.loads(body)))
        out = json.dumps({'choices': [{'message': {'role': 'assistant', 'content': 'こんにちは'}}]}).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(out)))
        self.end_headers()
        self.wfile.write(out)

    def log_message(self, *args):
        pass


class DoorTests(unittest.TestCase):
    def setUp(self):
        FakeOllama.seen = []
        self.ollama = http.server.ThreadingHTTPServer(('127.0.0.1', 0), FakeOllama)
        threading.Thread(target=self.ollama.serve_forever, daemon=True).start()
        self.speaker = FakeSpeaker()
        self.upstream = llm_door.Upstream('127.0.0.1', self.ollama.server_address[1], model='test-model')
        self.server = app.Server(('127.0.0.1', 0), FakeRecognizer(), ['127.0.0.1'], self.speaker, self.upstream)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()
        self.ollama.shutdown()
        self.ollama.server_close()

    def wyoming(self):
        sock = socket.create_connection(self.server.server_address, timeout=3)
        self.addCleanup(sock.close)
        f = sock.makefile('rwb')
        self.addCleanup(f.close)
        return f

    def test_synthesize_streams_audio(self):
        f = self.wyoming()
        app.send_event(f, 'synthesize', {'text': 'おはよう', 'voice': {'name': 'default'}})
        kind, data, _ = app.read_event(f)
        self.assertEqual((kind, data), ('audio-start', {'rate': 24000, 'width': 2, 'channels': 1}))
        pcm = b''
        while True:
            kind, data, payload = app.read_event(f)
            if kind != 'audio-chunk':
                break
            self.assertEqual(data['rate'], 24000)
            pcm += payload
        self.assertEqual(kind, 'audio-stop')
        self.assertEqual(len(pcm), 80000)
        self.assertEqual(self.speaker.asked, [('おはよう', None)])

    def test_synthesize_passes_named_voice(self):
        f = self.wyoming()
        app.send_event(f, 'synthesize', {'text': 'はい', 'voice': {'name': '3'}})
        self.assertEqual(app.read_event(f)[0], 'audio-start')
        self.assertEqual(self.speaker.asked, [('はい', '3')])

    def test_describe_lists_tts(self):
        f = self.wyoming()
        app.send_event(f, 'describe', {})
        kind, data, _ = app.read_event(f)
        self.assertEqual(kind, 'info')
        self.assertEqual(data['tts'][0]['voices'][0]['languages'], ['ja'])

    def test_speech_failure_is_an_error_event(self):
        def broken(text, voice=None):
            raise RuntimeError('engine down')
        self.speaker.synthesize = broken
        f = self.wyoming()
        app.send_event(f, 'synthesize', {'text': 'はい'})
        self.assertEqual(app.read_event(f)[0], 'error')

    def test_transcribe_warms_speech_and_model(self):
        self.upstream._chatted_at = self.upstream._clock()  # 最近、会話の中継に使われた
        f = self.wyoming()
        app.send_event(f, 'transcribe', {'language': 'ja'})
        app.send_event(f, 'audio-start', app.FORMAT)
        app.send_event(f, 'audio-chunk', app.FORMAT, b'\x00\x10' * 1600)
        app.send_event(f, 'audio-stop', {})
        self.assertEqual(app.read_event(f)[0], 'transcript')
        for _ in range(50):
            if FakeOllama.seen and self.speaker.warmed:
                break
            threading.Event().wait(0.05)
        self.assertEqual(self.speaker.warmed, 1)
        self.assertEqual(FakeOllama.seen[0][0], '/api/generate')
        self.assertEqual(FakeOllama.seen[0][2], {'model': 'test-model'})

    def test_bare_transcribe_warms_up(self):
        # 端末は起動語を聞いた時点で transcribe だけを送って切る（wyoming.Warm）。話している間に準備させる
        self.upstream._chatted_at = self.upstream._clock()
        prepared = []
        self.server.recognizer.prepare = lambda: prepared.append(1)
        sock = socket.create_connection(self.server.server_address, timeout=3)
        f = sock.makefile('rwb')
        app.send_event(f, 'transcribe', {'language': 'ja'})
        f.close()
        sock.close()
        for _ in range(50):
            if prepared and self.speaker.warmed and FakeOllama.seen:
                break
            threading.Event().wait(0.05)
        self.assertEqual((len(prepared), self.speaker.warmed, FakeOllama.seen[0][0]), (1, 1, '/api/generate'))

    def http(self):
        conn = http.client.HTTPConnection(*self.server.server_address, timeout=3)
        self.addCleanup(conn.close)
        return conn

    def test_warm_up_skips_parts_not_used_lately(self):
        self.speaker.recently_used = lambda: False
        f = self.wyoming()
        app.send_event(f, 'transcribe', {'language': 'ja'})
        app.send_event(f, 'audio-start', app.FORMAT)
        app.send_event(f, 'audio-chunk', app.FORMAT, b'\x00\x10' * 1600)
        app.send_event(f, 'audio-stop', {})
        self.assertEqual(app.read_event(f)[0], 'transcript')
        threading.Event().wait(0.3)
        self.assertEqual((self.speaker.warmed, FakeOllama.seen), (0, []))  # どちらも最近使っていない

    def test_warm_up_waits_for_recognizer(self):
        class Slow(FakeRecognizer):
            gate = None

            def prepare(self):
                # 本物と同じく、prepare で新しい認識プロセスが起きる。warm が prepare より前だと待てない
                self.gate = threading.Event()

            def wait_ready(self, timeout):
                return self.gate is not None and self.gate.wait(timeout)
        self.server.recognizer = Slow()
        f = self.wyoming()
        app.send_event(f, 'transcribe', {'language': 'ja'})
        app.send_event(f, 'audio-start', app.FORMAT)
        app.send_event(f, 'audio-chunk', app.FORMAT, b'\x00\x10' * 1600)
        app.send_event(f, 'audio-stop', {})
        self.assertEqual(app.read_event(f)[0], 'transcript')
        threading.Event().wait(0.2)
        self.assertEqual(self.speaker.warmed, 0)  # 認識の準備ができるまでは起こさない
        self.server.recognizer.gate.set()
        for _ in range(50):
            if self.speaker.warmed:
                break
            threading.Event().wait(0.05)
        self.assertEqual(self.speaker.warmed, 1)

    def test_chat_is_relayed_with_default_model_and_local_host(self):
        conn = self.http()
        conn.request('POST', '/v1/chat/completions', body=json.dumps({'messages': [{'role': 'user', 'content': 'やあ'}]}),
                     headers={'Content-Type': 'application/json'})
        resp = conn.getresponse()
        self.assertEqual(resp.status, 200)
        self.assertEqual(json.loads(resp.read())['choices'][0]['message']['content'], 'こんにちは')
        path, host, body = FakeOllama.seen[0]
        self.assertEqual(path, '/v1/chat/completions')
        self.assertTrue(host.startswith('127.0.0.1'))
        self.assertEqual(body['model'], 'test-model')
        # 同じ接続でもう一度（端末の HTTP クライアントは接続を使い回す）
        conn.request('POST', '/v1/chat/completions', body=json.dumps({'model': 'other', 'messages': []}),
                     headers={'Content-Type': 'application/json'})
        resp = conn.getresponse()
        resp.read()
        self.assertEqual(resp.status, 200)
        self.assertEqual(FakeOllama.seen[1][2]['model'], 'other')

    def test_other_ollama_api_is_not_relayed(self):
        conn = self.http()
        conn.request('POST', '/api/delete', body=b'{"model":"x"}', headers={'Content-Type': 'application/json'})
        resp = conn.getresponse()
        resp.read()
        self.assertEqual(resp.status, 404)
        self.assertEqual(FakeOllama.seen, [])

    def test_upstream_down_is_bad_gateway(self):
        self.upstream.port = self.free_port()
        conn = self.http()
        conn.request('POST', '/v1/chat/completions', body=b'{"messages":[]}', headers={'Content-Type': 'application/json'})
        resp = conn.getresponse()
        resp.read()
        self.assertEqual(resp.status, 502)

    @staticmethod
    def free_port():
        s = socket.socket()
        s.bind(('127.0.0.1', 0))
        port = s.getsockname()[1]
        s.close()
        return port

    def test_openai_style_speech_returns_24k_pcm(self):
        conn = self.http()
        conn.request('POST', '/v1/audio/speech', body=json.dumps({'model': 'x', 'voice': 'alloy', 'input': 'こんにちは',
                     'response_format': 'pcm'}), headers={'Content-Type': 'application/json', 'Authorization': 'Bearer local'})
        resp = conn.getresponse()
        data = resp.read()
        self.assertEqual((resp.status, resp.getheader('Content-Type')), (200, 'audio/pcm'))
        self.assertEqual(len(data), 80000)
        self.assertEqual(self.speaker.asked, [('こんにちは', None)])

    def test_speech_in_other_formats_is_refused(self):
        conn = self.http()
        conn.request('POST', '/v1/audio/speech', body=json.dumps({'input': 'はい', 'response_format': 'mp3'}))
        resp = conn.getresponse()
        resp.read()
        self.assertEqual(resp.status, 400)

    def test_resample_to_24k(self):
        pcm = llm_door.to_rate(b'\x00\x10' * 22050, 22050)
        self.assertEqual(len(pcm), 2 * 24000)
        self.assertEqual(llm_door.to_rate(b'\x01\x00' * 10, 24000), b'\x01\x00' * 10)

    def test_health_and_no_connection_reuse(self):
        conn = self.http()
        conn.request('GET', '/health')
        resp = conn.getresponse()
        resp.read()
        self.assertEqual((resp.status, resp.getheader('Connection')), (200, 'close'))

    def test_bad_content_length_is_refused(self):
        sock = socket.create_connection(self.server.server_address, timeout=3)
        self.addCleanup(sock.close)
        sock.sendall(b'POST /v1/chat/completions HTTP/1.1\r\nHost: x\r\nContent-Length: abc\r\n\r\n')
        self.assertIn(b' 413 ', sock.recv(200))

    def test_http_is_refused_without_upstream(self):
        self.server.upstream = None
        self.server.speaker = None
        conn = self.http()
        conn.request('GET', '/v1/models')
        with self.assertRaises((http.client.RemoteDisconnected, ConnectionError)):
            conn.getresponse()



class WorkerEnvTests(unittest.TestCase):
    def test_worker_loads_cached_model_offline(self):
        import unittest.mock
        seen = {}

        class Proc:
            stdout = stderr = __import__('io').BytesIO(b'')
            stdin = None
            pid = 1

            def poll(self):
                return None

        def popen(cmd, **kw):
            seen.update(kw['env'])
            return Proc()
        with unittest.mock.patch.object(app.subprocess, 'Popen', popen):
            app.WorkerProxy(['worker']).prepare()
        self.assertEqual((seen['HF_HUB_OFFLINE'], seen['PYTHONUTF8']), ('1', '1'))


if __name__ == '__main__':
    unittest.main()
