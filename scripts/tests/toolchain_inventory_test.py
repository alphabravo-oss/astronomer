import json
import pathlib
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1]))
import toolchain_inventory as inventory


class ConsumerInventoryTests(unittest.TestCase):
    def test_new_changed_removed_and_unknown_source_pins(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            source = root / 'Dockerfile'
            source.write_text('FROM node:26.11.1\n')
            document = {'components': [{'id': 'node', 'selected': '26.11.1', 'consumers': [{'path': 'Dockerfile', 'kind': 'regex', 'selector': r'^FROM (node:26\.11\.1)$', 'value': 'node:26.11.1'}]}], 'inventory': [{'path': 'Dockerfile', 'kind': 'builder', 'value': 'node:26.11.1', 'state': 'managed', 'component': 'node'}]}
            with patch.object(inventory, 'paths', return_value=['Dockerfile']):
                inventory.check(document, root)
                source.write_text('FROM node:27.0.0\n')
                with self.assertRaisesRegex(ValueError, 'uninventoried/changed'):
                    inventory.check(document, root)
                source.write_text('FROM node:26.11.1\nARG NEW_VERSION=1.2.3\n')
                with self.assertRaisesRegex(ValueError, 'uninventoried/changed'):
                    inventory.check(document, root)
                source.write_text('FROM node:26.11.1\n')
                document['inventory'][0]['component'] = 'unknown'
                with self.assertRaisesRegex(ValueError, 'unknown authority'):
                    inventory.check(document, root)

    def test_managed_pin_must_match_declared_component_consumer_and_count(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            source = root / 'Dockerfile'
            source.write_text('FROM node:26.11.1\nFROM node:20.0.0\n')
            document = {'components': [{'id': 'node', 'selected': '26.11.1', 'consumers': [{'path': 'Dockerfile', 'kind': 'regex', 'selector': r'^FROM (node:26\.11\.1)$', 'value': 'node:26.11.1'}]}], 'inventory': [{'path': 'Dockerfile', 'kind': 'builder', 'value': version, 'state': 'managed', 'component': 'node', 'count': 1} for version in ['node:26.11.1', 'node:20.0.0']]}
            with patch.object(inventory, 'paths', return_value=['Dockerfile']):
                with self.assertRaisesRegex(ValueError, 'consumer provenance'):
                    inventory.check(document, root)
                source.write_text('FROM node:26.11.1\nFROM node:26.11.1\n')
                document['inventory'].pop()
                with self.assertRaisesRegex(ValueError, 'occurrence count'):
                    inventory.check(document, root)

    def test_schema_properties_are_not_image_consumers(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / 'schema.yaml').write_text('image:\n  type: string\n  image: postgres:16-alpine\n')
            rows = inventory.scan(root, ['schema.yaml'])
            self.assertEqual([row['value'] for row in rows], ['postgres:16-alpine'])


if __name__ == '__main__':
    unittest.main()
