import { formatToastError } from '@/lib/toast';

describe('formatToastError()', () => {
  it('uses API envelope error messages', () => {
    expect(formatToastError('Save failed', {
      response: { data: { error: { message: 'invalid field' } } },
    })).toBe('Save failed: invalid field');
  });

  it('uses plain Error messages', () => {
    expect(formatToastError('Delete failed', new Error('permission denied'))).toBe(
      'Delete failed: permission denied',
    );
  });

  it('uses string errors', () => {
    expect(formatToastError('Validation failed', 'missing namespace')).toBe(
      'Validation failed: missing namespace',
    );
  });

  it('falls back for unknown shapes', () => {
    expect(formatToastError('Apply failed', { code: 'unknown' }, 'try again')).toBe(
      'Apply failed: try again',
    );
  });
});
