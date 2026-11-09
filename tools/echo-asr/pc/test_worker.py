import io, json, os, sys, threading, time, unittest
import worker as w


class SlowEOF:
    """readline が 5 秒ブロックしてから EOF を返す標準入力の代役。"""
    def readline(self, *args):
        time.sleep(5)
        return b''

    def read(self, *args):
        time.sleep(5)
        return b''


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
        out = w.serve(Model(), w.start_reader(io.BytesIO(inp)), io.BytesIO(), idle_seconds=0)
        lines = [json.loads(l) for l in out.getvalue().splitlines()]
        self.assertEqual(lines, [{'event': 'ready'}, {'id': 1, 'text': 'テキスト2'}, {'id': 2, 'text': 'テキスト0'}])

    def test_reader_drains_stdin_before_serve(self):
        # モデル読み込み中（serve 前）でも stdin を吸い出し、受付の大きな書き込みを詰まらせない
        r, wfd = os.pipe()
        with os.fdopen(r, 'rb') as rf, os.fdopen(wfd, 'wb') as wf:
            requests = w.start_reader(rf)
            pcm = b'\x00\x10' * 60000
            t = threading.Thread(target=lambda: (wf.write(b'{"id": 1, "language": "ja", "pcm_length": %d}\n' % len(pcm) + pcm), wf.flush()))
            t.start(); t.join(2)
            self.assertFalse(t.is_alive())
            self.assertEqual(requests.get(timeout=2)[:2], (1, 'ja'))
            wf.close()
            self.assertIsNone(requests.get(timeout=2))

    def test_serve_exits_after_idle(self):
        class Model:
            def transcribe(self, pcm, language): return ''
        start = time.monotonic()
        w.serve(Model(), w.start_reader(SlowEOF()), io.BytesIO(), idle_seconds=1)
        self.assertLess(time.monotonic() - start, 3)


class CliTest(unittest.TestCase):
    def test_parse_args_defaults(self):
        ns = w.parse_args(['--model', 'large-v3-turbo', '--device', 'cuda', '--idle-seconds', '1800'])
        self.assertEqual((ns.model, ns.device, ns.idle_seconds, ns.check), ('large-v3-turbo', 'cuda', 1800, False))


if __name__ == '__main__':
    unittest.main()
