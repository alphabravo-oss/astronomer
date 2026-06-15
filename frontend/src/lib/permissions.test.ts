import { can, explainPermission, isSuperuser } from './permissions';
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

  it('explains denied scoped permissions with the required grant and request path', () => {
    const decision = explainPermission(baseUser, 'clusters', 'update', { type: 'cluster', id: 'cluster-a' });

    expect(decision.allowed).toBe(false);
    expect(decision.permission).toBe('clusters:update');
    expect(decision.scopeLabel).toBe('cluster:cluster-a');
    expect(decision.disabledReason).toContain('Requires clusters:update for cluster cluster-a');
    expect(decision.disabledReason).toContain('Request access from a cluster owner or platform administrator.');
  });

  it('reports the role binding that grants a scoped permission', () => {
    const user: User = {
      ...baseUser,
      roles: {
        global: [],
        cluster: [
          {
            id: 'b1',
            roleName: 'Cluster Operator',
            clusterId: 'cluster-a',
            roleRules: [{ resource: 'clusters', verbs: ['update'] }],
          },
        ],
        project: [],
      },
    };

    const decision = explainPermission(user, 'clusters', 'update', { type: 'cluster', id: 'cluster-a' });

    expect(decision.allowed).toBe(true);
    expect(decision.grantedBy).toEqual(['Cluster Operator (cluster:cluster-a)']);
    expect(decision.reason).toBe('Granted by Cluster Operator (cluster:cluster-a).');
  });
});
