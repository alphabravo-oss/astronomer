import { useState, type ReactNode } from "react";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";

export function ConfigurationConfirmation({
  title,
  description,
  confirmText,
  onConfirm,
  pending,
  disabled,
  children,
}: {
  title: string;
  description: string;
  confirmText: string;
  onConfirm: () => void;
  pending?: boolean;
  disabled: boolean;
  children: (open: () => void) => ReactNode;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      {children(() => setOpen(true))}
      <ConfirmDialog
        open={open}
        title={title}
        description={description}
        confirmText={confirmText}
        loading={pending}
        confirmDisabledReason={
          disabled
            ? "Resolve the configuration or permission errors before proceeding."
            : undefined
        }
        onClose={() => setOpen(false)}
        onConfirm={() => {
          if (!disabled) {
            setOpen(false);
            onConfirm();
          }
        }}
      />
    </>
  );
}
