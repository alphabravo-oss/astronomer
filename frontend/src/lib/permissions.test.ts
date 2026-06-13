import { can, isSuperuser } from './permissions';
import type { User } from '@/types';

const baseUser: User = {
  id: 'u1',
  username: 'operator',
  email: 'operator@example.com',
  displayName: 'Operator',
  provider: 'local',
  globalRoles: [],
  enabled: true,
  lastLogin: '',
  createdAt: '',
};

describe('permissions', () => {
  it('allows superusers regardless of explicit role rules', () => {
    const user = { ...baseUser, isSuperuser: true };

    expect(isSuperuser(user)).toBe(true);
    expect(can(user, 'settings', 'delete')).toBe(true);
  });

  it('matches global role rules from /auth/me', () => {
    const user: User = {
      ...baseUser,
      roles: {
        global: [
          {
            id: 'b1',
            roleRules: [{ resource: 'clusters', verbs: ['read', 'list'] }],
          },
        ],
        cluster: [],
        project: [],
      },
    };

    expect(can(user, 'clusters', 'list')).toBe(true);
    expect(can(user, 'clusters', 'delete')).toBe(false);
    expect(can(user, 'settings', 'read')).toBe(false);
  });

  it('matches wildcard role rules', () => {
    const user: User = {
      ...baseUser,
      roles: {
        global: [{ id: 'b1', roleRules: [{ resource: '*', verbs: ['*'] }] }],
        cluster: [],
        project: [],
      },
    };

    expect(can(user, 'rbac', 'manage')).toBe(true);
  });

  it('filters cluster-scoped bindings by cluster id', () => {
    const user: User = {
      ...baseUser,
      roles: {
        global: [],
        cluster: [
          {
            id: 'b1',
            clusterId: 'cluster-a',
            roleRules: [{ resource: 'workloads', verbs: ['read'] }],
          },
        ],
        project: [],
      },
    };

    expect(can(user, 'workloads', 'read', { type: 'cluster', id: 'cluster-a' })).toBe(true);
    expect(can(user, 'workloads', 'read', { type: 'cluster', id: 'cluster-b' })).toBe(false);
  });
});
