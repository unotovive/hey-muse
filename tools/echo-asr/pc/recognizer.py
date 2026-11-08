"""Private Wyoming STT service for this Echo; audio is processed in RAM only."""
import argparse
import json
import logging
import socketserver
import threading
import time
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

class Recognizer:
    def __init__(self, model, device, compute_type):
        from faster_whisper import WhisperModel

        self.lock = threading.Lock()
        # A local directory is used offline; a model name is fetched from the hub on first use.
        self.model = WhisperModel(str(model), device=device, compute_type=compute_type,
                                 cpu_threads=6 if device == 'cpu' else 0,
                                 local_files_only=Path(str(model)).is_dir())

    def transcribe(self, pcm, language):
        import numpy as np

        samples = np.frombuffer(pcm, dtype='<i2').astype(np.float32)/32768
        if len(samples) < 1600 or float(np.max(np.abs(samples))) < .001:
            return ''
        if not self.lock.acquire(timeout=3):
            raise ValueError('recognizer busy')
        try:
            start = time.monotonic()
            segments, _ = self.model.transcribe(samples, language=language, beam_size=5,
                vad_filter=False, condition_on_previous_text=False, temperature=0)
            text = ''.join(s.text for s in segments).strip()
            LOG.info('transcribed audio_seconds=%.2f processing_seconds=%.2f', len(samples)/16000, time.monotonic()-start)
            return text
        finally:
            self.lock.release()

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
    allow_reuse_address = True
    daemon_threads = True
    request_queue_size = 2

    def __init__(self, address, recognizer, allowed):
        self.recognizer, self.allowed = recognizer, set(allowed)
        super().__init__(address, Handler)

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

def main():
    args = parse_args()
    logging.basicConfig(level=logging.INFO, format='%(asctime)s %(levelname)s %(message)s')
    model, device, compute_type = model_settings(args)
    LOG.info('loading model=%s device=%s compute_type=%s', model, device, compute_type)
    recognizer = Recognizer(model, device, compute_type)
    with Server((args.bind, args.port), recognizer, args.allow) as server:
        LOG.info('ready bind=%s port=%s model=%s device=%s compute_type=%s', args.bind, args.port, model, device, compute_type)
        server.serve_forever()

if __name__ == '__main__':
    main()
