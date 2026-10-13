import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { execFileSync } from 'node:child_process';
import { test } from 'node:test';
import { gitShimAnchor, gitShimGuard, patchRunnerGit } from './patch-runner-git.mjs';

test('secondary repositories fetch real commits while the candidate stays synthetic', () => {
  const root = mkdtempSync(join(tmpdir(), 'astronomer-local-ci-git-'));
  const workspace = join(root, 'workspace');
  const upstream = join(root, 'upstream');
  const secondary = join(workspace, 'trivy');
  const git = (cwd, ...args) => execFileSync('/usr/bin/git', args, { cwd, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
  try {
    for (const path of [workspace, upstream, secondary]) mkdirSync(path, { recursive: true });
    for (const path of [workspace, upstream]) {
      git(path, 'init');
      git(path, '-c', 'user.name=Test', '-c', 'user.email=test@example.test', 'commit', '--allow-empty', '-m', 'fixture');
    }
    const sha = git(upstream, 'rev-parse', 'HEAD');
    git(secondary, 'init');
    git(secondary, 'remote', 'add', 'origin', upstream);
    const shim = join(root, 'git-shim');
    writeFileSync(shim, '#!/bin/bash\n' + gitShimGuard.replaceAll('/usr/bin/git.real', '/usr/bin/git') + '\necho synthetic-workspace\n', { mode: 0o755 });
    const run = (cwd, ...args) => execFileSync(shim, args, { cwd, encoding: 'utf8', env: { ...process.env, GITHUB_WORKSPACE: workspace }, stdio: ['ignore', 'pipe', 'pipe'] }).trim();
    assert.equal(run(workspace, 'fetch', 'origin'), 'synthetic-workspace');
    run(secondary, 'fetch', '--depth=1', 'origin', sha);
    run(secondary, 'checkout', '--detach', sha);
    assert.equal(run(secondary, 'rev-parse', 'HEAD'), sha);
    assert.equal(run(secondary, 'config', '--get', 'remote.origin.url'), upstream);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('Git shim patch is version-bound, exact and idempotent', () => {
  const source = `before ${gitShimAnchor} after`;
  const patched = patchRunnerGit(source, '0.18.1');
  assert.equal(patchRunnerGit(patched, '0.18.1'), patched);
  assert.match(patched, /\\\$\{GITHUB_WORKSPACE/);
  assert.throws(() => patchRunnerGit(source, '0.18.2'), /Review/);
  for (const source of ['', gitShimAnchor + gitShimAnchor]) assert.throws(() => patchRunnerGit(source, '0.18.1'), /Unexpected/);
});
