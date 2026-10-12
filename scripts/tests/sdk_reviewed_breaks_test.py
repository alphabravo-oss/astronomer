#!/usr/bin/env python3
import hashlib
import importlib.util
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('review', ROOT/'scripts/filter-sdk-reviewed-breaks.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class ReviewedSDKBreaksTest(unittest.TestCase):
    def setUp(self):
        self.before = b'type ToolResponse struct {\n\tJSON200 *interface{}\n\tBody []byte\n}\n'
        self.after = b'type ToolResponse struct {\n\tBody []byte\n}\n'
        self.review = {'baseline_sha256': hashlib.sha256(self.before).hexdigest(),
                       'current_sha256': hashlib.sha256(self.after).hexdigest(),
                       'removed_fields': {'ToolResponse': ['JSON200']}}

    def test_preserves_unreviewed_fields(self):
        filtered, count = module.reviewed_baseline(self.before, self.after, self.review)
        self.assertEqual(count, 1)
        self.assertNotIn('JSON200', filtered)
        self.assertIn('Body []byte', filtered)

    def test_changed_current_or_baseline_requires_review(self):
        for before, after in [(self.before+b'\n', self.after), (self.before, self.after+b'\n')]:
            with self.subTest(before=before, after=after), self.assertRaises(ValueError):
                module.reviewed_baseline(before, after, self.review)

    def test_unreviewed_field_is_rejected(self):
        self.review['removed_fields']['ToolResponse'] = ['Body']
        with self.assertRaises(ValueError):
            module.reviewed_baseline(self.before, self.after, self.review)

    def test_missing_field_is_rejected(self):
        self.review['removed_fields'] = {'OtherResponse': ['JSON200']}
        with self.assertRaises(ValueError):
            module.reviewed_baseline(self.before, self.after, self.review)

    def test_only_exact_data_envelope_is_migrated(self):
        before = b'type CharlieResponse struct {\n\tJSON200 *Payload\n\tBody []byte\n}\n'
        after = b'type CharlieResponse struct {\n\tJSON200 *struct {\n\t\tData Payload `json:"data"`\n\t}\n\tBody []byte\n}\n'
        review = {'baseline_sha256': hashlib.sha256(before).hexdigest(),
                  'current_sha256': hashlib.sha256(after).hexdigest(),
                  'removed_fields': {}, 'wrapped_fields': {'CharlieResponse': ['JSON200']}}
        migrated, removed = module.reviewed_baseline(before, after, review)
        self.assertEqual(migrated, after.decode())
        self.assertEqual(removed, 0)
        changed = after.replace(b'Data Payload', b'Data DifferentPayload')
        review['current_sha256'] = hashlib.sha256(changed).hexdigest()
        with self.assertRaises(ValueError):
            module.reviewed_baseline(before, changed, review)

    def test_non_charlie_envelope_is_not_eligible(self):
        review = dict(self.review, removed_fields={}, wrapped_fields={'ToolResponse': ['JSON200']})
        with self.assertRaises(ValueError):
            module.reviewed_baseline(self.before, self.after, review)

    def test_unlisted_type_rename_is_not_eligible(self):
        review = dict(self.review, renamed_types={'ToolResponse': 'OtherResponse'})
        with self.assertRaises(ValueError):
            module.reviewed_baseline(self.before, self.after, review)


if __name__ == '__main__':
    unittest.main()
