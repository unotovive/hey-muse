"""テスト専用: worker.py のプロトコルを話す偽ワーカー（標準ライブラリのみ。GPU も faster-whisper も不要）。"""
import argparse
import json
import queue
import sys
import threading
import time


def write_json(stream, obj):
    stream.write(json.dumps(obj, ensure_ascii=False).encode('utf-8') + b'\n')
    stream.flush()


def read_requests(stdin, requests):
    """stdin から (id, pcm_length) を読んでキューに入れる。EOF・不正入力で None を入れて終わる。"""
    try:
        while True:
            line = stdin.readline()
            if not line:
                break
            header = json.loads(line)
            size = header['pcm_length']
            if len(stdin.read(size)) != size:
                break
            requests.put((header['id'], size))
    except Exception:
        pass
    requests.put(None)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--delay-ready', type=float, default=0)
    parser.add_argument('--idle-seconds', type=float, default=0)
    parser.add_argument('--die-on', type=int, default=0)
    parser.add_argument('--slow', type=float, default=0)
    parser.add_argument('--hang-on', type=int, default=0, help='N 件目の要求で答えずに眠り続ける')
    parser.add_argument('--stderr-line', default=None)
    args = parser.parse_args()
    stdout = sys.stdout.buffer
    if args.stderr_line is not None:
        sys.stderr.buffer.write(args.stderr_line.encode('utf-8') + b'\n')
        sys.stderr.buffer.flush()
    time.sleep(args.delay_ready)
    requests = queue.Queue()
    threading.Thread(target=read_requests, args=(sys.stdin.buffer, requests), daemon=True).start()
    write_json(stdout, {'event': 'ready'})
    count = 0
    while True:
        try:
            item = requests.get(timeout=args.idle_seconds or None)
        except queue.Empty:
            return
        if item is None:
            return
        count += 1
        if args.die_on and count == args.die_on:
            sys.exit(3)
        if args.hang_on and count == args.hang_on:
            while True:
                time.sleep(60)
        time.sleep(args.slow)
        write_json(stdout, {'id': item[0], 'text': 'ja:%d' % item[1]})


if __name__ == '__main__':
    main()
