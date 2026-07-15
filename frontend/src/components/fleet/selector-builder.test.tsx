import { render, fireEvent } from '@testing-library/react';
import { SelectorBuilder, buildSelector } from './selector-builder';
import { selectorIsEmpty } from '@/lib/api/fleet-operations';

describe('buildSelector — emits the exact wire shape', () => {
  it('drops empty label/expression rows', () => {
    const sel = buildSelector([{ key: '', value: '' }], [], []);
    expect(sel).toEqual({});
    expect(selectorIsEmpty(sel)).toBe(true);
  });

  it('builds matchLabels from non-empty rows only', () => {
    const sel = buildSelector(
      [
        { key: 'tier', value: 'prod' },
        { key: '', value: 'ignored' },
      ],
      [],
      [],
    );
    expect(sel).toEqual({ matchLabels: { tier: 'prod' } });
  });

  it('splits In/NotIn values on commas and omits values for Exists/DoesNotExist', () => {
    const sel = buildSelector(
      [],
      [
        { key: 'env', operator: 'In', values: 'staging, canary' },
        { key: 'gpu', operator: 'Exists', values: 'ignored' },
      ],
      [],
    );
    expect(sel).toEqual({
      matchExpressions: [
        { key: 'env', operator: 'In', values: ['staging', 'canary'] },
        { key: 'gpu', operator: 'Exists' },
      ],
    });
  });

  it('includes matchGroupIDs when groups are selected', () => {
    const sel = buildSelector([{ key: 'tier', value: 'prod' }], [], ['g1', 'g2']);
    expect(sel).toEqual({
      matchLabels: { tier: 'prod' },
      matchGroupIDs: ['g1', 'g2'],
    });
  });
});

describe('SelectorBuilder — onChange wiring', () => {
  it('emits an empty selector on mount and a populated one after typing', () => {
    const onChange = vi.fn();
    const { getByLabelText } = render(<SelectorBuilder onChange={onChange} />);

    // Initial emit is the empty selector (single blank label row).
    expect(onChange).toHaveBeenLastCalledWith({});

    fireEvent.change(getByLabelText('label key 1'), { target: { value: 'tier' } });
    fireEvent.change(getByLabelText('label value 1'), { target: { value: 'prod' } });

    expect(onChange).toHaveBeenLastCalledWith({ matchLabels: { tier: 'prod' } });
  });

  it('renders a group multi-select when groups are provided', () => {
    const onChange = vi.fn();
    const { getByRole } = render(
      <SelectorBuilder onChange={onChange} groups={[{ id: 'g1', name: 'Prod fleet' }]} />,
    );
    fireEvent.click(getByRole('button', { name: 'Prod fleet' }));
    expect(onChange).toHaveBeenLastCalledWith({ matchGroupIDs: ['g1'] });
  });
});
