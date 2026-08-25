#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const requireFromFrontend = createRequire(path.join(repoRoot, 'frontend/package.json'));
const yaml = requireFromFrontend('js-yaml');
const spec = yaml.load(fs.readFileSync(path.join(repoRoot, 'docs/openapi.yaml'), 'utf8'));
const methods = new Set(['get', 'put', 'post', 'delete', 'patch', 'head', 'options', 'trace']);
const routeClasses = new Set(['public-api', 'internal', 'diagnostic', 'proxy', 'stream', 'download', 'scim', 'compatibility-alias']);
const ids = new Map();
const failures = [];
let operationCount = 0;
let actionable202Count = 0;
const EXPECTED_ACTIONABLE_202_COUNT = 104;
const EXPECTED_202_EXCEPTION_COUNT = 5;
const async202Exceptions = new Map([
  ['POST /api/v1/gitops/sources/{id}/webhook', {
    operationId: 'postGitopsSourcesByIdWebhook',
    reason: 'trusted_webhook_ingest',
  }],
  ['POST /api/v1/clusters/{cluster_id}/apiserver-audit/', {
    operationId: 'postClustersByClusterIdApiserverAudit',
    reason: 'append_only_agent_audit_ingest',
  }],
  ['POST /api/v1/auth/password-reset/request/', {
    operationId: 'postAuthPasswordResetRequest',
    reason: 'privacy_preserving_acknowledgement',
  }],
  ['POST /api/v1/charlie/threads/messages/', {
    operationId: 'createCharlieThreadMessage',
    reason: 'interactive_message_acceptance',
  }],
  ['POST /api/v1/charlie/sessions/{session_id}/messages/', {
    operationId: 'createCharlieSessionMessage',
    reason: 'interactive_message_acceptance',
  }],
]);
const seenAsync202Exceptions = new Set();
const exactStatusReads = new Map([
  ['createControlPlaneSnapshot', '/api/v1/clusters/{cluster_id}/control-plane-snapshots/{id}/'],
  ['postClustersByClusterIdSnapshots', '/api/v1/clusters/{cluster_id}/snapshots/{id}'],
]);

function resolve(ref) {
  return ref.slice(2).split('/').map((part) => part.replace(/~1/g, '/').replace(/~0/g, '~'))
    .reduce((node, part) => node?.[part], spec);
}
function resolved(value) {
  return value?.$ref ? resolved(resolve(value.$ref)) : value;
}
function fail(method, routePath, message) {
  failures.push(`${method.toUpperCase()} ${routePath}: ${message}`);
}

for (const [routePath, pathItemRaw] of Object.entries(spec.paths ?? {})) {
  if (!routePath.startsWith('/') && !routePath.startsWith('x-')) failures.push(`Invalid path key ${routePath}`);
  const pathItem = resolved(pathItemRaw) ?? {};
  for (const [method, operationRaw] of Object.entries(pathItem)) {
    if (!methods.has(method.toLowerCase())) continue;
    operationCount += 1;
    const operation = resolved(operationRaw) ?? {};
    if (!operation.operationId) fail(method, routePath, 'missing operationId');
    else {
      const prior = ids.get(operation.operationId);
      if (prior) fail(method, routePath, `duplicate operationId ${operation.operationId} (also ${prior})`);
      ids.set(operation.operationId, `${method.toUpperCase()} ${routePath}`);
    }

    const routeClass = operation['x-astronomer-route-class'];
    if (!routeClasses.has(routeClass)) fail(method, routePath, 'missing or invalid x-astronomer-route-class');
    if (routeClass === 'compatibility-alias') {
      if (operation.deprecated !== true) fail(method, routePath, 'compatibility alias is missing deprecated: true');
      const sunset = operation['x-astronomer-sunset'];
      if (typeof sunset !== 'string' || !/^\d{4}-\d{2}-\d{2}$/.test(sunset) || Number.isNaN(Date.parse(`${sunset}T00:00:00Z`))) {
        fail(method, routePath, 'compatibility alias has no valid x-astronomer-sunset date');
      }
      const successor = operation['x-astronomer-successor'];
      if (typeof successor !== 'string' || !successor.startsWith('/')) {
        fail(method, routePath, 'compatibility alias has no absolute-path x-astronomer-successor');
      } else {
        const normalized = successor.replace(/\{[^}]+\}/g, '{}').replace(/\/+$/, '');
        const exists = Object.keys(spec.paths ?? {}).some((candidate) => candidate.replace(/\{[^}]+\}/g, '{}').replace(/\/+$/, '') === normalized);
        if (!exists) fail(method, routePath, `compatibility alias successor ${successor} is not documented`);
      }
    }
    if (!operation['x-rbac'] && !operation['x-astronomer-rbac']) fail(method, routePath, 'missing machine-readable RBAC metadata');
    if (!operation['x-astronomer-auth']) fail(method, routePath, 'missing machine-readable auth metadata');
    const requestSchemaStatus = operation['x-astronomer-request-schema-status'];
    if (requestSchemaStatus !== undefined && !['provisional', 'passthrough', 'polymorphic'].includes(requestSchemaStatus)) {
      fail(method, routePath, 'invalid x-astronomer-request-schema-status');
    }
    if (requestSchemaStatus === 'passthrough' && routeClass !== 'proxy') {
      fail(method, routePath, 'passthrough request schema status is only valid on proxy routes');
    }
    if (requestSchemaStatus === 'polymorphic' && routeClass === 'proxy') {
      fail(method, routePath, 'polymorphic request schema status is not valid on a proxy route');
    }

    const rawParameters = [
      ...(Array.isArray(pathItem.parameters) ? pathItem.parameters : []),
      ...(Array.isArray(operation.parameters) ? operation.parameters : []),
    ];
    const parameters = rawParameters.map(resolved).filter(Boolean);
    const idempotencyParameters = parameters.filter((parameter) =>
      parameter.in === 'header' && parameter.name === 'Idempotency-Key');
    const requiresDurableIdempotency = idempotencyParameters.some((parameter) => parameter.required === true);
    const response202 = operation.responses?.['202'];
    const exceptionKey = `${method.toUpperCase()} ${routePath}`;
    const expectedException = async202Exceptions.get(exceptionKey);
    const exceptionMetadata = operation['x-astronomer-async-202-exception'];
    if (expectedException) {
      seenAsync202Exceptions.add(exceptionKey);
      if (!response202) fail(method, routePath, 'registered async-202 exception no longer returns 202');
      if (operation.operationId !== expectedException.operationId) {
        fail(method, routePath, `async-202 exception operationId must remain ${expectedException.operationId}`);
      }
      if (!exceptionMetadata || typeof exceptionMetadata !== 'object' || Array.isArray(exceptionMetadata)) {
        fail(method, routePath, 'registered async-202 exception must declare object metadata');
      } else {
        if (exceptionMetadata.reason !== expectedException.reason) {
          fail(method, routePath, `async-202 exception reason must remain ${expectedException.reason}`);
        }
        const metadataKeys = Object.keys(exceptionMetadata);
        if (metadataKeys.length !== 1 || metadataKeys[0] !== 'reason') {
          fail(method, routePath, 'async-202 exception metadata may contain only the validated reason');
        }
      }
    } else if (exceptionMetadata !== undefined) {
      fail(method, routePath, 'unregistered async-202 exception metadata is forbidden');
    }
    if (response202 && !expectedException) {
      actionable202Count += 1;
      if (!['post', 'put', 'patch', 'delete'].includes(method.toLowerCase())) {
        fail(method, routePath, 'actionable 202 contract is only valid on mutation methods');
      }
      if (idempotencyParameters.length !== 1 || !requiresDurableIdempotency) {
        fail(method, routePath, 'actionable 202 mutation must require exactly one Idempotency-Key');
      } else {
        const schema = resolved(idempotencyParameters[0].schema) ?? {};
        if (schema.type !== 'string' || !Number.isInteger(schema.minLength) || schema.minLength < 1 ||
          !Number.isInteger(schema.maxLength) || schema.maxLength < schema.minLength || schema.maxLength > 128) {
          fail(method, routePath, 'required Idempotency-Key must be a bounded string with minLength >= 1 and maxLength <= 128');
        }
      }
      const accepted = resolved(response202) ?? {};
      const typed = Object.values(accepted.content ?? {}).some((media) => media?.schema);
      if (!typed) fail(method, routePath, 'actionable 202 mutation must return a typed receipt');
      const headers = accepted.headers ?? {};
      if (!headers.Location || !headers['Retry-After']) {
        fail(method, routePath, 'actionable 202 mutation must document Location and Retry-After headers');
      }
    }
    if (!response202 && requiresDurableIdempotency) {
      fail(method, routePath, 'required durable Idempotency-Key is only valid with a typed 202 receipt');
    }
    const expectedStatusPath = exactStatusReads.get(operation.operationId);
    if (expectedStatusPath) {
      if (operation['x-astronomer-operation-status-path'] !== expectedStatusPath) {
        fail(method, routePath, `durable mutation must declare exact status path ${expectedStatusPath}`);
      }
      if (!spec.paths?.[expectedStatusPath]?.get) {
        fail(method, routePath, `durable mutation status read ${expectedStatusPath} is not documented as GET`);
      }
    }
    for (const name of [...routePath.matchAll(/\{([^}]+)\}/g)].map((match) => match[1].replace(/^\.+/, ''))) {
      const parameter = parameters.find((candidate) => candidate.in === 'path' && candidate.name === name);
      if (!parameter) fail(method, routePath, `path template {${name}} has no parameter contract`);
      else if (parameter.required !== true) fail(method, routePath, `path parameter ${name} must be required`);
    }

    const successes = Object.entries(operation.responses ?? {}).filter(([status]) => /^[23]\d\d$/.test(status));
    if (!successes.length && !['stream', 'proxy'].includes(routeClass)) fail(method, routePath, 'has no successful response');
    for (const [status, responseRaw] of successes) {
      if (['204', '205', '303', '304'].includes(status) || operation['x-astronomer-bodyless-success'] === true) continue;
      const response = resolved(responseRaw) ?? {};
      const typed = Object.values(response.content ?? {}).some((media) => media?.schema);
      if (!typed) fail(method, routePath, `${status} response has no media schema`);
    }
  }
}

for (const exceptionKey of async202Exceptions.keys()) {
  if (!seenAsync202Exceptions.has(exceptionKey)) failures.push(`Missing registered async-202 exception operation ${exceptionKey}`);
}
if (async202Exceptions.size !== EXPECTED_202_EXCEPTION_COUNT) {
  failures.push(`Async-202 exception registry changed: ${async202Exceptions.size}, expected exactly ${EXPECTED_202_EXCEPTION_COUNT}`);
}
if (actionable202Count !== EXPECTED_ACTIONABLE_202_COUNT) {
  failures.push(`Actionable 202 inventory changed: ${actionable202Count} operations, expected exactly ${EXPECTED_ACTIONABLE_202_COUNT}`);
}

if (failures.length) {
  console.error(`OpenAPI quality gate failed with ${failures.length} issue(s):`);
  for (const failure of failures) console.error(`  - ${failure}`);
  process.exit(1);
}
console.log(`OpenAPI quality gate passed for ${operationCount} operations, including ${actionable202Count} actionable 202 mutations and ${seenAsync202Exceptions.size} explicit exceptions.`);
