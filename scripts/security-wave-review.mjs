#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(scriptDir, '..');
const reviewPath = path.join(repoRoot, 'docs/security-wave-review.json');
const allowedDispositions = new Set([
  'locally_evidenced',
  'reviewed_unchanged',
  'live_qualification_pending',
]);

function fail(message) {
  console.error(`security wave review: ${message}`);
  process.exitCode = 1;
}

let review;
try {
  review = JSON.parse(fs.readFileSync(reviewPath, 'utf8'));
} catch (error) {
  fail(`cannot parse docs/security-wave-review.json: ${error.message}`);
  process.exit();
}

if (review.version !== 1) fail(`version is ${review.version}; want 1`);
if (!String(review.policy || '').includes('not a GA waiver')) {
  fail('policy must state that review completion is not a GA waiver');
}

const controls = Array.isArray(review.controls) ? review.controls : [];
if (controls.length !== 10) fail(`control count is ${controls.length}; want 10`);
const controlIDs = controls.map((control) => control.id);
const uniqueControlIDs = new Set(controlIDs);
if (uniqueControlIDs.size !== controlIDs.length) fail('control IDs are not unique');

for (const control of controls) {
  if (!control.id || !control.title || !control.acceptance) {
    fail(`control ${control.id || '<unknown>'} is missing id, title, or acceptance`);
  }
  if (!Array.isArray(control.evidence) || control.evidence.length < 2) {
    fail(`control ${control.id} needs at least two independent evidence anchors`);
    continue;
  }
  for (const evidence of control.evidence) {
    const relativePath = String(evidence.path || '');
    const anchor = String(evidence.anchor || '');
    if (!relativePath || path.isAbsolute(relativePath) || relativePath.includes('..')) {
      fail(`control ${control.id} has invalid evidence path ${JSON.stringify(relativePath)}`);
      continue;
    }
    if (!anchor) {
      fail(`control ${control.id} has an empty evidence anchor for ${relativePath}`);
      continue;
    }
    const absolutePath = path.join(repoRoot, relativePath);
    if (!fs.existsSync(absolutePath)) {
      fail(`control ${control.id} evidence file does not exist: ${relativePath}`);
      continue;
    }
    const source = fs.readFileSync(absolutePath, 'utf8');
    if (!source.includes(anchor)) {
      fail(`control ${control.id} evidence anchor is stale: ${relativePath} :: ${anchor}`);
    }
  }
}

const reviews = Array.isArray(review.wave_reviews) ? review.wave_reviews : [];
if (reviews.length !== 8) fail(`wave review count is ${reviews.length}; want 8`);
const waveNumbers = reviews.map((entry) => entry.wave);
if (new Set(waveNumbers).size !== waveNumbers.length) fail('wave numbers are not unique');

for (let wave = 0; wave <= 7; wave += 1) {
  const entry = reviews.find((candidate) => candidate.wave === wave);
  if (!entry) {
    fail(`wave ${wave} has no review`);
    continue;
  }
  if (!entry.scope || !entry.notes) fail(`wave ${wave} needs scope and notes`);
  const dispositions = entry.dispositions && typeof entry.dispositions === 'object'
    ? entry.dispositions
    : {};
  const dispositionIDs = Object.keys(dispositions);
  const missing = controlIDs.filter((id) => !Object.hasOwn(dispositions, id));
  const unknown = dispositionIDs.filter((id) => !uniqueControlIDs.has(id));
  if (missing.length > 0) fail(`wave ${wave} is missing controls: ${missing.join(', ')}`);
  if (unknown.length > 0) fail(`wave ${wave} has unknown controls: ${unknown.join(', ')}`);
  for (const [id, disposition] of Object.entries(dispositions)) {
    if (!allowedDispositions.has(disposition)) {
      fail(`wave ${wave} control ${id} has invalid disposition ${JSON.stringify(disposition)}`);
    }
  }
}

const finalWave = reviews.find((entry) => entry.wave === 7);
if (finalWave && !Object.values(finalWave.dispositions).includes('live_qualification_pending')) {
  fail('wave 7 must preserve live qualification blockers until release evidence exists');
}

if (!process.exitCode) {
  const evidenceCount = controls.reduce((total, control) => total + control.evidence.length, 0);
  console.log(`security wave review: ${controls.length} controls, ${reviews.length} waves, ${evidenceCount} evidence anchors; pass`);
}
