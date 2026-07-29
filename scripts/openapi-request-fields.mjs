#!/usr/bin/env node
// OpenAPI request-schema FIELD contract tool.
//
// Every other contract gate in verify_contract_artifacts is path-level:
// openapi-coverage.mjs proves a documented path is still routed, but never
// looks inside a request body. That hole let `NodeDrainRequest` ship without
// the `force` field the handler has decoded since it was written — every gate
// green. This tool closes it by comparing, field by field, each documented
// request schema against the Go struct that actually decodes it.
//
// ASSOCIATION — explicit marker on the Go struct, nothing inferred.
//
//   // openapi:request NodeDrainRequest
//   type drainNodeRequest struct { ... }
//
// The marker must appear in the doc comment immediately above a
// `type X struct {` or `var x struct {` line (a few handlers decode into an
// anonymous struct declared in the function body). It names a schema under
// `components.schemas`; a name that is not defined there is a hard failure, so
// a typo or a renamed schema is caught rather than silently un-checking the
// struct. One struct may carry several markers when several schemas decode
// into it (the three RBAC binding schemas share `roleBindingRequest`).
//
// Why a marker and not a naming convention: only 23 of the 47 named request
// schemas in docs/openapi.yaml share a name with their Go struct. The known
// live defect is one of the 24 that do not (`NodeDrainRequest` decodes into
// `drainNodeRequest`), so a convention-matched gate would have missed the very
// bug that motivated it — while a same-name collision between an unrelated
// Go struct and a schema would fail loudly for no reason. A marker has a
// structurally zero false-positive rate on association: a pair is only ever
// compared because someone wrote the pairing down. The cost is coverage, which
// is why coverage is itself gated — see the ratchet below.
//
// DIRECTIONS — both are drift, both fail:
//   undocumented : the Go struct decodes a field the spec does not describe
//                  (clients cannot discover it; this is the `force` class)
//   phantom      : the spec describes a field the Go struct will discard
//                  (a lie to clients; they send it and nothing happens)
//
// ESCAPE HATCH — per field, reason mandatory, in the source under review:
//
//   // openapi:request-allow legacy_name  DEBT: renamed in v2, still accepted
//   // openapi:request-allow RBACBindingRequest.cluster_id  global bindings ...
//
// One line per field, in the same doc comment as the binding. The bare form
// covers the field in every schema the struct binds; the `Schema.field` form
// covers it in exactly one, which is what you want when a shared struct serves
// schemas of different shapes. There is no per-endpoint or per-struct "skip",
// by design: a blanket skip is how a gate stops finding anything. A waiver
// that no longer suppresses drift fails too, so the list cannot rot into
// permanent noise.
//
// COVERAGE RATCHET — a gate is only as good as the surface it covers, so every
// way of escaping it is itself gated by the debt sets below:
//   UNBOUND_REQUEST_SCHEMAS        : documented request schema with no marker
//   PLACEHOLDER_REQUEST_SCHEMAS    : bound, but the schema declares no properties
//   EXPECTED_INLINE_REQUEST_BODIES : requestBody with an INLINE (unnameable) schema
// Adding a new documented request body therefore forces a choice that shows up
// in review — bind it and enumerate it, or write it down as debt. A bound schema
// that no requestBody $refs any more fails as well, which is what catches the
// other direction: converting a $ref'd body to an equivalent inline schema, the
// one edit that used to drop an endpoint out of the gate with nothing to show.
//
// NOT CHECKED — deliberately, to keep the false-positive rate at zero:
//   - field TYPES (bool vs boolean, *int64 vs integer/int64, json.RawMessage
//     vs anything) — too many defensible spellings to judge mechanically
//   - required/optional agreement (spec `required` vs pointer/omitempty)
//   - inline request bodies, and query/path parameters
//   - response schemas
//
// Modes:
//   (default)  print the report, exit non-zero only on a structural error
//   --check    exit non-zero on drift, a stale waiver, an unbound schema, a new
//              placeholder, an inline-body count that moved, a bound schema no
//              requestBody uses, or a debt entry that is no longer needed
//   --verbose  also list every bound pair and every waiver

import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(scriptDir, '..');
const requireFromFrontend = createRequire(path.join(repoRoot, 'frontend/package.json'));
const yaml = requireFromFrontend('js-yaml');

const args = new Set(process.argv.slice(2));
const check = args.has('--check');
const verbose = args.has('--verbose');

// Named request schemas with no `// openapi:request` binding yet.
//
// THIS IS A DEBT LIST, NOT AN APPROVAL. Every entry is a documented request
// body whose fields nothing verifies; the entry exists so the gate can fail on
// a NEW unbound schema instead of drowning in the pre-existing ones. Retiring
// an entry means adding the marker to the Go struct that decodes it and fixing
// whatever field drift that exposes. Do not add to this list to silence a gate
// failure on a schema you are actively adding — bind it instead.
const UNBOUND_REQUEST_SCHEMAS = new Set([]);

// Request schemas that are bound to a Go struct but describe no properties at
// all — `type: object` + `additionalProperties: true` placeholders carrying the
// "Schema not yet fully enumerated" description. There is nothing to compare
// field by field, so the gate can only report them.
//
// THIS IS A DEBT LIST, NOT AN APPROVAL. Each entry is an endpoint whose request
// body is documented as "any JSON object", which tells a client nothing. The fix
// is to enumerate the properties from the bound Go struct (named in the report)
// and delete the entry, at which point the field comparison starts running.
// Adding an entry to unblock a new placeholder is going backwards.
const PLACEHOLDER_REQUEST_SCHEMAS = new Set([
  'AllowlistUpdateRequest',
  'ApplyClusterTemplateRequest',
  'ApplyNetworkPolicyRequest',
  'ClusterRegistryRequest',
  'CreateClusterGroupRequest',
  'CreateClusterTemplateRequest',
  'CreateFleetOperationRequest',
  'CreateNetworkPolicyTemplateRequest',
  'MoveClustersRequest',
  'UpdateClusterGroupRequest',
  'UpdateClusterRequest',
  'UpdateRegistryConfigRequest',
]);

// Request bodies whose schema is declared INLINE on the operation instead of
// under components.schemas. An inline schema has no name, so it can carry no
// marker and not one of its fields is compared — it is the cheapest way to add a
// request body this gate never looks at, and at 70 of 141 bodies it is already
// the majority of the surface.
//
// THIS IS A DEBT BUDGET, NOT AN APPROVAL. Reporting the number was not enough:
// a number in a passing CI log is not visible, and the count could grow forever
// without failing anything. It is pinned here so a new inline body fails and
// has to be argued for. It also fails when the count DROPS — lower this line in
// the same commit that promotes a body to a named schema, so the budget cannot
// leave slack for the next unverified body to fill.
const EXPECTED_INLINE_REQUEST_BODIES = 70;

const specPath = path.join(repoRoot, 'docs/openapi.yaml');
const spec = yaml.load(fs.readFileSync(specPath, 'utf8'));
const schemas = spec?.components?.schemas;

if (!schemas || typeof schemas !== 'object' || Array.isArray(schemas)) {
  console.error('FAIL: docs/openapi.yaml has no components.schemas object.');
  process.exit(2);
}

const HTTP_METHODS = new Set(['get', 'put', 'post', 'delete', 'patch', 'head', 'options', 'trace']);
const JSON_MEDIA_TYPES = ['application/json', 'application/merge-patch+json', 'application/scim+json'];

const errors = [];       // structural problems in the markers or the spec
const drift = [];        // field-level drift, one entry per binding
const placeholders = []; // bound, but the schema documents no properties
const staleWaivers = [];

// ---------------------------------------------------------------------------
// Spec side
// ---------------------------------------------------------------------------

// requestBody usages, so the ratchet knows which schemas are part of the
// request surface (a schema used only in a response is not this gate's
// business) and so the report can name the operations a drifting schema
// affects.
const schemaUsage = new Map(); // schemaName -> Set("METHOD /path")
let inlineRequestBodies = 0;
let nonJSONRequestBodies = 0;

for (const [pattern, item] of Object.entries(spec?.paths ?? {})) {
  if (!item || typeof item !== 'object') continue;
  for (const [method, op] of Object.entries(item)) {
    if (!HTTP_METHODS.has(method.toLowerCase())) continue;
    const body = op?.requestBody;
    if (!body || typeof body !== 'object') continue;
    const content = body.content ?? {};
    const mediaType = JSON_MEDIA_TYPES.find((m) => content[m]);
    if (!mediaType) {
      nonJSONRequestBodies += 1;
      continue;
    }
    const schema = content[mediaType]?.schema;
    const ref = typeof schema?.$ref === 'string' ? schema.$ref : null;
    const name = ref?.startsWith('#/components/schemas/') ? ref.slice('#/components/schemas/'.length) : null;
    if (!name) {
      inlineRequestBodies += 1;
      continue;
    }
    if (!schemaUsage.has(name)) schemaUsage.set(name, new Set());
    schemaUsage.get(name).add(`${method.toUpperCase()} ${pattern}`);
  }
}

// specFields returns the wire field names a schema documents, or null when the
// schema is deliberately opaque (a free-form object with no properties, e.g. a
// passthrough values blob). Composition is unioned: allOf is a merge by
// definition, and for oneOf/anyOf the union is the set of names a client could
// legitimately send, which is what "does the struct accept it" needs.
function specFields(schema, seen = new Set()) {
  if (!schema || typeof schema !== 'object') return null;
  if (typeof schema.$ref === 'string') {
    const prefix = '#/components/schemas/';
    if (!schema.$ref.startsWith(prefix)) return null;
    const name = schema.$ref.slice(prefix.length);
    if (seen.has(name)) return new Set();
    seen.add(name);
    if (!Object.prototype.hasOwnProperty.call(schemas, name)) {
      errors.push(`docs/openapi.yaml: $ref to undefined schema ${name}`);
      return null;
    }
    return specFields(schemas[name], seen);
  }
  const out = new Set();
  let sawShape = false;
  if (schema.properties && typeof schema.properties === 'object') {
    sawShape = true;
    for (const key of Object.keys(schema.properties)) out.add(key);
  }
  for (const key of ['allOf', 'anyOf', 'oneOf']) {
    if (!Array.isArray(schema[key])) continue;
    for (const member of schema[key]) {
      const nested = specFields(member, seen);
      if (nested === null) continue;
      sawShape = true;
      for (const name of nested) out.add(name);
    }
  }
  return sawShape ? out : null;
}

// ---------------------------------------------------------------------------
// Go side
// ---------------------------------------------------------------------------

// Directories the Go toolchain itself ignores, plus the dependency and
// scratch trees. Skipping every dot-directory matters more than it looks:
// .claude/worktrees holds whole stale copies of this repo, and scanning one
// would bind a schema twice — once to the live struct and once to a snapshot of
// it — reporting drift that no longer exists anywhere a request can reach.
const SKIP_DIRS = new Set(['node_modules', 'vendor', 'worktrees', 'bin', 'testdata']);

function goFiles(dir, out = []) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (SKIP_DIRS.has(entry.name) || entry.name.startsWith('.') || entry.name.startsWith('_')) continue;
      goFiles(full, out);
      continue;
    }
    if (entry.isFile() && entry.name.endsWith('.go') && !entry.name.endsWith('_test.go')) out.push(full);
  }
  return out;
}

// Strip back-quoted struct tags, double-quoted strings and line comments so
// brace counting is not thrown off by a `{` inside any of them.
function stripNoise(line) {
  return line
    .replace(/`[^`]*`/g, '``')
    .replace(/"(?:[^"\\]|\\.)*"/g, '""')
    .replace(/\/\/.*$/, '');
}

const MARKER_BIND = /^\/\/\s*openapi:request\s+([A-Za-z_][A-Za-z0-9_]*)\s*$/;
const MARKER_ALLOW = /^\/\/\s*openapi:request-allow\s+([A-Za-z_][A-Za-z0-9_.]*)\s+(.*\S)\s*$/;
const MARKER_ANY = /^\/\/\s*openapi:request(-[a-z-]+)?\b/;
// A named type declaration, or the anonymous `var req struct {` a handful of
// handlers decode into. Both are explicit enough to hang a marker on.
const STRUCT_DECL = /^(?:type|var)\s+([A-Za-z_][A-Za-z0-9_]*)\s+struct\s*\{/;
const TYPE_DECL = /^type\s+([A-Za-z_][A-Za-z0-9_]*)\s+struct\s*\{/;

// A struct field line at depth 1: one or more field names, then a type. E.g.
//   Force bool `json:"force,omitempty"`   -> names ["Force"], tag json:"force"
const FIELD_LINE = /^([A-Za-z_][A-Za-z0-9_]*(?:\s*,\s*[A-Za-z_][A-Za-z0-9_]*)*)\s+(\S.*)$/;
// A bare embedded field: `SnapshotSpec`, `*SnapshotSpec` or `pkg.Spec`, with
// no field name and no tag.
const EMBED_LINE = /^\*?([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?)$/;
const JSON_TAG = /json:"([^"]*)"/;

// parseStructBody walks a struct body from its opening `{` and returns its
// depth-1 members, the index of the closing brace, and any parse problem.
//
// Members are either a wire field or an embedded type to resolve later.
// Anonymous nested structs (depth > 1) are skipped: they hang off a named
// parent field, so their own fields are not on this schema's top level.
//
// Problems are RETURNED rather than pushed onto `errors`: this runs over every
// struct in the repo to build the type index, and a struct nothing binds (and
// nothing embeds) is not this gate's business. The caller reports.
function parseStructBody(lines, openIndex, structName) {
  const members = [];
  let problem = null;

  // The opening line can also close the struct (`type X struct{}`) or, rarely,
  // carry the whole body on one line. Account for whatever follows the `{`.
  const openClean = stripNoise(lines[openIndex]);
  const tail = openClean.slice(openClean.indexOf('{') + 1);
  let depth = 1 + (tail.match(/[{([]/g) ?? []).length - (tail.match(/[})\]]/g) ?? []).length;
  if (depth <= 0) {
    if (tail.replace(/[{}()\[\]\s]/g, '') !== '') {
      problem = `${openIndex + 1}: single-line struct ${structName} cannot be field-checked; ` +
        'put each field on its own line';
    }
    return { members, closeIndex: openIndex, problem };
  }

  let j = openIndex;
  let closed = false;
  while (++j < lines.length) {
    const raw = lines[j];
    const clean = stripNoise(raw);

    if (depth === 1) {
      const text = raw.trim();
      if (text && !text.startsWith('//')) {
        const tag = JSON_TAG.exec(text);
        const field = FIELD_LINE.exec(text);
        const embed = EMBED_LINE.exec(text);
        if (field) {
          const names = field[1].split(',').map((s) => s.trim());
          const wire = tag ? tag[1].split(',')[0] : '';
          if (tag && wire === '-') {
            // json:"-" is not on the wire at all.
          } else if (tag && wire !== '') {
            if (names.length > 1) {
              problem = `${j + 1}: ${structName}: one json tag shared by several field names is not supported`;
            }
            members.push({ kind: 'field', wire, goName: names[0], line: j + 1 });
          } else {
            // No json tag, or a tag with only options: encoding/json uses the
            // Go field name verbatim for exported fields, so it IS wire surface.
            for (const name of names) {
              if (/^[A-Z]/.test(name)) members.push({ kind: 'field', wire: name, goName: name, line: j + 1 });
            }
          }
        } else if (embed) {
          members.push({ kind: 'embed', type: embed[1], line: j + 1 });
        }
      }
    }

    depth += (clean.match(/[{([]/g) ?? []).length;
    depth -= (clean.match(/[})\]]/g) ?? []).length;
    if (depth <= 0) {
      closed = true;
      break;
    }
  }
  if (!closed) problem = `${openIndex + 1}: unterminated struct ${structName}`;
  return { members, closeIndex: j, problem };
}

// The module path, so an import path can be mapped back to a directory in this
// repo and a QUALIFIED embed (`sharedtypes.Common`) can be resolved the same way
// an unqualified one is. Hoisting fields shared by several request structs into
// a types package is an ordinary refactor; if the gate could not follow it, the
// only remedies would be naming the embedded field — which nests it on the wire
// and breaks every client — or deleting the gate.
const GO_MOD_MODULE = /^module\s+(\S+)\s*$/m.exec(
  fs.readFileSync(path.join(repoRoot, 'go.mod'), 'utf8'),
)?.[1];
if (!GO_MOD_MODULE) {
  console.error('FAIL: go.mod has no module line; cannot resolve cross-package embeds.');
  process.exit(2);
}

// parseImports maps the qualifier a file uses to the imported package path.
// Blank (`_`) and dot imports cannot appear in a type qualifier, so they are
// dropped. The qualifier for an unaliased import is the last path element,
// which holds for every package in this module.
function parseImports(lines) {
  const out = new Map();
  const add = (alias, importPath) => {
    if (alias === '_' || alias === '.') return;
    out.set(alias || importPath.split('/').pop(), importPath);
  };
  let inBlock = false;
  for (const raw of lines) {
    const line = raw.trim();
    if (inBlock) {
      if (line.startsWith(')')) break;
      const entry = /^(?:([A-Za-z_][A-Za-z0-9_]*)\s+)?"([^"]+)"$/.exec(line);
      if (entry) add(entry[1], entry[2]);
      continue;
    }
    if (line === 'import (') { inBlock = true; continue; }
    const single = /^import\s+(?:([A-Za-z_.][A-Za-z0-9_]*)\s+)?"([^"]+)"$/.exec(line);
    if (single) { add(single[1], single[2]); continue; }
    // Imports precede every declaration in a valid Go file.
    if (/^(?:func|type|var|const)\b/.test(line)) break;
  }
  return out;
}

// Pass 1: index every named struct type by package directory, so an embedded
// field can be resolved, and record each file's import map for the qualified
// case.
const typeIndex = new Map();     // "<dir> <TypeName>" -> { members, rel, dir, problem }
const importsByFile = new Map(); // rel -> Map(qualifier -> import path)
const allFiles = goFiles(repoRoot);

for (const file of allFiles) {
  const rel = path.relative(repoRoot, file);
  const dir = path.dirname(rel);
  const lines = fs.readFileSync(file, 'utf8').split('\n');
  importsByFile.set(rel, parseImports(lines));
  for (let i = 0; i < lines.length; i += 1) {
    const decl = TYPE_DECL.exec(lines[i]);
    if (!decl) continue;
    const { members, closeIndex, problem } = parseStructBody(lines, i, decl[1]);
    typeIndex.set(`${dir} ${decl[1]}`, { members, rel, dir, problem });
    i = closeIndex;
  }
}

// embedTarget resolves an embedded type name — `Common` or `sharedtypes.Common`
// — to its entry in the type index, or to a reason it could not be resolved.
function embedTarget(embedded, dir, rel) {
  const dot = embedded.indexOf('.');
  if (dot < 0) {
    const key = `${dir} ${embedded}`;
    return typeIndex.has(key)
      ? { key, target: typeIndex.get(key) }
      : { reason: `${embedded} is not a struct type in ${dir}` };
  }
  const qualifier = embedded.slice(0, dot);
  const typeName = embedded.slice(dot + 1);
  const importPath = importsByFile.get(rel)?.get(qualifier);
  if (!importPath) {
    return { reason: `${rel} does not import a package qualified ${qualifier}` };
  }
  if (importPath !== GO_MOD_MODULE && !importPath.startsWith(`${GO_MOD_MODULE}/`)) {
    return { reason: `${importPath} is outside this module (${GO_MOD_MODULE})` };
  }
  const targetDir = importPath === GO_MOD_MODULE ? '.' : importPath.slice(GO_MOD_MODULE.length + 1);
  const key = `${targetDir} ${typeName}`;
  return typeIndex.has(key)
    ? { key, target: typeIndex.get(key) }
    : { reason: `${typeName} is not a struct type in ${targetDir}` };
}

// resolveFields flattens a member list into wire-name -> Go field name,
// following embedded types through the package index the way encoding/json
// promotes them. An embedded type that cannot be resolved is an error, not a
// silent omission: under-reporting the Go side would turn into a bogus
// "documented but not decoded" claim in the other direction.
function resolveFields(members, dir, rel, structName, seen = new Set()) {
  const out = new Map();
  for (const member of members) {
    if (member.kind === 'field') {
      out.set(member.wire, member.goName);
      continue;
    }
    const { key, target, reason } = embedTarget(member.type, dir, rel);
    if (!target) {
      errors.push(
        `${rel}:${member.line}: ${structName} embeds ${member.type}, whose fields the gate cannot ` +
        `read (${reason}). Any struct in a package of this module resolves, so moving the embedded ` +
        'type in-module fixes it; failing that, drop the openapi:request marker and record the ' +
        'schema in UNBOUND_REQUEST_SCHEMAS with a note. Do NOT give the embedded field a name to ' +
        'silence this — that nests it under a key on the wire and breaks every client.',
      );
      continue;
    }
    if (seen.has(key)) continue;
    seen.add(key);
    if (target.problem) errors.push(`${target.rel}:${target.problem}`);
    for (const [wire, goName] of resolveFields(target.members, target.dir, target.rel, member.type, seen)) {
      if (!out.has(wire)) out.set(wire, goName);
    }
  }
  return out;
}

// Pass 2: collect the markers and bind them.
const bindings = []; // { schema, goStruct, file, line, fields, waivers }

for (const file of allFiles) {
  const rel = path.relative(repoRoot, file);
  const dir = path.dirname(rel);
  const lines = fs.readFileSync(file, 'utf8').split('\n');
  let block = [];      // the doc-comment lines immediately above the current line
  let blockStart = 0;

  for (let i = 0; i < lines.length; i += 1) {
    const trimmed = lines[i].trim();
    if (trimmed.startsWith('//')) {
      if (block.length === 0) blockStart = i + 1;
      block.push({ text: trimmed, line: i + 1 });
      continue;
    }

    const binds = block.filter((c) => MARKER_BIND.test(c.text));
    const allows = block.filter((c) => MARKER_ALLOW.test(c.text));
    for (const bad of block) {
      if (!MARKER_ANY.test(bad.text) || MARKER_BIND.test(bad.text) || MARKER_ALLOW.test(bad.text)) continue;
      errors.push(
        `${rel}:${bad.line}: unparseable marker ${JSON.stringify(bad.text)}; expected ` +
        '"// openapi:request <SchemaName>" or "// openapi:request-allow <field> <reason>"',
      );
    }
    block = [];

    if (binds.length === 0 && allows.length === 0) continue;

    const decl = STRUCT_DECL.exec(trimmed);
    if (!decl) {
      errors.push(
        `${rel}:${blockStart}: openapi:request marker(s) are not attached to a struct declaration ` +
        '(the doc comment must sit immediately above a `type X struct {` or `var x struct {` line)',
      );
      continue;
    }
    if (binds.length === 0) {
      errors.push(`${rel}:${blockStart}: openapi:request-allow without an openapi:request binding on ${decl[1]}`);
      continue;
    }

    const goStruct = decl[1];
    const boundNames = new Set(binds.map((b) => MARKER_BIND.exec(b.text)[1]));
    const waivers = new Map(); // token ("field" or "Schema.field") -> { reason, line, schema, field }
    for (const allow of allows) {
      const [, token, reason] = MARKER_ALLOW.exec(allow.text);
      if (reason.trim().length < 8) {
        errors.push(`${rel}:${allow.line}: openapi:request-allow ${token} needs a real reason, not ${JSON.stringify(reason)}`);
      }
      if (waivers.has(token)) {
        errors.push(`${rel}:${allow.line}: duplicate openapi:request-allow for ${token}`);
      }
      // A token is schema-scoped only when the part before the first dot names a
      // schema this struct binds. That keeps a JSON field name containing a dot
      // from being misread as a scope, and turns a scope typo into an error
      // rather than a silently-bare waiver.
      let schema = null;
      let field = token;
      const dot = token.indexOf('.');
      if (dot > 0) {
        const prefix = token.slice(0, dot);
        if (boundNames.has(prefix)) {
          schema = prefix;
          field = token.slice(dot + 1);
        } else if (Object.prototype.hasOwnProperty.call(schemas, prefix)) {
          errors.push(
            `${rel}:${allow.line}: openapi:request-allow ${token} scopes to ${prefix}, ` +
            `which ${goStruct} does not bind (bound: ${[...boundNames].join(', ')})`,
          );
        }
      }
      waivers.set(token, { reason: reason.trim(), line: allow.line, schema, field });
    }

    const { members, closeIndex, problem } = parseStructBody(lines, i, goStruct);
    if (problem) errors.push(`${rel}:${problem}`);
    const fields = resolveFields(members, dir, rel, goStruct);

    for (const bind of binds) {
      const [, schema] = MARKER_BIND.exec(bind.text);
      bindings.push({ schema, goStruct, file: rel, line: bind.line, declLine: i + 1, fields, waivers });
    }

    i = closeIndex;
  }
}

// ---------------------------------------------------------------------------
// Compare
// ---------------------------------------------------------------------------

const boundSchemas = new Set();
// Keyed on the DECLARATION SITE, not the struct name: several handlers decode
// into an anonymous `var req struct {`, so two unrelated bindings in one file
// share the name `req` (internal/handler/cluster_snapshots.go has exactly two).
// Keying on the name merged their used-sets, and a stale waiver on one was
// silently held alive by a live waiver of the same token on the other. The three
// roleBindingRequest markers still share a set, which is required — they are one
// declaration.
const usedWaivers = new Map(); // "file:declLine" -> Set(waiver token)

// waiverFor returns the waiver token covering `wire` for this binding's schema:
// the schema-scoped one if present, else the bare one.
function waiverFor(binding, wire) {
  for (const [token, waiver] of binding.waivers) {
    if (waiver.field !== wire) continue;
    if (waiver.schema === null || waiver.schema === binding.schema) return token;
  }
  return null;
}

for (const binding of bindings) {
  if (!Object.prototype.hasOwnProperty.call(schemas, binding.schema)) {
    errors.push(
      `${binding.file}:${binding.line}: openapi:request ${binding.schema} — ` +
      'no such schema under components.schemas in docs/openapi.yaml',
    );
    continue;
  }
  boundSchemas.add(binding.schema);

  const documented = specFields(schemas[binding.schema]);
  if (documented === null) {
    placeholders.push({
      schema: binding.schema,
      goStruct: binding.goStruct,
      file: binding.file,
      line: binding.line,
      ops: [...(schemaUsage.get(binding.schema) ?? [])].sort(),
      onDebtList: PLACEHOLDER_REQUEST_SCHEMAS.has(binding.schema),
    });
    continue;
  }

  const key = `${binding.file}:${binding.declLine}`;
  if (!usedWaivers.has(key)) usedWaivers.set(key, new Set());
  const used = usedWaivers.get(key);

  const undocumented = [];
  for (const wire of binding.fields.keys()) {
    if (documented.has(wire)) continue;
    const token = waiverFor(binding, wire);
    if (token) { used.add(token); continue; }
    undocumented.push(wire);
  }
  const phantom = [];
  for (const wire of documented) {
    if (binding.fields.has(wire)) continue;
    const token = waiverFor(binding, wire);
    if (token) { used.add(token); continue; }
    phantom.push(wire);
  }

  if (undocumented.length > 0 || phantom.length > 0) {
    drift.push({
      schema: binding.schema,
      goStruct: binding.goStruct,
      file: binding.file,
      line: binding.line,
      ops: [...(schemaUsage.get(binding.schema) ?? [])].sort(),
      undocumented: undocumented.sort(),
      phantom: phantom.sort(),
    });
  }
}

const reportedStale = new Set();
for (const binding of bindings) {
  const key = `${binding.file}:${binding.declLine}`;
  const used = usedWaivers.get(key) ?? new Set();
  for (const [token, waiver] of binding.waivers) {
    if (used.has(token) || reportedStale.has(`${key}:${token}`)) continue;
    reportedStale.add(`${key}:${token}`);
    staleWaivers.push({
      file: binding.file,
      line: waiver.line,
      goStruct: binding.goStruct,
      field: token,
    });
  }
}

const unbound = [];
const staleDebt = [];
for (const name of schemaUsage.keys()) {
  if (boundSchemas.has(name)) {
    if (UNBOUND_REQUEST_SCHEMAS.has(name)) staleDebt.push(name);
    continue;
  }
  if (UNBOUND_REQUEST_SCHEMAS.has(name)) continue;
  unbound.push(name);
}
for (const name of UNBOUND_REQUEST_SCHEMAS) {
  if (!schemaUsage.has(name)) staleDebt.push(name);
}
// A marker on a schema that no requestBody $refs is the $ref-to-inline
// conversion seen from the Go side: the struct is still bound, the endpoint no
// longer is. Reported here because the unbound/placeholder ratchets only look at
// schemas the spec still uses, so this is the one direction they cannot see.
const boundButUnused = [...boundSchemas].filter((name) => !schemaUsage.has(name)).sort();

const newPlaceholders = placeholders.filter((p) => !p.onDebtList);
const placeholderNames = new Set(placeholders.map((p) => p.schema));
for (const name of PLACEHOLDER_REQUEST_SCHEMAS) {
  if (!placeholderNames.has(name)) staleDebt.push(name);
}

// ---------------------------------------------------------------------------
// Report
// ---------------------------------------------------------------------------

const totalWaivers = bindings.reduce((n, b) => n + b.waivers.size, 0);
const driftingFields = drift.reduce((n, d) => n + d.undocumented.length + d.phantom.length, 0);

console.log('OpenAPI request-schema field contract');
console.log('=====================================');
console.log(`named request schemas (requestBody $ref) : ${schemaUsage.size}`);
console.log(`  bound to a Go struct                   : ${boundSchemas.size}`);
console.log(`  unbound, on the debt list              : ${[...UNBOUND_REQUEST_SCHEMAS].filter((n) => schemaUsage.has(n)).length}`);
console.log(`  unbound, NOT on the debt list          : ${unbound.length}`);
console.log(`  bound but property-less (placeholder)  : ${placeholders.length}`);
console.log(`bindings (marker occurrences)            : ${bindings.length}`);
console.log(`  field-comparable                       : ${bindings.length - placeholders.length}`);
console.log(`per-field waivers                        : ${totalWaivers}`);
console.log(`schemas with field drift                 : ${drift.length}`);
console.log(`drifting fields                          : ${driftingFields}`);
console.log(`inline request bodies (unverifiable)     : ${inlineRequestBodies} (budget ${EXPECTED_INLINE_REQUEST_BODIES})`);
if (boundButUnused.length > 0) {
  console.log(`bound schemas no requestBody uses        : ${boundButUnused.length}`);
}
if (nonJSONRequestBodies > 0) {
  console.log(`non-JSON request bodies (out of scope)   : ${nonJSONRequestBodies}`);
}

if (drift.length > 0) {
  console.log('\nDRIFT — documented request schema does not match the Go struct that decodes it:');
  for (const d of drift.sort((a, b) => a.schema.localeCompare(b.schema))) {
    console.log(`  ${d.schema}  <->  ${d.goStruct} (${d.file}:${d.line})`);
    for (const op of d.ops) console.log(`      used by ${op}`);
    if (d.undocumented.length > 0) {
      console.log(`      undocumented API (in Go, not in spec): ${d.undocumented.join(', ')}`);
    }
    if (d.phantom.length > 0) {
      console.log(`      lie to clients (in spec, not in Go)  : ${d.phantom.join(', ')}`);
    }
  }
}

// The placeholder list is 12 entries of known debt; spelling it out on every
// green CI run is noise. Show it when a human is reading the report, or when an
// entry is NOT already written down (which also fails --check).
if (placeholders.length > 0 && (!check || verbose || newPlaceholders.length > 0)) {
  console.log('\nPLACEHOLDER SCHEMAS — documented as "any JSON object", so no field is verified.');
  console.log('Enumerate the properties from the bound Go struct, then drop the debt-list entry:');
  for (const p of placeholders.sort((a, b) => a.schema.localeCompare(b.schema))) {
    const tag = p.onDebtList ? 'debt' : 'NEW — not on the debt list';
    console.log(`  ${p.schema}  <->  ${p.goStruct} (${p.file}:${p.line})  [${tag}]`);
    for (const op of p.ops) console.log(`      used by ${op}`);
  }
}

if (staleWaivers.length > 0) {
  console.log('\nSTALE WAIVERS — openapi:request-allow no longer suppresses anything; delete the line:');
  for (const s of staleWaivers) console.log(`  ${s.file}:${s.line}  ${s.goStruct}.${s.field}`);
}

if (unbound.length > 0) {
  console.log('\nUNBOUND — documented request schema with no `// openapi:request` marker and not on the debt list:');
  for (const name of unbound.sort()) {
    console.log(`  ${name}  (${[...schemaUsage.get(name)].sort().join(', ')})`);
  }
}

if (boundButUnused.length > 0) {
  console.log('\nBOUND BUT UNUSED — a Go struct binds these, but no requestBody $refs them:');
  for (const name of boundButUnused) {
    const binding = bindings.find((b) => b.schema === name);
    console.log(`  ${name}  <->  ${binding.goStruct} (${binding.file}:${binding.line})`);
  }
}

if (staleDebt.length > 0) {
  console.log('\nSTALE DEBT LIST — remove these from the debt sets at the top of this script:');
  for (const name of [...new Set(staleDebt)].sort()) console.log(`  ${name}`);
}

if (errors.length > 0) {
  console.log('\nERRORS — the gate could not evaluate these:');
  for (const message of errors) console.log(`  ${message}`);
}

if (verbose) {
  console.log('\nBOUND PAIRS:');
  for (const b of bindings.slice().sort((x, y) => x.schema.localeCompare(y.schema))) {
    console.log(`  ${b.schema} <-> ${b.goStruct} (${b.file}:${b.line})  ${b.fields.size} field(s)`);
    for (const [field, waiver] of b.waivers) console.log(`      allow ${field}: ${waiver.reason}`);
  }
}

if (errors.length > 0) {
  console.error(`\nFAIL: ${errors.length} structural error(s) in the request-field contract.`);
  process.exit(2);
}

if (check && (drift.length > 0 || staleWaivers.length > 0 || unbound.length > 0
  || staleDebt.length > 0 || newPlaceholders.length > 0 || boundButUnused.length > 0
  || inlineRequestBodies !== EXPECTED_INLINE_REQUEST_BODIES)) {
  console.error('\nFAIL: request-schema field contract violated.');
  if (drift.length > 0) {
    console.error(`  ${driftingFields} field(s) drift across ${drift.length} schema(s) — fix docs/openapi.yaml or the Go struct,`);
    console.error('  then run `make openapi-embed`. If a field legitimately differs, add a per-field');
    console.error('  `// openapi:request-allow <field> <reason>` line to the struct doc comment.');
  }
  if (newPlaceholders.length > 0) {
    console.error(`  ${newPlaceholders.length} request schema(s) document no properties at all:`);
    for (const p of newPlaceholders) console.error(`    ${p.schema} — enumerate it from ${p.goStruct} (${p.file})`);
  }
  if (inlineRequestBodies > EXPECTED_INLINE_REQUEST_BODIES) {
    console.error(`  ${inlineRequestBodies - EXPECTED_INLINE_REQUEST_BODIES} new inline request body/bodies ` +
      `(${inlineRequestBodies} vs a budget of ${EXPECTED_INLINE_REQUEST_BODIES}). An inline schema cannot be`);
    console.error('  bound, so none of its fields is checked. Move it under components.schemas, add the');
    console.error('  `// openapi:request <Name>` marker, and leave EXPECTED_INLINE_REQUEST_BODIES alone.');
  }
  if (inlineRequestBodies < EXPECTED_INLINE_REQUEST_BODIES) {
    console.error(`  inline request bodies dropped to ${inlineRequestBodies} — lower ` +
      `EXPECTED_INLINE_REQUEST_BODIES to ${inlineRequestBodies} so the budget cannot be refilled.`);
  }
  if (boundButUnused.length > 0) {
    console.error(`  ${boundButUnused.length} bound schema(s) that no requestBody $refs: ${boundButUnused.join(', ')}.`);
    console.error('  Either the operation was switched to an inline schema — restore the $ref, or the');
    console.error('  endpoint is gone and the openapi:request marker should go with it.');
  }
  if (staleWaivers.length > 0) console.error(`  ${staleWaivers.length} stale waiver(s) — delete them.`);
  if (unbound.length > 0) console.error(`  ${unbound.length} unbound request schema(s) — add a marker, or add to UNBOUND_REQUEST_SCHEMAS.`);
  if (staleDebt.length > 0) console.error('  the debt list names schemas that no longer need it.');
  process.exit(1);
}

process.exit(0);
