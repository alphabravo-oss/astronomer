import { fireEvent, render, screen } from '@testing-library/react';
import { Input } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Switch } from '@/components/ui/switch';
import { TabStrip } from '@/components/ui/tabs';

describe('form primitives', () => {
  it('renders an input with the shared control chrome', () => {
    render(<Input aria-label="Host" placeholder="smtp.example.com" />);
    const input = screen.getByLabelText('Host');
    expect(input).toHaveClass('h-9', 'rounded-md', 'border-input');
    expect(input).toHaveAttribute('placeholder', 'smtp.example.com');
  });

  it('renders a native select and textarea on the same chrome', () => {
    render(
      <>
        <Select aria-label="Provider">
          <option value="aws">AWS</option>
        </Select>
        <Textarea aria-label="Notes" />
      </>,
    );
    expect(screen.getByLabelText('Provider')).toHaveClass('h-9', 'rounded-md');
    expect(screen.getByLabelText('Notes')).toHaveClass('min-h-[120px]');
  });
});

describe('Card / Badge / Switch / Tabs', () => {
  it('renders a card frame', () => {
    render(
      <Card>
        <CardHeader>
          <CardTitle>Instances</CardTitle>
        </CardHeader>
        <CardContent>Table</CardContent>
      </Card>,
    );
    expect(screen.getByText('Instances')).toBeInTheDocument();
    expect(screen.getByText('Table')).toBeInTheDocument();
  });

  it('applies status badge variants', () => {
    const { container } = render(<Badge variant="error">Failed</Badge>);
    expect(container.firstChild).toHaveClass('text-status-error');
  });

  it('toggles the switch via onCheckedChange', () => {
    const onCheckedChange = vi.fn();
    render(<Switch checked={false} onCheckedChange={onCheckedChange} aria-label="Enabled" />);
    fireEvent.click(screen.getByRole('switch', { name: 'Enabled' }));
    expect(onCheckedChange).toHaveBeenCalledWith(true);
  });

  it('renders an underline tab strip and reports the clicked key', () => {
    const onChange = vi.fn();
    render(
      <TabStrip
        value="rules"
        onChange={onChange}
        tabs={[
          { key: 'rules', label: 'Alert Rules' },
          { key: 'channels', label: 'Channels' },
        ]}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Channels' }));
    expect(onChange).toHaveBeenCalledWith('channels');
  });
});
