#!/usr/bin/env python3
import copy
import datetime as dt
import importlib.util
import json
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("reviews", ROOT / "scripts/openapi-current-reviews.py")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class CurrentReviewTests(unittest.TestCase):
    def setUp(self):
        self.registry = json.loads((ROOT / "deploy/contracts/openapi-current-reviews.json").read_text())
        self.review = (ROOT / "docs/api-breaking-change-review.md").read_text()
        self.rows = [{key: row[key] for key in module.IDENTITY} for row in self.registry["entries"]]
        self.source = self.registry["sourceSpecificationDigest"]
        self.base = self.registry["baselineSpecifications"][0]["digest"]
        self.now = dt.datetime(2026, 10, 9, tzinfo=dt.timezone.utc)

    def run_review(self):
        return module.effective_review(self.registry, self.review, self.rows, self.source, self.base, self.now)

    def test_exact_current_reviews_and_history(self):
        effective = self.run_review()
        history = self.review.split(self.registry["historicalBoundary"])[0]
        self.assertTrue(effective.startswith(history))
        self.assertEqual(len(self.rows), 76)
        self.assertTrue(all(row["ignoreLine"] in effective for row in self.registry["entries"]))

    def test_expired_response_review_rejected_at_exact_deadline(self):
        self.now = dt.datetime(2026, 10, 16, tzinfo=dt.timezone.utc)
        with self.assertRaisesRegex(ValueError, "expired"):
            self.run_review()

    def test_inhibition_deadline_separate(self):
        self.rows = [row for row in self.rows if row["operation"] != "GET"]
        self.now = dt.datetime(2026, 10, 16, tzinfo=dt.timezone.utc)
        self.run_review()
        self.now = dt.datetime(2026, 11, 8, tzinfo=dt.timezone.utc)
        with self.assertRaisesRegex(ValueError, "expired"):
            self.run_review()

    def test_unused_expired_reviews_never_pass_to_oasdiff(self):
        self.rows = []
        self.now = dt.datetime(2026, 12, 1, tzinfo=dt.timezone.utc)
        result = self.run_review()
        self.assertNotIn(self.registry["entries"][0]["ignoreLine"], result)

    def test_source_binding_rejected(self):
        self.source = "sha256:" + "0" * 64
        with self.assertRaisesRegex(ValueError, "bound"):
            self.run_review()

    def test_base_binding_rejected(self):
        self.base = "sha256:" + "0" * 64
        with self.assertRaisesRegex(ValueError, "bound"):
            self.run_review()

    def test_other_future_value_never_ignored(self):
        row = copy.deepcopy(next(row for row in self.rows if row["id"] == "response-property-enum-value-added"))
        row["text"] = row["text"].replace("`aborted`", "`future_not_reviewed`")
        self.rows = [row]
        self.assertNotIn("future_not_reviewed", self.run_review())

    def test_wrong_rule_or_level_not_ignored(self):
        row = copy.deepcopy(self.rows[0])
        row["id"] = "unreviewed-rule"
        self.rows = [row]
        self.assertNotIn(self.registry["entries"][0]["ignoreLine"], self.run_review())
        row["id"] = self.registry["entries"][0]["id"]
        row["level"] = 1
        self.assertNotIn(self.registry["entries"][0]["ignoreLine"], self.run_review())

    def test_unclosed_entry_rejected(self):
        self.registry["entries"][0]["ignoreRule"] = "all-enums"
        with self.assertRaisesRegex(ValueError, "unclosed"):
            self.run_review()

    def test_duplicate_entry_rejected(self):
        self.registry["entries"].append(copy.deepcopy(self.registry["entries"][0]))
        with self.assertRaisesRegex(ValueError, "duplicate"):
            self.run_review()

    def test_utc_offsets_and_naive_dates_rejected(self):
        for value in ("2026-10-16T00:00:00", "2026-10-16T00:00:00+08:00"):
            with self.assertRaisesRegex(ValueError, "UTC"):
                module.instant(value)

    def test_history_bytes_and_undocumented_review_rejected(self):
        self.review = self.review.replace("# Reviewed", "# Changed", 1)
        with self.assertRaisesRegex(ValueError, "historical review bytes"):
            self.run_review()
        self.review = (ROOT / "docs/api-breaking-change-review.md").read_text() + "\n- GET /api/v1/future unrelated review\n"
        with self.assertRaisesRegex(ValueError, "differ"):
            self.run_review()


if __name__ == "__main__":
    unittest.main()
