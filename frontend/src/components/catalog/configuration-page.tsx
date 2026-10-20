import type { ReactNode } from "react";
import { ActionButton } from "@/components/ui/action-button";
import { PageHeader, PageShell } from "@/components/ui/page";
import { ArrowLeft } from "lucide-react";

/** Configuration is a document page. Only the final action uses a dialog. */
export function ConfigurationPage({
  title,
  subtitle,
  onClose,
  children,
  footer,
  headerActions,
}: {
  title: string;
  subtitle?: string;
  onClose: () => void;
  children: ReactNode;
  footer?: ReactNode;
  headerActions?: ReactNode;
}) {
  return (
    <PageShell className="mx-auto w-full max-w-7xl min-w-0 pb-4">
      <ActionButton
        intent="ghost"
        icon={<ArrowLeft className="h-4 w-4" />}
        onClick={onClose}
      >
        Back
      </ActionButton>
      <PageHeader title={title} description={subtitle} />
      {headerActions}
      <div className="min-w-0 space-y-6">{children}</div>
      {footer && (
        <div className="sticky bottom-0 z-20 -mx-2 border-t border-border bg-background/95 px-2 py-4 backdrop-blur-sm sm:mx-0">
          {footer}
        </div>
      )}
    </PageShell>
  );
}
