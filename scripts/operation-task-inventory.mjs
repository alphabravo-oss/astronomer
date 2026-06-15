#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(scriptDir, '..');
const args = new Set(process.argv.slice(2));
const outputPath = path.join(repoRoot, 'docs/rancher-quality-phase0-operation-task-inventory.md');
const directEnqueueClassificationPath = path.join(repoRoot, 'docs/direct-enqueue-classifications.json');
const skipDirs = new Set(['.git', '.next', 'coverage', 'dist', 'node_modules', 'out', 'tmp', 'vendor']);
const validDirectEnqueueClassifications = new Set([
  'outbox-backed',
  'operation-backed',
  'repair-backed',
  'best-effort',
  'wrapper-only',
]);

function rel(file) {
  return path.relative(repoRoot, file).replaceAll(path.sep, '/');
}

function exists(file) {
  return fs.existsSync(file);
}

function read(file) {
  return fs.readFileSync(file, 'utf8');
}

function walk(dir, include) {
  if (!exists(dir)) return [];
  const out = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (skipDirs.has(entry.name)) continue;
    const file = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      out.push(...walk(file, include));
      continue;
    }
    if (!entry.isFile()) continue;
    if (!include || include(file)) out.push(file);
  }
  return out.sort();
}

function goFilesUnder(...segments) {
  return walk(path.join(repoRoot, ...segments), (file) =>
    path.extname(file) === '.go' && !rel(file).endsWith('_test.go'),
  );
}

function link(file, line) {
  return `[\`${file}:${line}\`](${file}:${line})`;
}

function escapeCell(value) {
  return String(value ?? '')
    .replaceAll('|', '\\|')
    .replaceAll('\n', '<br>');
}

function table(headers, rows) {
  if (rows.length === 0) return '- None.\n';
  const head = `| ${headers.map(escapeCell).join(' |')} |`;
  const sep = `| ${headers.map(() => '---').join(' |')} |`;
  const body = rows.map((row) => `| ${row.map(escapeCell).join(' |')} |`).join('\n');
  return `${head}\n${sep}\n${body}\n`;
}

function bulletList(items, render, limit = 80) {
  if (items.length === 0) return '- None.\n';
  const visible = items.slice(0, limit).map((item) => `- ${render(item)}`).join('\n');
  const remaining = items.length - limit;
  return `${visible}${remaining > 0 ? `\n- ... ${remaining} more` : ''}\n`;
}

function lineRows(files, matcher) {
  const rows = [];
  for (const file of files) {
    const lines = read(file).split(/\r?\n/);
    lines.forEach((line, index) => {
      const row = matcher(line, file, index, lines);
      if (row) rows.push(row);
    });
  }
  return rows;
}

function normalizeExpr(expr) {
  return (expr ?? '')
    .trim()
    .replace(/^&?/, '')
    .replace(/[)\]}]+$/, '')
    .trim();
}

function buildTaskConstants(files) {
  const constants = new Map();
  const aliases = [];

  for (const file of files) {
    const lines = read(file).split(/\r?\n/);
    lines.forEach((rawLine, index) => {
      const line = rawLine.replace(/\/\/.*$/, '').trim().replace(/^const\s+/, '');
      let match = /^([A-Za-z][A-Za-z0-9_]*)\s*(?:[A-Za-z][A-Za-z0-9_\.\[\]\*]*)?\s*=\s*"([^"]+:[^"]+)"/.exec(line);
      if (match) {
        const row = { name: match[1], value: match[2], file: rel(file), line: index + 1 };
        constants.set(match[1], row);
        constants.set(`tasks.${match[1]}`, row);
        constants.set(`worker.${match[1]}`, row);
        return;
      }
      match = /^([A-Za-z][A-Za-z0-9_]*)\s*(?:[A-Za-z][A-Za-z0-9_\.\[\]\*]*)?\s*=\s*([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?)/.exec(line);
      if (match) {
        aliases.push({ name: match[1], expr: match[2], file: rel(file), line: index + 1 });
      }
    });
  }

  let changed = true;
  while (changed) {
    changed = false;
    for (const alias of aliases) {
      if (constants.has(alias.name)) continue;
      const target = constants.get(alias.expr) ?? constants.get(alias.expr.replace(/^tasks\./, ''));
      if (!target) continue;
      const row = { name: alias.name, value: target.value, file: alias.file, line: alias.line };
      constants.set(alias.name, row);
      constants.set(`tasks.${alias.name}`, row);
      constants.set(`worker.${alias.name}`, row);
      changed = true;
    }
  }
  return { constants, aliases };
}

function resolveTaskExpr(constants, expr) {
  const normalized = normalizeExpr(expr);
  const literal = /^"([^"]+:[^"]+)"$/.exec(normalized);
  if (literal) return { value: literal[1], resolved: true, expr: normalized };
  const constant = constants.get(normalized) ?? constants.get(normalized.replace(/^tasks\./, ''));
  if (constant) return { value: constant.value, resolved: true, expr: normalized };
  if (/^(e|row|task)\./.test(normalized) || normalized.endsWith('.Type()')) {
    return { value: normalized, resolved: false, dynamic: true, expr: normalized };
  }
  return { value: normalized, resolved: false, expr: normalized };
}

function currentFunctionForLine(lines, index) {
  for (let i = index; i >= 0; i -= 1) {
    const match = /^func\s+(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\(/.exec(lines[i].trim());
    if (match) return match[1];
  }
  return '';
}

function registeredHandlers(workerFiles, constants) {
  return lineRows(workerFiles, (line, file, index, lines) => {
    const match = /mux\.HandleFunc\(([^,]+),\s*instrumentTask\([^,]+,\s*(?:tasks\.)?([A-Za-z0-9_]+)\)\)/.exec(line);
    if (!match) return null;
    const fn = currentFunctionForLine(lines, index);
    const type = resolveTaskExpr(constants, match[1]);
    return {
      taskType: type.value,
      resolved: type.resolved,
      expr: type.expr,
      handler: match[2],
      queue: fn === 'RegisterTunnelHandlers' ? 'tunnel' : 'worker',
      file: rel(file),
      line: index + 1,
    };
  }).sort((a, b) => a.taskType.localeCompare(b.taskType) || a.handler.localeCompare(b.handler));
}

function scheduledTasks(constants) {
  const schedulerFile = path.join(repoRoot, 'internal/worker/scheduler.go');
  const rows = [];
  const lines = read(schedulerFile).split(/\r?\n/);
  let pendingTask = null;
  lines.forEach((line, index) => {
    const literal = /\{\s*"([^"]+)"\s*,\s*([^,]+)\s*,\s*"([^"]+)"\s*\}/.exec(line);
    if (literal) {
      const type = resolveTaskExpr(constants, literal[2]);
      rows.push({
        taskType: type.value,
        resolved: type.resolved,
        expr: type.expr,
        cron: literal[1],
        queue: 'default',
        description: literal[3],
        file: rel(schedulerFile),
        line: index + 1,
      });
      return;
    }

    const newTask = /task\s*:=\s*asynq\.NewTask\(([^,]+),/.exec(line);
    if (newTask) {
      pendingTask = resolveTaskExpr(constants, newTask[1]);
      return;
    }
    const tunnel = /scheduler\.Register\("([^"]+)",\s*task,\s*asynq\.Queue\(([^)]+)\)\)/.exec(line);
    if (tunnel && pendingTask) {
      rows.push({
        taskType: pendingTask.value,
        resolved: pendingTask.resolved,
        expr: pendingTask.expr,
        cron: tunnel[1],
        queue: normalizeExpr(tunnel[2]).replace(/^tasks\./, ''),
        description: 'tunnel-routed periodic task',
        file: rel(schedulerFile),
        line: index + 1,
      });
      pendingTask = null;
    }
  });
  return rows.sort((a, b) => a.taskType.localeCompare(b.taskType) || a.cron.localeCompare(b.cron));
}

function taskConstructors(taskFiles, constants) {
  const rows = [];
  for (const file of taskFiles) {
    const lines = read(file).split(/\r?\n/);
    let current = null;
    let body = [];
    let braceDepth = 0;

    lines.forEach((line, index) => {
      const fn = /^func\s+(New[A-Za-z0-9]+Task)\s*\(/.exec(line.trim());
      if (fn) {
        current = { name: fn[1], file: rel(file), line: index + 1 };
        body = [line];
        braceDepth = (line.match(/{/g) ?? []).length - (line.match(/}/g) ?? []).length;
        return;
      }
      if (!current) return;
      body.push(line);
      braceDepth += (line.match(/{/g) ?? []).length - (line.match(/}/g) ?? []).length;
      if (braceDepth > 0) return;

      const text = body.join('\n');
      const newTask = /asynq\.NewTask\(([^,\n]+),/.exec(text);
      const type = newTask ? resolveTaskExpr(constants, newTask[1]) : { value: '', resolved: false, expr: '' };
      rows.push({
        ...current,
        taskType: type.value,
        resolved: type.resolved,
        expr: type.expr,
        maxRetry: [...text.matchAll(/asynq\.MaxRetry\(([^)]+)\)/g)].map((m) => m[1]).join(', ') || '',
        timeout: [...text.matchAll(/asynq\.Timeout\(([^)]+)\)/g)].map((m) => m[1]).join(', ') || '',
        unique: [...text.matchAll(/asynq\.Unique\(([^)]+)\)/g)].map((m) => m[1]).join(', ') || '',
        queue: [...text.matchAll(/asynq\.Queue\(([^)]+)\)/g)].map((m) => normalizeExpr(m[1])).join(', ') || '',
      });
      current = null;
      body = [];
    });
  }
  return rows.sort((a, b) => a.taskType.localeCompare(b.taskType) || a.name.localeCompare(b.name));
}

function taskCreationCallsites(files, constants) {
  return lineRows(files, (line, file, index, lines) => {
    const match = /asynq\.NewTask\(([^,]+),/.exec(line);
    if (!match) return null;
    const fn = currentFunctionForLine(lines, index);
    const type = resolveTaskExpr(constants, match[1]);
    return {
      taskType: type.value,
      resolved: type.resolved,
      dynamic: type.dynamic,
      expr: type.expr,
      functionName: fn,
      file: rel(file),
      line: index + 1,
      options: [
        ...line.matchAll(/asynq\.(MaxRetry|Timeout|Unique|Queue)\(([^)]+)\)/g),
      ].map((m) => `${m[1]}(${m[2]})`).join(', '),
      constructor: /^New[A-Za-z0-9]+Task$/.test(fn),
    };
  }).sort((a, b) => a.taskType.localeCompare(b.taskType) || a.file.localeCompare(b.file) || a.line - b.line);
}

function enqueueCallsites(files, constants) {
  return lineRows(files, (line, file, index, lines) => {
    if (!/\.Enqueue\(/.test(line)) return null;
    const start = Math.max(0, index - 14);
    const end = Math.min(lines.length, index + 8);
    const window = lines.slice(start, end).join('\n');
    const taskMatch =
      /New([A-Za-z0-9]+Task)\(/.exec(window) ??
      /asynq\.NewTask\(([^,]+),/.exec(window);
    let task = '';
    let resolved = false;
    if (taskMatch) {
      if (taskMatch[0].startsWith('New')) {
        task = taskMatch[0].replace(/\($/, '');
      } else {
        const type = resolveTaskExpr(constants, taskMatch[1]);
        task = type.value;
        resolved = type.resolved;
      }
    }
    const outboxBacked = /EnqueueTaskOutbox|WithTaskOutbox|UpsertTaskOutbox|TaskOutboxOptions/.test(window);
    return {
      file: rel(file),
      line: index + 1,
      functionName: currentFunctionForLine(lines, index),
      task,
      resolved,
      outboxBacked,
      dispatcher: rel(file).endsWith('internal/worker/tasks/task_outbox_dispatch.go'),
      detail: line.trim(),
    };
  }).sort((a, b) => a.file.localeCompare(b.file) || a.line - b.line);
}

function taskOutboxProducers(files) {
  return lineRows(files, (line, file, index) => {
    if (!/EnqueueTaskOutbox|WithTaskOutbox|UpsertTaskOutbox/.test(line)) return null;
    return { file: rel(file), line: index + 1, detail: line.trim() };
  }).sort((a, b) => a.file.localeCompare(b.file) || a.line - b.line);
}

function operationTables() {
  const files = walk(path.join(repoRoot, 'internal/db/migrations'), (file) =>
    rel(file).endsWith('.up.sql'),
  );
  const rows = [];
  for (const file of files) {
    const lines = read(file).split(/\r?\n/);
    lines.forEach((line, index) => {
      const match = /CREATE TABLE(?: IF NOT EXISTS)?\s+([a-z0-9_]*operations)\b/i.exec(line);
      if (!match) return;
      if (match[1].endsWith('_operation_events')) return;
      rows.push({ table: match[1], file: rel(file), line: index + 1 });
    });
  }
  return rows.sort((a, b) => a.table.localeCompare(b.table));
}

function operationCreateHelpers() {
  const sqlcFiles = walk(path.join(repoRoot, 'internal/db/sqlc'), (file) =>
    path.extname(file) === '.go' && !rel(file).endsWith('_test.go'),
  );
  const helpers = lineRows(sqlcFiles, (line, file, index) => {
    const match = /func \(q \*Queries\) (Create[A-Za-z0-9]+Operation(?:Idempotent)?)\(/.exec(line);
    if (!match) return null;
    if (match[1].includes('OperationEvent')) return null;
    return {
      name: match[1],
      operation: match[1].replace(/^Create/, '').replace(/Idempotent$/, ''),
      idempotent: match[1].endsWith('Idempotent'),
      file: rel(file),
      line: index + 1,
    };
  });
  return helpers.sort((a, b) => a.operation.localeCompare(b.operation) || a.name.localeCompare(b.name));
}

function handlerOperationUsage(files) {
  return lineRows(files, (line, file, index) => {
    const match = /\b(Create[A-Za-z0-9]+Operation(?:Idempotent)?)\s*\(/.exec(line);
    if (!match) return null;
    if (match[1].includes('OperationEvent')) return null;
    if (/interface\s*\{|type\s+[A-Za-z0-9_]+\s+interface/.test(line)) return null;
    return {
      name: match[1],
      operation: match[1].replace(/^Create/, '').replace(/Idempotent$/, ''),
      idempotent: match[1].endsWith('Idempotent'),
      file: rel(file),
      line: index + 1,
    };
  }).sort((a, b) => a.operation.localeCompare(b.operation) || a.file.localeCompare(b.file) || a.line - b.line);
}

function idempotencyScopes(files) {
  return lineRows(files, (line, file, index) => {
    const match = /withOperationIdempotency\([^,]+,\s*"([^"]+)"\)/.exec(line);
    if (!match) return null;
    return { scope: match[1], file: rel(file), line: index + 1 };
  }).sort((a, b) => a.scope.localeCompare(b.scope) || a.file.localeCompare(b.file) || a.line - b.line);
}

function groupBy(items, keyFn) {
  const out = new Map();
  for (const item of items) {
    const key = keyFn(item);
    const rows = out.get(key) ?? [];
    rows.push(item);
    out.set(key, rows);
  }
  return out;
}

function loadDirectEnqueueClassifications() {
  if (!exists(directEnqueueClassificationPath)) {
    return { rows: [], errors: [] };
  }
  let parsed;
  try {
    parsed = JSON.parse(read(directEnqueueClassificationPath));
  } catch (error) {
    return { rows: [], errors: [`docs/direct-enqueue-classifications.json is not valid JSON: ${error.message}`] };
  }
  const rows = Array.isArray(parsed.classifications) ? parsed.classifications : [];
  const errors = [];
  rows.forEach((row, index) => {
    const label = `classifications[${index}]`;
    if (!row.file || !row.function) {
      errors.push(`${label} must include file and function`);
    }
    if (!validDirectEnqueueClassifications.has(row.classification)) {
      errors.push(`${label} has invalid classification "${row.classification}"`);
    }
    if (!row.owner || !row.reason || !row.validation) {
      errors.push(`${label} must include owner, reason, and validation`);
    }
  });
  return { rows, errors };
}

function directEnqueueClassificationKey(row) {
  return [
    row.file,
    row.functionName || row.function || '',
    row.task || '',
  ].join('#');
}

function applyDirectEnqueueClassifications(rows, classifications) {
  const byKey = new Map();
  for (const item of classifications) {
    byKey.set([
      item.file,
      item.function || '',
      item.task || '',
    ].join('#'), item);
  }
  const matched = new Set();
  const classified = rows.map((row) => {
    const exact = byKey.get(directEnqueueClassificationKey(row));
    const withoutTask = byKey.get([row.file, row.functionName || '', ''].join('#'));
    const classification = exact ?? withoutTask ?? null;
    if (classification) {
      matched.add(classification);
    }
    return {
      ...row,
      classification,
    };
  });
  return {
    classified,
    stale: classifications.filter((row) => !matched.has(row)),
  };
}

function buildInventory() {
  const workerFiles = goFilesUnder('internal/worker');
  const taskFiles = goFilesUnder('internal/worker/tasks');
  const handlerFiles = goFilesUnder('internal/handler');
  const sourceFiles = [...workerFiles, ...handlerFiles, ...walk(path.join(repoRoot, 'cmd'), (file) =>
    path.extname(file) === '.go' && !rel(file).endsWith('_test.go'),
  )].sort();
  const { constants } = buildTaskConstants(sourceFiles);
  const handlers = registeredHandlers(workerFiles, constants);
  const schedules = scheduledTasks(constants);
  const constructors = taskConstructors(taskFiles, constants);
  const creations = taskCreationCallsites(sourceFiles, constants);
  const enqueues = enqueueCallsites(sourceFiles, constants);
  const outbox = taskOutboxProducers(sourceFiles);
  const opTables = operationTables();
  const opHelpers = operationCreateHelpers();
  const opUsage = handlerOperationUsage(handlerFiles);
  const scopes = idempotencyScopes(handlerFiles);
  const classificationConfig = loadDirectEnqueueClassifications();

  const registeredTypes = new Set(handlers.filter((row) => row.resolved).map((row) => row.taskType));
  const createdTypes = new Set(creations.filter((row) => row.resolved).map((row) => row.taskType));
  const scheduledTypes = new Set(schedules.filter((row) => row.resolved).map((row) => row.taskType));
  const unregistered = [...new Set([...createdTypes, ...scheduledTypes])]
    .filter((taskType) => !registeredTypes.has(taskType))
    .sort();

  const unresolvedTaskExpressions = [
    ...handlers.filter((row) => !row.resolved),
    ...schedules.filter((row) => !row.resolved),
    ...constructors.filter((row) => !row.resolved && row.taskType),
    ...creations.filter((row) => !row.resolved && !row.dynamic),
  ].sort((a, b) => a.file.localeCompare(b.file) || a.line - b.line);

  const operations = new Map();
  for (const helper of opHelpers) {
    const row = operations.get(helper.operation) ?? {
      operation: helper.operation,
      create: null,
      idempotent: null,
      handlerCreates: [],
      idempotentUsages: [],
    };
    if (helper.idempotent) row.idempotent = helper;
    else row.create = helper;
    operations.set(helper.operation, row);
  }
  for (const usage of opUsage) {
    const row = operations.get(usage.operation) ?? {
      operation: usage.operation,
      create: null,
      idempotent: null,
      handlerCreates: [],
      idempotentUsages: [],
    };
    if (usage.idempotent) row.idempotentUsages.push(usage);
    else row.handlerCreates.push(usage);
    operations.set(usage.operation, row);
  }

  const handlerByType = groupBy(handlers, (row) => row.taskType);
  const scheduleByType = groupBy(schedules, (row) => row.taskType);
  const constructorByType = groupBy(constructors, (row) => row.taskType);
  const creationByType = groupBy(creations.filter((row) => row.resolved), (row) => row.taskType);
  const allTaskTypes = [...new Set([
    ...handlers.map((row) => row.taskType),
    ...schedules.map((row) => row.taskType),
    ...constructors.map((row) => row.taskType).filter(Boolean),
    ...creations.filter((row) => row.resolved).map((row) => row.taskType),
  ])].sort();

  const unbackedEnqueues = enqueues.filter((row) => !row.outboxBacked && !row.dispatcher);
  const classifiedDirectEnqueues = applyDirectEnqueueClassifications(unbackedEnqueues, classificationConfig.rows);

  return {
    sourceFileCount: sourceFiles.length,
    handlerFileCount: handlerFiles.length,
    workerFileCount: workerFiles.length,
    taskConstants: [...constants.values()].filter((row, index, rows) =>
      rows.findIndex((candidate) => candidate.name === row.name && candidate.value === row.value) === index,
    ).sort((a, b) => a.value.localeCompare(b.value)),
    handlers,
    schedules,
    constructors,
    creations,
    enqueues,
    outbox,
    operationTables: opTables,
    operationHelpers: [...operations.values()].sort((a, b) => a.operation.localeCompare(b.operation)),
    operationUsage: opUsage,
    idempotencyScopes: scopes,
    unregisteredTaskTypes: unregistered,
    unresolvedTaskExpressions,
    directEnqueueClassificationErrors: classificationConfig.errors,
    staleDirectEnqueueClassifications: classifiedDirectEnqueues.stale,
    unbackedEnqueues: classifiedDirectEnqueues.classified,
    unclassifiedEnqueues: classifiedDirectEnqueues.classified.filter((row) => !row.classification),
    taskRows: allTaskTypes.map((taskType) => ({
      taskType,
      handlers: handlerByType.get(taskType) ?? [],
      schedules: scheduleByType.get(taskType) ?? [],
      constructors: constructorByType.get(taskType) ?? [],
      creations: creationByType.get(taskType) ?? [],
    })),
  };
}

function renderMarkdown(inventory) {
  const operationRows = inventory.operationHelpers.map((row) => [
    row.operation,
    row.create ? link(row.create.file, row.create.line) : 'missing',
    row.idempotent ? link(row.idempotent.file, row.idempotent.line) : 'missing',
    row.handlerCreates.length,
    row.idempotentUsages.length,
  ]);

  const taskRows = inventory.taskRows.map((row) => [
    `\`${row.taskType}\``,
    row.handlers.map((handler) => `${handler.handler} (${handler.queue}) ${link(handler.file, handler.line)}`).join('<br>') || 'missing',
    row.schedules.map((schedule) => `${schedule.cron} / ${schedule.queue} ${link(schedule.file, schedule.line)}`).join('<br>') || 'none',
    row.constructors.map((ctor) => `${ctor.name} ${link(ctor.file, ctor.line)}`).join('<br>') || 'none',
    row.creations.length,
  ]);

  const scopeRows = [...groupBy(inventory.idempotencyScopes, (row) => row.scope).entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([scope, rows]) => [
      `\`${scope}\``,
      rows.length,
      rows.slice(0, 4).map((row) => link(row.file, row.line)).join('<br>'),
    ]);
  const directEnqueueClassificationRows = inventory.unbackedEnqueues.map((row) => [
    link(row.file, row.line),
    row.functionName || '',
    row.task || 'unknown',
    row.classification?.classification || 'unclassified',
    row.classification?.owner || '',
    row.classification?.reason || '',
    row.classification?.validation || '',
  ]);

  return `# Rancher-Quality Phase 0 Operation And Task Inventory

Status: Active
Generated by: \`node scripts/operation-task-inventory.mjs --write\`
CI gate: \`npm run code-health\` from \`frontend/\`

This inventory supports the Phase 0 durability work: every high-risk background action should have a visible task type, handler, schedule or producer, retry policy, idempotency story, and durable operation/outbox state where Redis delivery loss would be user-visible.

## Scan Scope

- Worker Go files scanned: ${inventory.workerFileCount}
- Handler Go files scanned: ${inventory.handlerFileCount}
- Production source files scanned: ${inventory.sourceFileCount}
- Task constants resolved: ${inventory.taskConstants.length}
- Worker handler registrations: ${inventory.handlers.length}
- Periodic schedules: ${inventory.schedules.length}
- Task constructors: ${inventory.constructors.length}
- Production \`asynq.NewTask\` call sites: ${inventory.creations.length}
- Production \`.Enqueue(...)\` call sites: ${inventory.enqueues.length}
- Task-outbox producer call sites: ${inventory.outbox.length}
- Durable operation tables: ${inventory.operationTables.length}

The hard CI behavior keeps this inventory current, requires registered/scheduled task expressions to remain parseable, and fails new direct Redis enqueue call sites until they are classified in \`docs/direct-enqueue-classifications.json\`.

## Immediate Findings

### Task Types Created Or Scheduled Without A Registered Handler

${bulletList(inventory.unregisteredTaskTypes, (taskType) => `\`${taskType}\``)}
### Unresolved Task Expressions

${bulletList(inventory.unresolvedTaskExpressions, (row) => `${link(row.file, row.line)} - \`${row.expr}\``)}
### Unclassified Direct Enqueue Call Sites

User-visible state changes should either use \`task_outbox\`, a durable operation table, or a documented periodic repair path. Any new direct enqueue row must be classified as \`outbox-backed\`, \`operation-backed\`, \`repair-backed\`, \`best-effort\`, or \`wrapper-only\`.

${bulletList(inventory.unclassifiedEnqueues, (row) =>
  `${link(row.file, row.line)} - ${row.task ? `near \`${row.task}\`; ` : ''}${row.detail}`,
)}
### Direct Enqueue Classifications

${table(['Location', 'Function', 'Nearby task', 'Classification', 'Owner', 'Reason', 'Validation'], directEnqueueClassificationRows)}
## Worker Task Registry

${table(['Task type', 'Handler', 'Schedule', 'Constructor', 'NewTask call sites'], taskRows)}
## Enqueue Points

${table(['Location', 'Function', 'Nearby task', 'Outbox observed', 'Detail'], inventory.enqueues.map((row) => [
  link(row.file, row.line),
  row.functionName || '',
  row.task || 'unknown',
  row.outboxBacked ? 'yes' : row.dispatcher ? 'dispatcher' : 'no',
  row.detail,
]))}
## Task Outbox Producers

${bulletList(inventory.outbox, (row) => `${link(row.file, row.line)} - \`${row.detail}\``)}
## Durable Operation Tables

${bulletList(inventory.operationTables, (row) => `\`${row.table}\` at ${link(row.file, row.line)}`)}
## Operation Idempotency Coverage

${table(['Operation helper', 'Create helper', 'Idempotent helper', 'Handler create uses', 'Handler idempotent uses'], operationRows)}
## Idempotency Scopes Used By Handlers

${table(['Scope', 'Call sites', 'Examples'], scopeRows)}
## Definition Of Done For Durability Review

- Every registered task type has an owning handler, an enqueue or schedule source, and a documented queue choice.
- Every task triggered by a committed product state change either writes \`task_outbox\` in the same transaction, records durable operation state first, or has an explicit periodic repair path.
- Every client-retryable operation create path accepts \`Idempotency-Key\` and uses an atomic idempotent SQL helper.
- Every direct enqueue row above is classified as outbox-backed, operation-backed, repair-backed, intentionally best-effort, or wrapper-only.
- Missing task handlers are fixed before enabling the producing feature in production.
`;
}

const inventory = buildInventory();
const markdown = renderMarkdown(inventory);

if (args.has('--write')) {
  fs.writeFileSync(outputPath, markdown);
}

if (args.has('--verify-doc')) {
  const current = exists(outputPath) ? read(outputPath) : '';
  if (current !== markdown) {
    console.error('docs/rancher-quality-phase0-operation-task-inventory.md is stale.');
    console.error('Run: node scripts/operation-task-inventory.mjs --write');
    process.exitCode = 1;
  }
}

if (args.has('--check')) {
  const structuralFailures = inventory.unresolvedTaskExpressions.filter((row) =>
    row.file === 'internal/worker/worker.go' || row.file === 'internal/worker/scheduler.go',
  );
  const classificationFailures = [
    ...inventory.directEnqueueClassificationErrors,
    ...inventory.unclassifiedEnqueues.map((row) => `unclassified direct enqueue: ${row.file}:${row.line} ${row.functionName || ''} ${row.task || ''}`),
    ...inventory.staleDirectEnqueueClassifications.map((row) => `stale direct enqueue classification: ${row.file} ${row.function || ''} ${row.task || ''}`),
  ];
  if (structuralFailures.length > 0) {
    console.error('Operation/task inventory could not resolve registered or scheduled task expressions:');
    for (const row of structuralFailures) {
      console.error(`- ${row.file}:${row.line} ${row.expr}`);
    }
    process.exitCode = 1;
  }
  if (classificationFailures.length > 0) {
    console.error('Operation/task inventory direct enqueue classification failures:');
    for (const failure of classificationFailures) {
      console.error(`- ${failure}`);
    }
    process.exitCode = 1;
  }
}

if (!args.has('--write') && !args.has('--verify-doc')) {
  process.stdout.write(markdown);
}
