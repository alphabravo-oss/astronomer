#!/usr/bin/env python3
"""Finite evidence tamper controls; no Go, DB, cluster or network execution."""
import copy
import importlib.util
import json
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('evidence_verify', ROOT / 'verify.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
index, members = module.verify(ROOT, ROOT.parents[4])
passed = []


def rejected(name, callback):
    try:
        callback()
    except (ValueError, KeyError, RuntimeError, TypeError):
        passed.append(name)
    else:
        raise AssertionError('tampered evidence accepted: ' + name)


def index_change(name, change):
    modified = copy.deepcopy(index)
    change(modified)
    rejected(name, lambda: module.load_artifacts(ROOT, modified))


def receipt_change(name, change):
    modified = dict(members)
    receipt = module.decode(members['corrected-pass', 'receipt.json'])
    change(receipt)
    modified['corrected-pass', 'receipt.json'] = json.dumps(receipt).encode()
    rejected(name, lambda: module.check_receipt(modified))


index_change('unknown-index-field', lambda d: d.update(extra=True))
index_change('missing-index-field', lambda d: d.pop('kind'))
index_change('claim-inflation', lambda d: d.update(scanUsedWitness=True))
index_change('boolean-schema-version', lambda d: d.update(schemaVersion=True))
index_change('path-traversal', lambda d: d['artifacts'][0].update(path='../index.json'))
index_change('stored-hash', lambda d: d['artifacts'][0].update(storedSHA256='0' * 64))
index_change('original-hash', lambda d: d['artifacts'][0].update(originalSHA256='0' * 64))
index_change('oversized-member', lambda d: d['artifacts'][0].update(originalBytes=9 * 1024**2))
index_change('missing-original', lambda d: d['artifacts'].pop())
index_change('duplicate-original', lambda d: d['artifacts'].__setitem__(1, copy.deepcopy(d['artifacts'][0])))
receipt_change('unknown-receipt-field', lambda d: d.update(extra=True))
receipt_change('tool-drift', lambda d: d.update(cosignSHA256='0' * 64))
receipt_change('missing-gate', lambda d: d['gates'].pop())
receipt_change('unjoined-group', lambda d: d['gates'][0].update(drained=False))
receipt_change('count-drift', lambda d: d['gates'][3].update(cases=1))
receipt_change('source-changed', lambda d: d.update(sourceUnchanged=False))
receipt_change('missing-baseline-restoration', lambda d: d.update(baselineRestored=False))
receipt_change('supplier-claim-inflation', lambda d: d.update(supplierWholeTransfer=True))
with tempfile.TemporaryDirectory() as directory:
    link = Path(directory) / 'packet'
    link.symlink_to(ROOT, target_is_directory=True)
    rejected('symlink-packet-root', lambda: module.load_artifacts(link, index))
selection = module.decode(members['corrected-pass', 'source-selection.json'])
receipt = module.decode(members['corrected-pass', 'receipt.json'])
selection['tree'] = '0' * 40
rejected('changed-tree-digest', lambda: module.check_source(selection, receipt, ROOT.parents[4]))
selection = module.decode(members['corrected-pass', 'source-selection.json'])
selection['paths'][0]['workingSHA256'] = '0' * 64
rejected('changed-source-bytes', lambda: module.check_source(selection, receipt, ROOT.parents[4]))
rejected('duplicate-json-key', lambda: module.decode(b'{"a":1,"a":2}'))
print('PASS: 1 complete packet and ' + str(len(passed)) + ' negative schema/binding/claim controls')
