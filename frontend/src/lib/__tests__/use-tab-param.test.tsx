/**
 * useTabParam (P2.4): the hook's per-page allowlist stays the real validator
 * on top of the routes' passthrough `validateSearch`, and its setter must
 * preserve every unrelated query param on a no-scroll replace.
 */
import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useTabParam } from '@/lib/use-tab-param';

const nav = vi.hoisted(() => ({
  search: '',
  replace: vi.fn(),
}));

vi.mock('@/lib/navigation', () => ({
  useRouter: () => ({ push: vi.fn(), replace: nav.replace, back: vi.fn() }),
  usePathname: () => '/dashboard/security',
  useSearchParams: () => new URLSearchParams(nav.search),
}));

const KEYS = ['cis', 'templates', 'policies'] as const;

describe('useTabParam', () => {
  beforeEach(() => {
    nav.search = '';
    nav.replace.mockClear();
  });

  it('falls back when the param is absent', () => {
    const { result } = renderHook(() => useTabParam(KEYS, 'cis'));
    expect(result.current[0]).toBe('cis');
  });

  it('falls back when the param is not in the allowlist', () => {
    nav.search = 'tab=bogus';
    const { result } = renderHook(() => useTabParam(KEYS, 'cis'));
    expect(result.current[0]).toBe('cis');
  });

  it('resolves an allowlisted param', () => {
    nav.search = 'tab=policies';
    const { result } = renderHook(() => useTabParam(KEYS, 'cis'));
    expect(result.current[0]).toBe('policies');
  });

  it('preserves unrelated query params on setTab and replaces without scroll', () => {
    nav.search = 'cluster=c1&tab=cis';
    const { result } = renderHook(() => useTabParam(KEYS, 'cis'));

    act(() => {
      result.current[1]('templates');
    });

    expect(nav.replace).toHaveBeenCalledWith(
      '/dashboard/security?cluster=c1&tab=templates',
      { scroll: false },
    );
  });

  it('supports a custom param name without touching ?tab=', () => {
    nav.search = 'tab=cis&sync=OutOfSync';
    const { result } = renderHook(() =>
      useTabParam(['', 'Synced', 'OutOfSync'] as const, '', 'sync'),
    );
    expect(result.current[0]).toBe('OutOfSync');

    act(() => {
      result.current[1]('Synced');
    });

    expect(nav.replace).toHaveBeenCalledWith(
      '/dashboard/security?tab=cis&sync=Synced',
      { scroll: false },
    );
  });
});
