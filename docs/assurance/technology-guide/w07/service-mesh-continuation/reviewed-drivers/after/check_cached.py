"""Actual full-source inventories using exact cached parsers; no Go fallback."""
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import sys

kind, root, output = sys.argv[1], Path(sys.argv[2]).resolve(), Path(sys.argv[3])
assert kind in ('json', 'sql')
TOOLS = {
    'json': ('/root/.cache/go-build/7c/7cf966d9f1c4495df190ca35356460cb891b57a7e85d3011df0d3ee0e3c17d85-d/json-governance', '7cf966d9f1c4495df190ca35356460cb891b57a7e85d3011df0d3ee0e3c17d85'),
    'sql': ('/root/.cache/go-build/9a/9a0dae6eddd8d1cd01ca6466bc7981b2f24dd2fc5807b7b4af5eaab4c28d4492-d/sql-governance', '9a0dae6eddd8d1cd01ca6466bc7981b2f24dd2fc5807b7b4af5eaab4c28d4492'),
}
path, digest = TOOLS[kind]
assert hashlib.sha256(Path(path).read_bytes()).hexdigest() == digest
spec = importlib.util.spec_from_file_location('gate', root / 'scripts' / ('check-' + kind + '-governance.py'))
gate = importlib.util.module_from_spec(spec); spec.loader.exec_module(gate)
raw = subprocess.check_output([path, str(root)], cwd=root, timeout=45)
output.write_bytes(raw)
actual = json.loads(raw)
if kind == 'sql':
    gate.inventory = lambda target=root: actual
else:
    def cached(command, *args, **kwargs):
        assert command == ['go', 'run', './tools/json-governance']
        return raw.decode()
    gate.subprocess.check_output = cached
try:
    gate.main()
except ValueError as error:
    print(kind + '-governance: ' + str(error), file=sys.stderr)
    raise SystemExit(1)
