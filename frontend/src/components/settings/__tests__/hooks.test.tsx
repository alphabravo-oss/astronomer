import { act, renderHook } from '@testing-library/react';
import { useIsSuperuser } from '@/components/settings/hooks';
import { useAuthStore } from '@/lib/store';

describe('useIsSuperuser', () => {
  afterEach(() => {
    act(() => {
      useAuthStore.setState({ user: null, isAuthenticated: false });
    });
    window.localStorage.clear();
  });

  it('treats backend bootstrap admins with is_superuser as superusers', () => {
    act(() => {
      useAuthStore.setState({
        isAuthenticated: true,
        user: {
          id: 'admin-id',
          email: 'admin@alphabravo.io',
          username: 'admin',
          first_name: 'Admin',
          last_name: '',
          is_active: true,
          is_staff: true,
          is_superuser: true,
          must_change_password: false,
          roles: { global: [], cluster: [], project: [] },
        } as never,
      });
    });

    const { result } = renderHook(() => useIsSuperuser());

    expect(result.current).toEqual({ isSuperuser: true, ready: true });
  });
});
