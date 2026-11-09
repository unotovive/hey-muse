"""認識プロセス: faster-whisper を読み込み、stdin/stdout の JSON 行プロトコルで文字起こしを行う。

stdout は JSON 行専用。ログはすべて stderr に出す。一定時間要求が無ければ自分で終了し、GPU メモリを解放する。
"""
import argparse
import json
import logging
import os
import queue
import sys
import threading
import time
from pathlib import Path

import recognizer

LOG = logging.getLogger('muse-worker')
LANGUAGES = ('ja', 'ja-JP')


def add_cuda_dll_dirs():
    """Windows で pip 版 CUDA ライブラリ（cublas/cudnn）の bin を DLL 検索パスに足す。足したパスを返す。"""
    if sys.platform != 'win32':
        return []
    import importlib.util
    added = []
    for name in ('nvidia.cublas', 'nvidia.cudnn'):
        try:
            spec = importlib.util.find_spec(name)
        except (ImportError, ValueError):
            continue
        for base in (spec.submodule_search_locations or []) if spec else []:
            path = os.path.join(base, 'bin')
            if os.path.isdir(path):
                os.add_dll_directory(path)
                added.append(path)
    return added


def parse_request(line):
    """要求ヘッダ 1 行を (id, language, pcm_length) にする。不正なら ValueError。"""
    header = json.loads(line)
    if not isinstance(header, dict):
        raise ValueError('invalid header')
    rid, language, size = header.get('id'), header.get('language'), header.get('pcm_length')
    if type(rid) is not int:
        raise ValueError('invalid id')
    if type(size) is not int or not 0 <= size <= recognizer.MAX_AUDIO:
        raise ValueError('invalid pcm_length')
    if language not in LANGUAGES:
        raise ValueError('unsupported language')
    return rid, language, size


class Recognizer:
    def __init__(self, model, device, compute_type):
        add_cuda_dll_dirs()  # faster_whisper の import より前に必要
        from faster_whisper import WhisperModel

        # ローカルディレクトリならオフライン、モデル名なら初回にハブから取得する
        self.model = WhisperModel(str(model), device=device, compute_type=compute_type,
                                 cpu_threads=6 if device == 'cpu' else 0,
                                 local_files_only=Path(str(model)).is_dir())

    def transcribe(self, pcm, language):
        import numpy as np

        samples = np.frombuffer(pcm, dtype='<i2').astype(np.float32)/32768
        start = time.monotonic()
        segments, _ = self.model.transcribe(samples, language='ja', beam_size=5,
            vad_filter=False, condition_on_previous_text=False, temperature=0)
        text = ''.join(s.text for s in segments).strip()
        LOG.info('transcribed audio_seconds=%.2f processing_seconds=%.2f', len(samples)/16000, time.monotonic()-start)
        return text


def write_json(stream, obj):
    stream.write(json.dumps(obj, ensure_ascii=False).encode('utf-8') + b'\n')
    stream.flush()


def read_requests(stdin, requests):
    """stdin から要求を読んでキューに入れる。EOF・不正な入力で None を入れて終わる。"""
    try:
        while True:
            line = stdin.readline(recognizer.MAX_HEADER + 1)
            if not line:
                break
            rid, language, size = parse_request(line)
            pcm = stdin.read(size) if size else b''
            if len(pcm) != size:
                raise ValueError('incomplete pcm')
            requests.put((rid, language, pcm))
    except Exception as exc:
        LOG.warning('stdin closed: %s', type(exc).__name__)
    requests.put(None)


def serve(model, stdin, stdout, idle_seconds):
    requests = queue.Queue()
    # 読み取りスレッドは readline でブロックしたままでも戻れるよう daemon にする
    threading.Thread(target=read_requests, args=(stdin, requests), daemon=True).start()
    write_json(stdout, {'event': 'ready'})
    while True:
        try:
            item = requests.get(timeout=idle_seconds or None)
        except queue.Empty:
            LOG.info('idle for %s seconds, exiting', idle_seconds)
            return stdout
        if item is None:
            return stdout
        rid, language, pcm = item
        try:
            write_json(stdout, {'id': rid, 'text': model.transcribe(pcm, language)})
        except Exception as exc:
            LOG.warning('request failed: %s', type(exc).__name__)
            write_json(stdout, {'id': rid, 'error': type(exc).__name__})


def parse_args(argv=None):
    parser = argparse.ArgumentParser()
    parser.add_argument('--model', default='large-v3-turbo', help='faster-whisper model name')
    parser.add_argument('--model-dir', type=Path, default=None, help='local model directory (overrides --model)')
    parser.add_argument('--device', choices=('auto', 'cuda', 'cpu'), default='auto')
    parser.add_argument('--compute-type', default=None, help='default: float16 on cuda, int8 on cpu')
    parser.add_argument('--idle-seconds', type=int, required=True, help='exit after this many idle seconds (0: never)')
    parser.add_argument('--check', action='store_true', help='load the model, print ready and exit')
    return parser.parse_args(argv)


def main():
    args = parse_args()
    logging.basicConfig(stream=sys.stderr, level=logging.INFO, format='%(asctime)s %(levelname)s %(message)s')
    stdout = sys.stdout.buffer
    try:
        model, device, compute_type = recognizer.model_settings(args)
        LOG.info('loading model=%s device=%s compute_type=%s', model, device, compute_type)
        loaded = Recognizer(model, device, compute_type)
    except Exception as exc:
        LOG.exception('model load failed')
        write_json(stdout, {'event': 'fatal', 'error': '%s: %s' % (type(exc).__name__, exc)})
        sys.exit(1)
    if args.check:
        write_json(stdout, {'event': 'ready'})
        return
    serve(loaded, sys.stdin.buffer, stdout, args.idle_seconds)


if __name__ == '__main__':
    main()
