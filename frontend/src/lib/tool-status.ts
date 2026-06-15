import type { ToolStatus } from '@/types';

export function normalizeToolStatus(status?: string | null): ToolStatus {
  const value = String(status || '').trim().toLowerCase();

  switch (value) {
    case '':
      return 'not_installed';
    case 'not_installed':
    case 'not-installed':
      return 'not_installed';
    case 'deployed':
    case 'installed':
      return 'installed';
    case 'pending':
    case 'pending_install':
    case 'pending-install':
    case 'installing':
      return 'installing';
    case 'pending_upgrade':
    case 'pending-upgrade':
    case 'upgrading':
      return 'upgrading';
    case 'pending_uninstall':
    case 'pending-uninstall':
    case 'uninstalling':
      return 'uninstalling';
    case 'failed':
      return 'failed';
    case 'installed_unmanaged':
    case 'installed-unmanaged':
    case 'unmanaged':
      return 'installed_unmanaged';
    case 'unknown':
      return 'unknown';
    default:
      return 'unknown';
  }
}
