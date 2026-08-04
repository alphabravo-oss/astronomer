import { describe, it, expect } from 'vitest';
import { isBrowserReachable, argoInstanceWebHref } from './argocd';

describe('isBrowserReachable', () => {
  it('rejects cluster-internal + private hosts', () => {
    for (const u of [
      'http://astro-argocd-server.astronomer.svc.cluster.local/argocd',
      'http://foo.svc',
      'http://localhost:8080',
      'http://127.0.0.1',
      'http://10.1.2.3',
      'http://192.168.1.10',
      'http://172.16.0.1',
      'not-a-url',
      '',
    ]) {
      expect(isBrowserReachable(u)).toBe(false);
    }
  });
  it('accepts public URLs', () => {
    expect(isBrowserReachable('https://argocd.example.com')).toBe(true);
    expect(isBrowserReachable('https://astronomer.dev.alphabravo.io/argocd')).toBe(true);
  });
});

describe('argoInstanceWebHref', () => {
  it('routes the in-cluster local instance through the /argocd/ proxy', () => {
    expect(argoInstanceWebHref('http://astro-argocd-server.astronomer.svc.cluster.local/argocd')).toBe(
      '/argocd/applications',
    );
  });
  it('opens a reachable instance directly', () => {
    expect(argoInstanceWebHref('https://argocd.example.com')).toBe('https://argocd.example.com');
  });
});
