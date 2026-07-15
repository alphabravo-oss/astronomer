import { queryKeys } from './query-keys';

describe('Argo CD query keys', () => {
  it('includes every operation-list parameter that changes the dataset', () => {
    const recent = queryKeys.argocd.operationList({ limit: 5 });
    const full = queryKeys.argocd.operationList({ limit: 100 });
    const filtered = queryKeys.argocd.operationList({
      targetType: 'application',
      targetKey: 'app-1',
      status: 'running',
      limit: 25,
      offset: 50,
    });

    expect(recent).not.toEqual(full);
    expect(filtered).toEqual([
      'argocd',
      'operations',
      'list',
      {
        targetType: 'application',
        targetKey: 'app-1',
        status: 'running',
        limit: 25,
        offset: 50,
      },
    ]);
    expect(recent.slice(0, queryKeys.argocd.operations.length)).toEqual(
      queryKeys.argocd.operations,
    );
  });
});
