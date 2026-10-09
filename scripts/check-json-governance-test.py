#!/usr/bin/env python3
import copy
import importlib.util
import pathlib
import unittest

path = pathlib.Path(__file__).with_name("check-json-governance.py")
spec = importlib.util.spec_from_file_location("json_governance", path)
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)


class JSONGovernanceTests(unittest.TestCase):
    def setUp(self):
        self.actual = {"columns": [{"model": "Record", "column": "payload", "storage":"json_slot"}], "requests": [], "json_calls": []}
        self.policy = {"schema_version": 1, "columns": [{"model": "Record", "column": "payload", "storage":"json_slot", "classification": "versioned_internal", "schema_version": 1, "owner": "domain", "schema": "record-v1", "reader_behavior": "typed_error"}], "requests": [], "nonrequest_decoder_exceptions": [], "json_modules": [], "json_sites": []}

    def test_new_persistence_requires_owner(self):
        self.actual["columns"].append({"model": "NewRecord", "column": "new_payload"})
        with self.assertRaisesRegex(ValueError, "JSON persistence drift"):
            gate.validate(self.actual, self.policy)

    def test_storage_representation_change_requires_review(self):
        self.actual["columns"][0]["storage"]="byte_slot"
        with self.assertRaisesRegex(ValueError,"storage representation drift"):
            gate.validate(self.actual,self.policy)

    def test_existing_column_requires_reader_behavior(self):
        del self.policy["columns"][0]["reader_behavior"]
        with self.assertRaisesRegex(ValueError, "lacks reader_behavior"):
            gate.validate(self.actual, self.policy)

    def test_raw_decoder_cannot_hide_behind_known_helper_name(self):
        self.policy["json_modules"]=[{"path":"internal/handler/new.go","owner":"api","scope":"reader"}]
        self.actual["json_calls"].append({"path": "internal/handler/new.go", "function": "decodeV1JSONBody", "operation": "j.NewDecoder", "arguments": ["request.Body"]})
        with self.assertRaisesRegex(ValueError, "noncanonical"):
            gate.validate(self.actual, self.policy)

    def test_changed_response_exception_is_not_blanket_allowed(self):
        row = {"path": "internal/handler/response.go", "function": "read", "operation": "json.NewDecoder", "arguments": ["response.Body"]}
        self.policy["nonrequest_decoder_exceptions"] = [copy.deepcopy(row)]
        self.policy["json_modules"]=[{"path":row["path"],"owner":"api","scope":"response reader"}]
        row["arguments"] = ["request.Body"]
        self.actual["json_calls"] = [row]
        with self.assertRaisesRegex(ValueError, "noncanonical"):
            gate.validate(self.actual, self.policy)

    def test_physical_JSON_cannot_be_excluded_as_binary(self):
        row=self.policy["columns"][0]
        row.update(classification="non_json",storage="byte_slot",sql_type="jsonb",exclusion_reason="invented binary exception")
        with self.assertRaisesRegex(ValueError,"physical JSON"):
            gate.validate(self.actual,self.policy)

    def test_new_serializer_module_requires_owner(self):
        self.actual["json_calls"].append({"path":"internal/new_writer.go","operation":"json.Marshal"})
        with self.assertRaisesRegex(ValueError,"serializer ownership drift"):
            gate.validate(self.actual,self.policy)

    def test_new_serializer_in_owned_module_requires_schema(self):
        self.policy["json_modules"]=[{"path":"internal/existing.go","owner":"domain","scope":"existing reader"}]
        self.actual["json_calls"]=[{"path":"internal/existing.go","function":"newWrite","operation":"json.Marshal","arguments":["newPayload"]}]
        with self.assertRaisesRegex(ValueError,"JSON site drift"):
            gate.validate(self.actual,self.policy)

    def test_read_only_exception_cannot_be_claimed_by_a_mutation(self):
        row={"key":"internal/handler/new.go:create:decodeV1JSONBody","classification":"strict_product","unknown_fields":"additive_v1","owner":"api","malformed_body":"deny_elevation_preserve_read_only_session"}
        self.policy["requests"]=[row]
        with self.assertRaisesRegex(ValueError,"not leased"):
            gate.validate(self.actual,self.policy)


if __name__ == "__main__":
    unittest.main()
