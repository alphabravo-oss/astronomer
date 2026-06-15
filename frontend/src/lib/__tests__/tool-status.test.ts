import { normalizeToolStatus } from '@/lib/tool-status';

describe('normalizeToolStatus', () => {
  it.each([
    ['pending-install', 'installing'],
    ['pending_install', 'installing'],
    ['pending-upgrade', 'upgrading'],
    ['pending_uninstall', 'uninstalling'],
    ['deployed', 'installed'],
    ['not-installed', 'not_installed'],
    ['unexpected-status', 'unknown'],
    [undefined, 'not_installed'],
  ])('maps %s to %s', (input, expected) => {
    expect(normalizeToolStatus(input)).toBe(expected);
  });
});
