import json
import pathlib
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]

class ExternalUpgradeReportContract(unittest.TestCase):
    def test_v1_schema_retains_fields_and_distinguishes_operator_confirmation(self):
        schema = json.loads((ROOT / 'deploy/release/upgrade-evidence.schema.json').read_text())
        self.assertEqual(len(schema['required']), 12)
        self.assertFalse(schema['additionalProperties'])
        self.assertEqual(schema['properties']['schema_version'], {'const': 1})
        self.assertEqual(schema['properties']['database_restore_mode']['enum'], ['verified_copy', 'live_replaced', 'operator_confirmed_external'])

    def test_operator_confirmation_never_satisfies_destructive_rehearsal_gate(self):
        source = (ROOT / 'scripts/validate-rc-rehearsal-inner.py').read_text()
        self.assertIn('upgrade["database_restore_mode"] != "live_replaced"', source)
        self.assertNotIn('operator_confirmed_external', source)
        self.assertIn('exact successful upgrade', source)
