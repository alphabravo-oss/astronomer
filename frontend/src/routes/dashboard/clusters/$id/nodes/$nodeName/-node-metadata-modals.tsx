import { ActionButton } from "@/components/ui/action-button";
import { Input } from "@/components/ui/input";
import { ModalShell } from "@/components/ui/modal-shell";
import { Select } from "@/components/ui/select";
import type { NodeTaintRequest } from "@/lib/api/nodes";
import type { NodeActions } from "./-node-actions";

interface KeyValue {
  key: string;
  value: string;
}

/**
 * The three "add metadata" modals for the node detail page (taint, label,
 * annotation). Extracted from index.tsx so the route file stays focused on
 * data-fetching and tab orchestration, and split into three small
 * components (rather than one) so each stays well under the 240-line
 * function budget.
 */

export function AddTaintModal({
  onClose,
  value,
  onChange,
  onSubmit,
  pending,
  canUpdate,
  blockedReason,
}: {
  onClose: () => void;
  value: NodeTaintRequest;
  onChange: (value: NodeTaintRequest) => void;
  onSubmit: () => void;
  pending: boolean;
  canUpdate: boolean;
  blockedReason?: string;
}) {
  return (
    <ModalShell
      title="Add Taint"
      onClose={onClose}
      size="sm"
      footerClassName="flex items-center justify-end gap-2"
      footer={
        <>
          <ActionButton size="sm" intent="ghost" onClick={onClose}>
            Cancel
          </ActionButton>
          <ActionButton
            size="sm"
            intent="primary"
            onClick={onSubmit}
            disabled={!value.key || pending || !canUpdate}
            disabledReason={blockedReason}
            loading={pending}
          >
            Add Taint
          </ActionButton>
        </>
      }
    >
      <div>
        <label
          className="block text-xs text-muted-foreground mb-1"
          htmlFor="field-b10d840c-875"
        >
          Key
        </label>
        <Input
          id="field-b10d840c-875"
          type="text"
          value={value.key}
          onChange={(e) => onChange({ ...value, key: e.target.value })}
          placeholder="node.kubernetes.io/unreachable"
          data-initial-focus
          className="h-8 font-mono"
        />
      </div>
      <div>
        <label
          className="block text-xs text-muted-foreground mb-1"
          htmlFor="field-b10d840c-880"
        >
          Value
        </label>
        <Input
          id="field-b10d840c-880"
          type="text"
          value={value.value ?? ""}
          onChange={(e) => onChange({ ...value, value: e.target.value })}
          placeholder="(optional)"
          className="h-8 font-mono"
        />
      </div>
      <div>
        <label
          className="block text-xs text-muted-foreground mb-1"
          htmlFor="field-b10d840c-885"
        >
          Effect
        </label>
        <Select
          id="field-b10d840c-885"
          value={value.effect}
          onChange={(e) =>
            onChange({
              ...value,
              effect: e.target.value as NodeTaintRequest["effect"],
            })
          }
          className="h-8"
        >
          <option value="NoSchedule">NoSchedule</option>
          <option value="PreferNoSchedule">PreferNoSchedule</option>
          <option value="NoExecute">NoExecute</option>
        </Select>
      </div>
    </ModalShell>
  );
}

export function AddLabelModal({
  onClose,
  value,
  onChange,
  onSubmit,
  pending,
  canUpdate,
  blockedReason,
}: {
  onClose: () => void;
  value: KeyValue;
  onChange: (value: KeyValue) => void;
  onSubmit: () => void;
  pending: boolean;
  canUpdate: boolean;
  blockedReason?: string;
}) {
  return (
    <ModalShell
      title="Add Label"
      onClose={onClose}
      size="sm"
      footerClassName="flex items-center justify-end gap-2"
      footer={
        <>
          <ActionButton size="sm" intent="ghost" onClick={onClose}>
            Cancel
          </ActionButton>
          <ActionButton
            size="sm"
            intent="primary"
            onClick={onSubmit}
            disabled={!value.key || pending || !canUpdate}
            disabledReason={blockedReason}
            loading={pending}
          >
            Add Label
          </ActionButton>
        </>
      }
    >
      <div>
        <label
          className="block text-xs text-muted-foreground mb-1"
          htmlFor="field-b10d840c-921"
        >
          Key
        </label>
        <Input
          id="field-b10d840c-921"
          type="text"
          value={value.key}
          onChange={(e) => onChange({ ...value, key: e.target.value })}
          placeholder="app.kubernetes.io/name"
          data-initial-focus
          className="h-8 font-mono"
        />
      </div>
      <div>
        <label
          className="block text-xs text-muted-foreground mb-1"
          htmlFor="field-b10d840c-926"
        >
          Value
        </label>
        <Input
          id="field-b10d840c-926"
          type="text"
          value={value.value}
          onChange={(e) => onChange({ ...value, value: e.target.value })}
          placeholder="my-app"
          className="h-8 font-mono"
        />
      </div>
    </ModalShell>
  );
}

export function AddAnnotationModal({
  onClose,
  value,
  onChange,
  onSubmit,
  pending,
  canUpdate,
  blockedReason,
}: {
  onClose: () => void;
  value: KeyValue;
  onChange: (value: KeyValue) => void;
  onSubmit: () => void;
  pending: boolean;
  canUpdate: boolean;
  blockedReason?: string;
}) {
  return (
    <ModalShell
      title="Add Annotation"
      onClose={onClose}
      size="sm"
      footerClassName="flex items-center justify-end gap-2"
      footer={
        <>
          <ActionButton size="sm" intent="ghost" onClick={onClose}>
            Cancel
          </ActionButton>
          <ActionButton
            size="sm"
            intent="primary"
            onClick={onSubmit}
            disabled={!value.key || pending || !canUpdate}
            disabledReason={blockedReason}
            loading={pending}
          >
            Add Annotation
          </ActionButton>
        </>
      }
    >
      <div>
        <label
          className="block text-xs text-muted-foreground mb-1"
          htmlFor="field-b10d840c-957"
        >
          Key
        </label>
        <Input
          id="field-b10d840c-957"
          type="text"
          value={value.key}
          onChange={(e) => onChange({ ...value, key: e.target.value })}
          placeholder="example.com/owner"
          data-initial-focus
          className="h-8 font-mono"
        />
      </div>
      <div>
        <label
          className="block text-xs text-muted-foreground mb-1"
          htmlFor="field-b10d840c-962"
        >
          Value
        </label>
        <Input
          id="field-b10d840c-962"
          type="text"
          value={value.value}
          onChange={(e) => onChange({ ...value, value: e.target.value })}
          placeholder="platform"
          className="h-8 font-mono"
        />
      </div>
    </ModalShell>
  );
}

/**
 * Renders whichever of the three "add metadata" modals is currently open,
 * wiring them straight to a useNodeActions() result. Kept separate from the
 * individual modal components above so each stays independently testable
 * and well under the function-length budget.
 */
export function NodeMetadataModals({
  actions,
  canUpdate,
  blockedReason,
}: {
  actions: NodeActions;
  canUpdate: boolean;
  blockedReason?: string;
}) {
  return (
    <>
      {actions.showAddTaint && (
        <AddTaintModal
          onClose={() => actions.setShowAddTaint(false)}
          value={actions.newTaint}
          onChange={actions.setNewTaint}
          onSubmit={actions.handleAddTaint}
          pending={actions.addTaintPending}
          canUpdate={canUpdate}
          blockedReason={blockedReason}
        />
      )}

      {actions.showAddLabel && (
        <AddLabelModal
          onClose={() => actions.setShowAddLabel(false)}
          value={actions.newLabel}
          onChange={actions.setNewLabel}
          onSubmit={actions.handleAddLabel}
          pending={actions.addLabelPending}
          canUpdate={canUpdate}
          blockedReason={blockedReason}
        />
      )}

      {actions.showAddAnnotation && (
        <AddAnnotationModal
          onClose={() => actions.setShowAddAnnotation(false)}
          value={actions.newAnnotation}
          onChange={actions.setNewAnnotation}
          onSubmit={actions.handleAddAnnotation}
          pending={actions.addAnnotationPending}
          canUpdate={canUpdate}
          blockedReason={blockedReason}
        />
      )}
    </>
  );
}
