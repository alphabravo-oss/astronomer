import { formatK8sVersion } from '@/lib/utils';

describe('formatK8sVersion', () => {
  it('does not double the v the API already sends', () => {
    // Every kubernetes_version in the DB is stored this way.
    expect(formatK8sVersion('v1.30.4+k3s1')).toBe('v1.30.4+k3s1');
    expect(formatK8sVersion('V1.30.4')).toBe('V1.30.4');
  });

  it('adds one when it is missing', () => {
    expect(formatK8sVersion('1.30.4+k3s1')).toBe('v1.30.4+k3s1');
  });

  it('renders a dash rather than a bare v when there is no version', () => {
    expect(formatK8sVersion('')).toBe('—');
    expect(formatK8sVersion(null)).toBe('—');
    expect(formatK8sVersion(undefined)).toBe('—');
  });
});
