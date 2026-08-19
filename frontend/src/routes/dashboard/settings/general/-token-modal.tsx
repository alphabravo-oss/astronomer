import { useState } from 'react';
import { useCreateAPIToken } from '@/lib/hooks';
import { toastError } from '@/lib/toast';
import { ActionButton } from '@/components/ui/action-button';
import { CodeBlock } from '@/components/ui/code-block';
import { Input } from '@/components/ui/input';
import { ModalShell } from '@/components/ui/modal-shell';
import { Select } from '@/components/ui/select';

export function TokenModal({ onClose }: { onClose: () => void }) {
  const [newTokenForm, setNewTokenForm] = useState({ name: '', description: '', expiresInDays: 30 });
  const [createdToken, setCreatedToken] = useState<string | null>(null);
  const createToken = useCreateAPIToken();

  const handleCreateToken = async () => {
    if (!newTokenForm.name) {
      toastError('Token name is required');
      return;
    }
    try {
      const result = await createToken.mutateAsync({
        name: newTokenForm.name,
        description: newTokenForm.description || undefined,
        expiresInDays: newTokenForm.expiresInDays,
      });
      setCreatedToken(result.token);
    } catch {
      // Error handled by mutation
    }
  };

  return (
    <ModalShell
      title={createdToken ? 'Token Created' : 'Create API Token'}
      onClose={onClose}
      size="sm"
      footer={
        createdToken ? (
          <ActionButton intent="primary" onClick={onClose} className="w-full">
            Done
          </ActionButton>
        ) : (
          <>
            <ActionButton onClick={onClose}>Cancel</ActionButton>
            <ActionButton
              intent="primary"
              onClick={handleCreateToken}
              disabled={!newTokenForm.name}
              loading={createToken.isPending}
            >
              Create Token
            </ActionButton>
          </>
        )
      }
      footerClassName={createdToken ? undefined : 'flex items-center justify-end gap-2'}
    >
      {createdToken ? (
        <div className="space-y-4">
          <p className="text-sm text-status-warning">
            Copy this token now. You will not be able to see it again.
          </p>
          <CodeBlock code={createdToken} title="API Token" />
        </div>
      ) : (
        <div className="space-y-4">
          <div className="space-y-1.5">
            <label htmlFor="token-name" className="text-sm font-medium text-foreground">Token Name</label>
            <Input
              id="token-name"
              value={newTokenForm.name}
              onChange={(e) => setNewTokenForm((f) => ({ ...f, name: e.target.value }))}
              placeholder="e.g., CI/CD Pipeline"
              autoFocus
            />
          </div>

          <div className="space-y-1.5">
            <label htmlFor="token-description" className="text-sm font-medium text-foreground">Description (optional)</label>
            <Input
              id="token-description"
              value={newTokenForm.description}
              onChange={(e) => setNewTokenForm((f) => ({ ...f, description: e.target.value }))}
              placeholder="What is this token for?"
            />
          </div>

          <div className="space-y-1.5">
            <label htmlFor="token-expires" className="text-sm font-medium text-foreground">Expires In</label>
            <Select
              id="token-expires"
              value={newTokenForm.expiresInDays}
              onChange={(e) => setNewTokenForm((f) => ({ ...f, expiresInDays: Number(e.target.value) }))}
            >
              <option value={7}>7 days</option>
              <option value={30}>30 days</option>
              <option value={90}>90 days</option>
              <option value={365}>1 year</option>
              <option value={0}>Never</option>
            </Select>
          </div>
        </div>
      )}
    </ModalShell>
  );
}
