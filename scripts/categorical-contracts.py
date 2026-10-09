#!/usr/bin/env python3
"""Generate categorical representations from canonical OpenAPI; verify parity."""
import argparse
import json
import pathlib
import re
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
REGISTRY = ROOT / 'deploy/contracts/categories.json'


def yaml_document(path):
    reader = "const fs=require('node:fs'); const {createRequire}=require('node:module'); const load=createRequire(process.cwd()+'/frontend/package.json')('js-yaml').load; process.stdout.write(JSON.stringify(load(fs.readFileSync(0,'utf8'))));"
    return json.loads(subprocess.check_output(['node', '-e', reader], input=path.read_bytes(), cwd=ROOT))


def browser_format(source):
    return subprocess.check_output(['node', str(ROOT / 'frontend/node_modules/prettier/bin/prettier.cjs'), '--parser', 'typescript', '--config', str(ROOT / 'frontend/.prettierrc.json')], input=source.encode(), cwd=ROOT).decode()


def local_source(value, suffix):
    if not isinstance(value, str) or not value.endswith(suffix):
        raise ValueError('invalid categorical source path')
    path = ROOT / value
    if path.is_absolute() and (path.resolve() == ROOT.resolve() or ROOT.resolve() not in path.resolve().parents):
        raise ValueError('categorical source escapes repository')
    return path


def validate_registry(registry, spec):
    if set(registry) != {'schemaVersion', 'authority', 'categories'} or registry['schemaVersion'] != 1:
        raise ValueError('unsupported category registry schema')
    if registry['authority'] != 'docs/openapi.yaml#/components/schemas':
        raise ValueError('unexpected category authority')
    schemas = spec['components']['schemas']
    names = set()
    bound = set()
    for row in registry['categories']:
        allowed = {'schema', 'owner', 'bindings', 'database', 'unknownBehavior', 'wireCompatibility', 'domain', 'domainCheck'}
        if set(row) - allowed or not {'schema', 'owner', 'bindings', 'database', 'unknownBehavior', 'wireCompatibility'} <= set(row) or not row.get('owner') or not row.get('unknownBehavior') or not row.get('wireCompatibility'):
            raise ValueError('category requires closed owned compatibility metadata')
        name = row['schema']
        if name in names:
            raise ValueError('duplicate category schema')
        names.add(name)
        schema = schemas[name]
        values = schema.get('enum', [])
        if schema.get('type') != 'string' or not values or len(values) != len(set(values)) or not all(isinstance(v, str) for v in values):
            raise ValueError('category requires a nonempty unique string enum')
        if schema.get('x-go-type') != 'string':
            raise ValueError('category must preserve Go string alias compatibility')
        for binding in row['bindings']:
            if set(binding) != {'schema', 'property', 'goSDK'} or binding['goSDK'] not in ('inline', 'string'):
                raise ValueError('invalid category wire binding')
            key = binding['schema'], binding['property']
            if key in bound:
                raise ValueError('duplicate wire binding')
            bound.add(key)
            field = schemas[binding['schema']]['properties'][binding['property']]
            if field.get('type') != 'string' or field.get('$ref') != '#/components/schemas/' + name or 'enum' in field or 'allOf' in field:
                raise ValueError('wire field differs from categorical authority')
        for source in row['database']:
            if set(source) != {'path', 'constraint'}:
                raise ValueError('invalid database category source')
            sql = local_source(source['path'], '.sql').read_text()
            pattern = r'CONSTRAINT\s+' + re.escape(source['constraint']) + r'\s+CHECK[^\n]*?ARRAY\[(.*?)\]'
            match = re.search(pattern, sql)
            actual = re.findall(r"'([^']*)'", match.group(1)) if match else []
            if len(actual) != len(set(actual)) or set(actual) != set(values):
                raise ValueError('database CHECK differs from categorical authority: ' + name)
        domain = row.get('domain')
        if domain and (set(domain) != {'path', 'type', 'prefix', 'all', 'symbols'} or set(domain['symbols']) != set(values) or len(set(domain['symbols'].values())) != len(values) or not all(re.fullmatch('[A-Za-z][A-Za-z0-9]*', value) for value in [domain['type'], domain['prefix'], domain['all'], *domain['symbols'].values()])):
            raise ValueError('domain symbols differ from categorical authority')
        if domain and local_source(domain['path'], '.go') != ROOT / 'internal/delivery/model/enums.gen.go':
            raise ValueError('unexpected generated domain source')
        if row.get('domainCheck'):
            source = row['domainCheck']
            if set(source) != {'path', 'type'}:
                raise ValueError('invalid domain check metadata')
            data = local_source(source['path'], '.go').read_text()
            actual = re.findall(r'\b\w+\s+' + re.escape(source['type']) + r'\s*=\s*"([^"\n]+)"', data)
            if len(actual) != len(set(actual)) or set(actual) != set(values):
                raise ValueError('domain constants differ from categorical authority: ' + name)
    return registry


def model_source(registry, schemas):
    lines = ['// Code generated by scripts/categorical-contracts.py; DO NOT EDIT.', 'package model', '']
    for row in registry['categories']:
        if 'domain' not in row:
            continue
        d = row['domain']
        values = schemas[row['schema']]['enum']
        lines += ['type ' + d['type'] + ' string', '', 'const (']
        lines += ['\t' + d['symbols'][v] + ' ' + d['type'] + ' = ' + json.dumps(v) for v in values]
        lines += [')', '', 'var ' + d['all'] + ' = []' + d['type'] + '{']
        lines += ['\t' + d['symbols'][v] + ',' for v in values]
        lines += ['}', '', 'func (s ' + d['type'] + ') Valid() bool { return contains(' + d['all'] + ', s) }', '']
    return subprocess.check_output(['gofmt'], input='\n'.join(lines).encode()).decode()


def overlay_source(registry, schemas):
    document = yaml_document(ROOT / 'oapi-codegen.compatibility-base.overlay.yaml')
    for row in registry['categories']:
        for binding in row['bindings']:
            if binding['goSDK'] != 'inline':
                continue
            target = '$.components.schemas.' + binding['schema'] + '.properties.' + binding['property']
            document['actions'] += [
                {'target': target + '.$ref', 'description': 'Retain v1 Go SDK inline enum type identity.', 'remove': True},
                {'target': target, 'description': 'Generate the existing inline SDK type from canonical enum values.', 'update': {'type': 'string', 'enum': schemas[row['schema']]['enum']}},
            ]
    return json.dumps(document, indent=2) + '\n'


def rollout_action_matrix(registry, schemas):
    row = next(r for r in registry['categories'] if r['schema'] == 'DeliveryRolloutState')
    state_by_symbol = {symbol: value for value, symbol in row['domain']['symbols'].items()}
    source = (ROOT / 'internal/delivery/rollout/control.go').read_text()
    action_by_symbol = dict(re.findall(r'(Action\w+)\s+Action\s*=\s*"([^"\n]+)"', source))
    matrix = {value: [] for value in schemas[row['schema']]['enum']}
    transitions = re.findall(r'case (Action\w+):\s*return \[\]string\{(.*?)\}, string\(model\.\w+\), nil', source, re.S)
    if {action for action, _ in transitions} != set(action_by_symbol):
        raise ValueError('rollout action writer requires an explicit matrix adapter')
    for action, states in transitions:
        for symbol in re.findall(r'string\(model\.(\w+)\)', states):
            matrix[state_by_symbol[symbol]].append(action_by_symbol[action])
    return matrix


def browser_source(registry, schemas):
    lines = ['// Generated by scripts/categorical-contracts.py; DO NOT EDIT.', 'import type { OpenAPIComponents } from "./openapi.generated";', '', 'export const categoricalValues = {']
    for row in registry['categories']:
        lines += ['  ' + row['schema'] + ': ' + json.dumps(schemas[row['schema']]['enum']) + ',']
    lines += ['} as const;', 'export const rolloutActionMatrix = ' + json.dumps(rollout_action_matrix(registry, schemas), indent=2) + ' as const;', 'export type RolloutAction = (typeof rolloutActionMatrix)[keyof typeof rolloutActionMatrix][number];', '', 'export type CategoryName = keyof typeof categoricalValues;', 'export type CategoryValue<K extends CategoryName> = OpenAPIComponents["schemas"][K];', 'export type { OpenAPIReadComponents } from \"./openapi.generated\";', '']
    return '\n'.join(lines)


def response_source(registry, spec):
    categories = {r['schema'] for r in registry['categories']}
    schemas = spec['components']['schemas']
    def refs(value):
        if isinstance(value, dict):
            result = {value['$ref'].rsplit('/', 1)[1]} if '$ref' in value else set()
            for key in ('properties', 'items', 'allOf', 'oneOf', 'anyOf'):
                result |= refs(value.get(key))
            if not any(k in value for k in ('properties', 'items', 'allOf', 'oneOf', 'anyOf', '$ref')):
                for item in value.values():
                    result |= refs(item)
            return result
        if isinstance(value, list):
            return set().union(*(refs(v) for v in value)) if value else set()
        return set()
    reachable = set(categories)
    while True:
        added = {name for name, schema in schemas.items() if refs(schema) & reachable}
        if added <= reachable:
            break
        reachable |= added
    def plan(schema):
        if not schema:
            return None
        if '$ref' in schema:
            name = schema['$ref'].rsplit('/', 1)[1]
            return ({'ref': name, 'nullable': True} if schema.get('nullable') else {'ref': name}) if name in reachable else None
        nodes = []
        if 'allOf' in schema:
            nodes += [p for p in (plan(v) for v in schema['allOf']) if p]
        if 'oneOf' in schema or 'anyOf' in schema:
            alternatives = schema.get('oneOf', schema.get('anyOf'))
            if any(plan(v) for v in alternatives):
                raise ValueError('categorical union requires an explicit checked adapter')
        properties = {}
        for field, value in schema.get('properties', {}).items():
            child = plan(value)
            if child:
                properties[field] = {'required': field in schema.get('required', []), 'node': child}
        if properties:
            nodes.append({'properties': properties})
        if schema.get('type') == 'array' and plan(schema.get('items')):
            nodes.append({'items': plan(schema['items'])})
        node = None if not nodes else nodes[0] if len(nodes) == 1 else {'all': nodes}
        if node and schema.get('nullable'):
            node = dict(node, nullable=True)
        return node
    definitions = {name: {'category': name} if name in categories else plan(schemas[name]) for name in sorted(reachable)}
    operations = {}
    for item in spec['paths'].values():
        for method, operation in item.items():
            if method not in ('get', 'post', 'put', 'patch', 'delete', 'head', 'options'):
                continue
            responses = {}
            for status, response in operation.get('responses', {}).items():
                if re.fullmatch('2[0-9][0-9]', str(status)):
                    node = plan(response.get('content', {}).get('application/json', {}).get('schema'))
                    responses[str(status)] = node
            if any(responses.values()):
                operations[operation['operationId']] = responses
    return '// Generated by scripts/categorical-contracts.py; DO NOT EDIT.\n' + 'import type { CategoryName } from "./categories.generated";\nexport interface CategoricalNode { category?: CategoryName; ref?: string; nullable?: boolean; all?: CategoricalNode[]; properties?: Record<string, { required: boolean; node: CategoricalNode }>; items?: CategoricalNode; }\n' + 'export const categoricalDefinitions: Record<string, CategoricalNode> = ' + json.dumps(definitions, indent=2) + ';\n' + 'export const categoricalResponses: Record<string, Record<string, CategoricalNode | null>> = ' + json.dumps(operations, indent=2) + ';\n'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=('write', 'check', 'check-browser'))
    args = parser.parse_args()
    registry = json.loads(REGISTRY.read_text())
    spec = yaml_document(ROOT / 'docs/openapi.yaml')
    validate_registry(registry, spec)
    schemas = spec['components']['schemas']
    outputs = {
        ROOT / 'frontend/src/types/categories.generated.ts': browser_format(browser_source(registry, schemas)),
        ROOT / 'frontend/src/types/categorical-responses.generated.ts': browser_format(response_source(registry, spec)),
    }
    if args.mode != 'check-browser':
        outputs[ROOT / 'internal/delivery/model/enums.gen.go'] = model_source(registry, schemas)
        outputs[ROOT / 'oapi-codegen.overlay.yaml'] = overlay_source(registry, schemas)
    for path, text in outputs.items():
        if args.mode == 'write':
            path.write_text(text)
        elif not path.exists() or path.read_text() != text:
            raise ValueError('generated categorical representation differs: ' + str(path.relative_to(ROOT)))
    print('Categorical authority aligned: ' + str(len(registry['categories'])) + ' named categories')


if __name__ == '__main__':
    try:
        main()
    except (ValueError, KeyError, OSError, subprocess.CalledProcessError) as error:
        print(error, file=sys.stderr)
        sys.exit(1)
