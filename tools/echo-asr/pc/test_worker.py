import io, json, sys, time, unittest
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
        out = w.serve(Model(), io.BytesIO(inp), io.BytesIO(), idle_seconds=0)
        lines = [json.loads(l) for l in out.getvalue().splitlines()]
        self.assertEqual(lines, [{'event': 'ready'}, {'id': 1, 'text': 'テキスト2'}, {'id': 2, 'text': 'テキスト0'}])

    def test_serve_exits_after_idle(self):
        class Model:
            def transcribe(self, pcm, language): return ''
        start = time.monotonic()
        w.serve(Model(), SlowEOF(), io.BytesIO(), idle_seconds=1)
        self.assertLess(time.monotonic() - start, 3)


class CliTest(unittest.TestCase):
    def test_parse_args_defaults(self):
        ns = w.parse_args(['--model', 'large-v3-turbo', '--device', 'cuda', '--idle-seconds', '1800'])
        self.assertEqual((ns.model, ns.device, ns.idle_seconds, ns.check), ('large-v3-turbo', 'cuda', 1800, False))


if __name__ == '__main__':
    unittest.main()
