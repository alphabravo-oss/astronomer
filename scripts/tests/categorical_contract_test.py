import copy
import importlib.util
import json
import unittest

from pathlib import Path

MODULE_PATH = Path(__file__).resolve().parents[1] / 'categorical-contracts.py'
loader = importlib.util.spec_from_file_location('categorical_contracts', MODULE_PATH)
contract = importlib.util.module_from_spec(loader)
loader.loader.exec_module(contract)


class CategoricalContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.registry = json.loads(contract.REGISTRY.read_text())
        cls.spec = contract.yaml_document(contract.ROOT / 'docs/openapi.yaml')

    def reject(self, edit_registry=None, edit_spec=None):
        registry, spec = copy.deepcopy(self.registry), copy.deepcopy(self.spec)
        if edit_registry:
            edit_registry(registry)
        if edit_spec:
            edit_spec(spec)
        with self.assertRaises((ValueError, KeyError)):
            contract.validate_registry(registry, spec)

    def test_canonical_complete_sets_align(self):
        contract.validate_registry(self.registry, self.spec)

    def test_database_cannot_omit_or_invent_terminal_state(self):
        for values in [['pending', 'ready'], ['pending', 'aborted', 'rollback_failed', 'invented']]:
            self.reject(edit_spec=lambda s: s['components']['schemas']['DeliveryRolloutClusterState'].update(enum=values))

    def test_domain_symbol_mapping_is_complete_and_unique(self):
        self.reject(edit_registry=lambda r: r['categories'][0]['domain']['symbols'].pop('aborted'))
        self.reject(edit_registry=lambda r: r['categories'][0]['domain']['symbols'].update(aborted='RolloutDraft'))

    def test_duplicate_schema_and_wire_binding_rejected(self):
        self.reject(edit_registry=lambda r: r['categories'].append(copy.deepcopy(r['categories'][0])))
        self.reject(edit_registry=lambda r: r['categories'][0]['bindings'].append(copy.deepcopy(r['categories'][0]['bindings'][0])))

    def test_unknown_schema_fields_and_unowned_rows_rejected(self):
        self.reject(edit_registry=lambda r: r.update(extra=True))
        self.reject(edit_registry=lambda r: r['categories'][0].update(owner=''))
        self.reject(edit_registry=lambda r: r['categories'][0].update(extra=True))

    def test_go_string_alias_required_for_previous_string_consumers(self):
        self.reject(edit_spec=lambda s: s['components']['schemas']['DeliveryRolloutState'].pop('x-go-type'))

    def test_independent_wire_enum_is_rejected(self):
        self.reject(edit_spec=lambda s: s['components']['schemas']['DeliveryRollout']['properties']['state'].update(enum=['succeeded']))

    def test_database_source_cannot_escape_repository(self):
        self.reject(edit_registry=lambda r: r['categories'][0]['database'][0].update(path='../outside.sql'))

    def test_unsupported_categorical_union_requires_checked_adapter(self):
        spec = copy.deepcopy(self.spec)
        spec['components']['schemas']['DeliveryRollout']['properties']['state'] = {'oneOf': [{'$ref': '#/components/schemas/DeliveryRolloutState'}, {'type': 'string'}]}
        with self.assertRaisesRegex(ValueError, 'checked adapter'):
            contract.response_source(self.registry, spec)

    def test_documented_success_without_category_retains_null_plan(self):
        spec = copy.deepcopy(self.spec)
        operation = {'operationId': 'categoryFixture', 'responses': {'200': {'content': {'application/json': {'schema': {'$ref': '#/components/schemas/DeliveryRollout'}}}}, '204': {'description': 'No content'}}}
        spec['paths']['/fixture'] = {'get': operation}
        result = contract.response_source(self.registry, spec)
        self.assertIn('"204": null', result)
