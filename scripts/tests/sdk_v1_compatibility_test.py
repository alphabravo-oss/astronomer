import os
import pathlib
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]


class LegacySDKCompileFixtureTests(unittest.TestCase):
    def compile_fixture(self, baseline):
        with tempfile.TemporaryDirectory(prefix='.sdk-negative-', dir=ROOT) as directory:
            path = pathlib.Path(directory)
            (path / 'baseline.source').write_text(baseline)
            generated = subprocess.run(['go', 'run', './scripts/sdk-compatibility', str(path / 'baseline.source'), str(path / 'legacy_test.go'), 'pkg/astroclient/astroclient.gen.go'], cwd=ROOT, capture_output=True, text=True, check=True)
            self.assertIn('Legacy fixture:', generated.stdout)
            return subprocess.run(['go', 'test', directory], cwd=ROOT, capture_output=True, text=True)

    def test_existing_pointer_layout_compiles(self):
        result = self.compile_fixture('package astroclient\ntype InhibitionMatcher struct { Label string; Value string }\ntype InhibitionRequest struct { SourceMatchers *[]InhibitionMatcher; TargetMatchers *[]InhibitionMatcher; EqualLabels *[]string; Enabled *bool }\n')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_pointer_to_slice_break_is_detected(self):
        result = self.compile_fixture('package astroclient\ntype InhibitionMatcher struct { Label string; Value string }\ntype InhibitionRequest struct { SourceMatchers []InhibitionMatcher }\n')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('cannot use', result.stdout + result.stderr)

    def test_changed_method_signature_is_detected(self):
        result = self.compile_fixture('package astroclient\nimport("context";"net/http")\ntype Client struct{}\nfunc (c *Client) GetAuthMe(context.Context) (*http.Response,error) { return nil,nil }\n')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('cannot use', result.stdout + result.stderr)

    def test_changed_field_type_is_detected(self):
        result = self.compile_fixture('package astroclient\ntype DeliveryRollout struct { State int }\n')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('cannot use', result.stdout + result.stderr)
