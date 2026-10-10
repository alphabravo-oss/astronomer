#!/usr/bin/env python3
import copy
import datetime as dt
import hashlib
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
        # The fixture observes every recorded review at its review instant;
        # production continues to use the actual UTC authority clock.
        self.now = max(module.instant(row["reviewedAt"]) for row in self.registry["entries"])

    def run_review(self):
        return module.effective_review(self.registry, self.review, self.rows, self.source, self.base, self.now)

    def test_exact_current_reviews_and_history(self):
        effective = self.run_review()
        history = self.review.split(self.registry["historicalBoundary"])[0]
        self.assertTrue(effective.startswith(history))
        self.assertEqual(len(self.rows), 82)
        inherited = self.registry["entries"][:76]
        # Canonical entry digest from reviewed e0f92cf7; includes every old
        # identity, owner, review/expiry instant and evidence disposition.
        self.assertEqual(hashlib.sha256(json.dumps(inherited, sort_keys=True, separators=(",", ":")).encode()).hexdigest(),
                         "bd351949f443534299ddcd135d9b1e4924bc3ef42b397c47861903eaca43ec84")
        self.assertEqual(len(self.registry["entries"][76:]), 6)
        self.assertEqual({(row["id"], row["operation"], row["path"], row["level"]) for row in self.rows[76:]}, {
            ("request-parameter-max-set", "GET", "/api/v1/catalog/charts/", 3),
            ("request-parameter-max-set", "GET", "/api/v1/controllers/alerts", 3),
            ("response-property-max-length-increased", "GET", "/api/v1/controllers/alerts", 3),
            ("request-parameter-max-set", "GET", "/api/v1/controllers/silences", 3),
            ("response-property-max-length-increased", "GET", "/api/v1/controllers/silences", 3),
            ("response-body-all-of-removed", "GET", "/api/v1/catalog/charts/", 2),
        })
        self.assertTrue(all(row["ignoreLine"] in effective for row in self.registry["entries"]))

    def test_signed_cursor_reviews_expire_at_their_own_deadline(self):
        current = self.registry["entries"][76:]
        self.rows = [{key: row[key] for key in module.IDENTITY} for row in current]
        self.assertEqual({row["path"] for row in self.rows}, {
            "/api/v1/catalog/charts/", "/api/v1/controllers/alerts", "/api/v1/controllers/silences",
        })
        self.assertTrue(all(row["operation"] == "GET" for row in self.rows))
        self.now = dt.datetime(2026, 11, 8, 23, 59, 59, tzinfo=dt.timezone.utc)
        self.run_review()
        self.now = dt.datetime(2026, 11, 9, tzinfo=dt.timezone.utc)
        with self.assertRaisesRegex(ValueError, "expired"):
            self.run_review()

    def test_new_cursor_dispositions_never_ignore_changed_findings(self):
        for entry in self.registry["entries"][76:]:
            with self.subTest(path=entry["path"], rule=entry["id"]):
                row = {key: entry[key] for key in module.IDENTITY}
                row["text"] += " unreviewed variation"
                self.rows = [row]
                self.assertNotIn(entry["ignoreLine"], self.run_review())

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
