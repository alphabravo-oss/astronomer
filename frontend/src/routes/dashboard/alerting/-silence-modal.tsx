import { useState } from 'react';
import { Plus, X } from 'lucide-react';
import { useCreateAlertSilence } from '@/lib/hooks';
import { ActionButton } from '@/components/ui/action-button';
import { Input } from '@/components/ui/input';
import { ModalShell } from '@/components/ui/modal-shell';
import { Select } from '@/components/ui/select';

export function SilenceModal({ onClose }: { onClose: () => void }) {
  const createSilence = useCreateAlertSilence();
  const [form, setForm] = useState({
    reason: '',
    duration: '1h',
    matcherKey: '',
    matcherValue: '',
    matchers: {} as Record<string, string>,
  });

  const addMatcher = () => {
    if (form.matcherKey && form.matcherValue) {
      setForm((f) => ({
        ...f,
        matchers: { ...f.matchers, [f.matcherKey]: f.matcherValue },
        matcherKey: '',
        matcherValue: '',
      }));
    }
  };

  const removeMatcher = (key: string) => {
    setForm((f) => {
      const m = { ...f.matchers };
      delete m[key];
      return { ...f, matchers: m };
    });
  };

  const handleSave = async () => {
    try {
      await createSilence.mutateAsync({
        reason: form.reason,
        duration: form.duration,
        matchers: form.matchers,
      });
      onClose();
    } catch {
      // Error handled by mutation
    }
  };

  return (
    <ModalShell
      title="Create Silence"
      onClose={onClose}
      size="md"
      footer={
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            onClick={handleSave}
            disabled={!form.reason}
            loading={createSilence.isPending}
          >
            Create Silence
          </ActionButton>
        </>
      }
      footerClassName="flex items-center justify-end gap-2"
    >
      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Reason</label>
        <Input
          value={form.reason}
          onChange={(e) => setForm((f) => ({ ...f, reason: e.target.value }))}
          placeholder="Scheduled maintenance window"
        />
      </div>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Duration</label>
        <Select
          value={form.duration}
          onChange={(e) => setForm((f) => ({ ...f, duration: e.target.value }))}
        >
          <option value="30m">30 minutes</option>
          <option value="1h">1 hour</option>
          <option value="2h">2 hours</option>
          <option value="4h">4 hours</option>
          <option value="8h">8 hours</option>
          <option value="24h">24 hours</option>
          <option value="7d">7 days</option>
        </Select>
      </div>

      <div className="space-y-2">
        <label className="text-sm font-medium text-foreground">Matchers</label>
        <div className="flex gap-2">
          <Input
            value={form.matcherKey}
            onChange={(e) => setForm((f) => ({ ...f, matcherKey: e.target.value }))}
            placeholder="Label name"
            className="h-8 flex-1 font-mono text-xs w-auto"
          />
          <Input
            value={form.matcherValue}
            onChange={(e) => setForm((f) => ({ ...f, matcherValue: e.target.value }))}
            placeholder="Value"
            className="h-8 flex-1 font-mono text-xs w-auto"
          />
          <ActionButton
            size="icon"
            onClick={addMatcher}
            disabled={!form.matcherKey || !form.matcherValue}
            icon={<Plus className="h-3.5 w-3.5" />}
            title="Add matcher"
          />
        </div>
        {Object.entries(form.matchers).length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {Object.entries(form.matchers).map(([k, v]) => (
              <span
                key={k}
                className="inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground font-mono"
              >
                {k}={v}
                <button type="button" onClick={() => removeMatcher(k)} className="hover:text-foreground">
                  <X className="h-3 w-3" />
                </button>
              </span>
            ))}
          </div>
        )}
      </div>
    </ModalShell>
  );
}
