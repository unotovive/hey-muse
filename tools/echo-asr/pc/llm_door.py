"""端末の「答え手」（OpenAI 互換の /v1/chat/completions）を、同じ PC の Ollama へ中継する。

母艦の受付ポート（Wyoming の 50024）に HTTP で来た要求だけをここで扱う。Windows の受信規則を
増やさずに済み、Ollama 自身は 127.0.0.1 だけで待ち受けたままにできる。中継するのは会話と
モデル一覧の 2 つだけで、それ以外の Ollama の API（モデルの削除や取得など）は通さない。

OpenAI 互換の読み上げ（/v1/audio/speech、24 kHz の PCM）もここで受け、speaker.py で読む。
Muse で答えるときの「Speech endpoint」に母艦を指定すれば、OpenAI の鍵なしで日本語を読める。
"""
import http.client
import http.server
import json
import logging
import threading
import time

LOG = logging.getLogger('muse-llm')
MAX_BODY = 4 * 1024 * 1024
ROUTES = {('POST', '/v1/chat/completions'), ('GET', '/v1/models')}
SPEECH = ('POST', '/v1/audio/speech')
SPEECH_RATE = 24000  # OpenAI の pcm 形式（16 bit リトルエンディアン、モノラル）


def to_rate(pcm, rate, target=SPEECH_RATE):
    """16 bit モノラル PCM を線形補間で target Hz にする（SAPI の 22050 Hz を 24 kHz にするため）。"""
    if rate == target or not pcm:
        return pcm
    from array import array
    src = array('h')
    src.frombytes(pcm[:len(pcm) // 2 * 2])
    n = len(src) * target // rate
    out = array('h', bytes(2 * n))
    last = len(src) - 1
    for i in range(n):
        pos = i * rate / target
        j = int(pos)
        k = min(j + 1, last)
        out[i] = int(src[j] + (src[k] - src[j]) * (pos - j))
    return out.tobytes()


class Upstream:
    """中継先（Ollama）。model の無い要求には既定のモデルを入れる。"""

    def __init__(self, host='127.0.0.1', port=11434, model='qwen3:4b-instruct-2507-q4_K_M', timeout=120.0,
                 connect=http.client.HTTPConnection, clock=time.monotonic):
        self.host, self.port, self.model, self.timeout = host, port, model, timeout
        self._connect, self._clock = connect, clock
        self._warmed_at = None
        self._chatted_at = None
        self._warm_lock = threading.Lock()

    def recently_used(self):
        """24 時間以内に会話の中継をしたか（Muse で答えているときは、会話の始まりに LLM を温めない）。"""
        return self._chatted_at is not None and self._clock() - self._chatted_at < 24 * 3600

    def forward(self, method, path, body):
        if path == '/v1/chat/completions':
            self._chatted_at = self._clock()
        if method == 'POST' and body:
            try:
                req = json.loads(body)
            except ValueError:
                return 400, 'application/json', b'{"error":"invalid json"}'
            if isinstance(req, dict) and not req.get('model'):
                req['model'] = self.model
                body = json.dumps(req).encode('utf-8')
        conn = self._connect(self.host, self.port, timeout=self.timeout)
        try:
            headers = {'Content-Type': 'application/json'} if body else {}
            conn.request(method, path, body=body, headers=headers)
            resp = conn.getresponse()
            return resp.status, resp.getheader('Content-Type', 'application/json'), resp.read(MAX_BODY)
        finally:
            conn.close()

    def warm(self):
        """会話の始まりに呼ぶ。モデルを GPU に読み込ませておく（1 分に 1 回まで、裏で）。"""
        with self._warm_lock:
            now = self._clock()
            if self._warmed_at is not None and now - self._warmed_at < 60:
                return
            self._warmed_at = now

        def run():
            try:
                status, _, _ = self.forward('POST', '/api/generate', json.dumps({'model': self.model}).encode())
                if status != 200:
                    LOG.warning('LLM warm-up returned %s', status)
            except Exception as exc:
                LOG.warning('LLM warm-up failed: %s', exc)
        threading.Thread(target=run, name='llm-warm', daemon=True).start()


class ChatDoor(http.server.BaseHTTPRequestHandler):
    """受付の接続をそのまま HTTP/1.1 として扱う。serve() で既存のソケットの上に作る。"""
    protocol_version = 'HTTP/1.1'
    server_version = 'hey-muse'
    sys_version = ''

    @classmethod
    def serve(cls, rfile, wfile, connection, client_address, upstream, speaker=None):
        door = cls.__new__(cls)
        door.rfile, door.wfile, door.connection, door.request = rfile, wfile, connection, connection
        door.client_address, door.server, door.upstream, door.speaker = client_address, None, upstream, speaker
        door.close_connection = True
        door.handle()

    def _reply(self, status, ctype, body):
        # 受付は 10 秒で接続を閉じるので、使い回しを許さない（閉じる瞬間に重なった要求は再送されない）
        self.close_connection = True
        self.send_response(status)
        self.send_header('Content-Type', ctype)
        self.send_header('Content-Length', str(len(body)))
        self.send_header('Connection', 'close')
        self.end_headers()
        self.wfile.write(body)
        self.wfile.flush()

    def _route(self, method):
        path = self.path.split('?', 1)[0]
        if (method, path) == ('GET', '/health'):
            # lib/mothership の起動待ち（mothership.json の Address がこの口のとき）
            self._reply(200, 'application/json', b'{"ok":true}')
            return
        speech = (method, path) == SPEECH and self.speaker is not None
        if not speech and ((method, path) not in ROUTES or self.upstream is None):
            self._reply(404, 'application/json', b'{"error":"not found"}')
            return
        try:
            length = int(self.headers.get('Content-Length') or 0)
        except ValueError:
            length = -1
        if length < 0 or length > MAX_BODY:
            self.close_connection = True
            self._reply(413, 'application/json', b'{"error":"too large"}')
            return
        body = self.rfile.read(length) if length else b''
        if speech:
            self._speak(body)
            return
        started = time.monotonic()
        try:
            status, ctype, data = self.upstream.forward(method, path, body)
        except (OSError, http.client.HTTPException) as exc:
            LOG.warning('LLM upstream failed: %s', exc)
            self._reply(502, 'application/json', b'{"error":"assistant model is not running"}')
            return
        LOG.info('llm %s %s -> %s in %.1fs', method, path, status, time.monotonic() - started)
        self._reply(status, ctype, data)

    def _speak(self, body):
        try:
            req = json.loads(body)
            text = str(req.get('input') or '')
            fmt = req.get('response_format') or 'pcm'
        except (ValueError, AttributeError):
            self._reply(400, 'application/json', b'{"error":"invalid json"}')
            return
        if fmt != 'pcm':
            self._reply(400, 'application/json', b'{"error":"only response_format pcm is served"}')
            return
        voice = req.get('voice')
        started = time.monotonic()
        try:
            pcm, rate = self.speaker.synthesize(text, voice if str(voice or '').isdigit() else None)
        except Exception as exc:
            LOG.warning('speech failed: %s', exc)
            self._reply(502, 'application/json', b'{"error":"speech failed"}')
            return
        pcm = to_rate(pcm, rate)
        LOG.info('speech chars=%d audio_seconds=%.1f in %.2fs', len(text), len(pcm) / 2 / SPEECH_RATE,
                 time.monotonic() - started)
        self._reply(200, 'audio/pcm', pcm)

    def do_POST(self):
        self._route('POST')

    def do_GET(self):
        self._route('GET')

    def log_message(self, fmt, *args):
        # pythonw.exe には stderr が無いので、既定の stderr 出力は使わない
        LOG.debug(fmt, *args)
