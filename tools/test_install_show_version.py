import importlib.util, pathlib, unittest
spec = importlib.util.spec_from_file_location('install_show', pathlib.Path(__file__).with_name('install-show.py'))
mod = importlib.util.module_from_spec(spec); spec.loader.exec_module(mod)

class ShowVersionTest(unittest.TestCase):
    def test_ja_suffix_orders_after_upstream_and_by_number(self):
        self.assertLess(mod.show_version('v1.6.30'), mod.show_version('v1.6.30-ja.1'))
        self.assertLess(mod.show_version('v1.6.30-ja.1'), mod.show_version('v1.6.30-ja.2'))
        self.assertLess(mod.show_version('v1.6.30-ja.2'), mod.show_version('v1.6.31-ja.1'))
    def test_muse_suffix_still_parses(self):
        self.assertIsNotNone(mod.show_version('v1.0.0-muse.1'))
