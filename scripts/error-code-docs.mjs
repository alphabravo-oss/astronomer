#!/usr/bin/env node
// Generates docs/error-codes.md from the canonical apierror catalog.
//
// Source of truth: internal/handler/apierror/codes.go. Every error response
// produced by the Astronomer REST API carries a stable machine-readable `code`
// string, and that file enumerates the canonical constant per concept. This
// script parses the catalog and emits a human-readable table so the set of
// codes a client may observe is documentable.
//
// Modes:
//   (default)  write docs/error-codes.md
//   --write    same as default (explicit)
//   --check    regenerate in-memory and fail (exit 1) if the committed
//              docs/error-codes.md is stale. Used by CI / `make error-codes-check`.
//
// The literal wire value is always emitted verbatim from the source string —
// never re-derived from the Go identifier — because name and value are not
// always derivable from each other (e.g. InvalidClusterID -> "invalid_cluster",
// PersistError -> "persist_failed").

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(scriptDir, '..');
const args = new Set(process.argv.slice(2));

const sourcePath = path.join(repoRoot, 'internal/handler/apierror/codes.go');
const outputPath = path.join(repoRoot, 'docs/error-codes.md');

const GENERATOR_CMD = 'node scripts/error-code-docs.mjs --write';

function rel(file) {
  return path.relative(repoRoot, file).replaceAll(path.sep, '/');
}

function escapeCell(value) {
  return String(value ?? '')
    .replaceAll('|', '\\|')
    .replaceAll('\n', ' ')
    .trim();
}

function table(headers, rows) {
  const head = `| ${headers.join(' | ')} |`;
  const sep = `| ${headers.map(() => '---').join(' | ')} |`;
  const body = rows.map((row) => `| ${row.map(escapeCell).join(' | ')} |`).join('\n');
  return `${head}\n${sep}\n${body}`;
}

// Parse the `(typically HTTP NNN[ / NNN ...])` substring out of a section header.
function parseHttpStatus(header) {
  const match = /HTTP\s+([0-9]{3}(?:\s*\/\s*[0-9]{3})*)/i.exec(header);
  return match ? match[1].replace(/\s*\/\s*/g, ' / ') : '';
}

// Strip a `// --- <text> (typically HTTP NNN) ---` group header down to its name.
function parseGroupName(header) {
  return header
    .replace(/^---\s*/, '')
    .replace(/\s*---$/, '')
    .replace(/\s*\((?:typically\s+)?HTTP[^)]*\)\s*$/i, '')
    .trim();
}

function parseCatalog(source) {
  const lines = source.split(/\r?\n/);

  const constants = [];
  let group = null; // { name, http, provenance }
  let provenance = 'seed';
  let docLines = [];

  const constRe = /^([A-Za-z][A-Za-z0-9_]*)\s+(?:[A-Za-z][A-Za-z0-9_.\[\]*]*)\s*=\s*"([^"]+)"/;

  for (let i = 0; i < lines.length; i += 1) {
    const raw = lines[i];
    const trimmed = raw.trim();

    // Second-tier banner: `// ===...===` block marking the codemod expansion.
    // It is narrative, not a group; we use it only to flip provenance.
    if (/^\/\/\s*={3,}/.test(trimmed)) {
      // Scan the banner body for the provenance label.
      let j = i + 1;
      while (j < lines.length && /^\/\//.test(lines[j].trim()) && !/^\/\/\s*={3,}/.test(lines[j].trim())) {
        if (/codemod/i.test(lines[j])) provenance = 'codemod';
        j += 1;
      }
      docLines = [];
      continue;
    }

    // Group header: `// --- <name> (typically HTTP NNN) ---`
    const groupMatch = /^\/\/\s*(---\s*.*?---)\s*$/.exec(trimmed);
    if (groupMatch) {
      group = {
        name: parseGroupName(groupMatch[1]),
        http: parseHttpStatus(groupMatch[1]),
        provenance,
      };
      docLines = [];
      continue;
    }

    // Doc comment line (not a group header / banner).
    if (trimmed.startsWith('//')) {
      docLines.push(trimmed.replace(/^\/\/\s?/, ''));
      continue;
    }

    // Constant declaration.
    const constMatch = constRe.exec(trimmed);
    if (constMatch) {
      const name = constMatch[1];
      const value = constMatch[2];
      const doc = joinDoc(docLines, name);
      constants.push({
        name,
        value,
        group: group ? group.name : '',
        http: group ? group.http : '',
        provenance: group ? group.provenance : provenance,
        doc: doc.text,
        aliases: doc.aliases,
      });
      docLines = [];
      continue;
    }

    // Any other non-blank, non-comment line resets the pending doc comment so a
    // stray comment far from a constant does not leak onto it.
    if (trimmed !== '' && !trimmed.startsWith('//')) {
      docLines = [];
    }
  }

  return constants;
}

// Join a multi-line doc comment into prose. The first token is always the Go
// identifier; we validate it and strip it. Pull out any "Collapses legacy
// literal(s): ..." line as structured aliases.
function joinDoc(docLines, name) {
  const aliases = [];
  const proseLines = [];

  for (const line of docLines) {
    const aliasMatch = /Collapses legacy literal\(s\):\s*(.+?)\.?$/.exec(line);
    if (aliasMatch) {
      for (const m of aliasMatch[1].matchAll(/"([^"]+)"/g)) {
        aliases.push(m[1]);
      }
      continue;
    }
    proseLines.push(line);
  }

  let text = proseLines.join(' ').replace(/\s+/g, ' ').trim();
  // Strip the leading Go identifier (the godoc convention) when present.
  if (text.startsWith(`${name} `)) {
    text = text.slice(name.length + 1).trim();
  }
  return { text, aliases };
}

function renderMarkdown(constants) {
  // Group rows by their section header, preserving first-seen order.
  const groups = [];
  const byGroup = new Map();
  for (const c of constants) {
    const key = c.group || 'Uncategorized';
    if (!byGroup.has(key)) {
      byGroup.set(key, []);
      groups.push(key);
    }
    byGroup.get(key).push(c);
  }

  const sections = groups.map((name) => {
    const rows = byGroup.get(name);
    const http = rows.find((r) => r.http)?.http || '—';
    const provenance = rows[0]?.provenance || 'seed';
    const tableRows = rows.map((c) => [
      `\`${c.name}\``,
      `\`${c.value}\``,
      c.http || '—',
      [c.doc, c.aliases.length ? `Aliases: ${c.aliases.map((a) => `\`${a}\``).join(', ')}` : '']
        .filter(Boolean)
        .join(' '),
    ]);
    return `### ${name}\n\nDominant HTTP status: ${http} · Provenance: ${provenance}\n\n${table(
      ['Constant', 'Wire value', 'HTTP', 'Description'],
      tableRows,
    )}`;
  });

  const aliasRows = constants
    .filter((c) => c.aliases.length)
    .flatMap((c) => c.aliases.map((a) => [`\`${a}\``, `\`${c.value}\``, `\`${c.name}\``]));

  const aliasSection = aliasRows.length
    ? `## Legacy literal aliases

Near-duplicate wire spellings that were collapsed onto a single canonical code.
Clients that previously observed a legacy literal should branch on the canonical
wire value instead.

${table(['Legacy literal', 'Canonical wire value', 'Constant'], aliasRows)}
`
    : '';

  return `<!-- GENERATED FILE — DO NOT EDIT.
     Source: ${rel(sourcePath)}
     Regenerate: ${GENERATOR_CMD}
     CI checks freshness via: make error-codes-check -->

# API error codes

Every error response produced by the Astronomer REST API carries a stable,
machine-readable \`code\` string in its body:

\`\`\`json
{"error": {"code": "<code>", "message": "<message>", "request_id": "..."}}
\`\`\`

This document is generated from the canonical catalog in
[\`${rel(sourcePath)}\`](../${rel(sourcePath)}). Each row lists the Go constant,
the literal wire value clients actually observe (emitted verbatim from source —
never re-derived from the identifier), the HTTP status family the code typically
accompanies, and a short description. Codes are grouped by status family; a
handful of codes legitimately appear under more than one status depending on
context, so the grouping reflects the dominant usage, not an exhaustive contract.

**Total codes: ${constants.length}**

## Codes by category

${sections.join('\n\n')}

${aliasSection}`.replace(/\n{3,}/g, '\n\n').replace(/\s+$/, '') + '\n';
}

function main() {
  const source = fs.readFileSync(sourcePath, 'utf8');
  const constants = parseCatalog(source);

  if (constants.length === 0) {
    console.error(`error-code-docs: parsed 0 constants from ${rel(sourcePath)} — refusing to write.`);
    process.exit(1);
  }

  const markdown = renderMarkdown(constants);

  if (args.has('--check')) {
    const current = fs.existsSync(outputPath) ? fs.readFileSync(outputPath, 'utf8') : '';
    if (current !== markdown) {
      console.error(`${rel(outputPath)} is stale (parsed ${constants.length} codes).`);
      console.error(`Run: ${GENERATOR_CMD}`);
      process.exit(1);
    }
    console.error(`${rel(outputPath)} is up to date (${constants.length} codes).`);
    return;
  }

  fs.writeFileSync(outputPath, markdown);
  console.error(`Wrote ${rel(outputPath)} (${constants.length} codes).`);
}

main();
