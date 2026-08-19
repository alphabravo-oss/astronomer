import { useState } from 'react';
import { Download, LifeBuoy } from 'lucide-react';
import { downloadBlob } from '@/lib/utils';
import { toastError, toastSuccess } from '@/lib/toast';
import { ActionButton } from '@/components/ui/action-button';

// SupportTab renders the "Download support bundle" button. The bundle
// itself is a streaming zip from /api/v1/support-bundle/; superusers only.
// Errors are surfaced via toast.
export function SupportTab() {
  const [downloading, setDownloading] = useState(false);

  const handleDownload = async () => {
    setDownloading(true);
    try {
      // Use the shared axios instance so the JWT/auth interceptor stamps
      // the request; force a binary response so axios doesn't try to JSON-
      // decode the zip stream.
      const { default: api } = await import('@/lib/api');
      const res = await api.get('/support-bundle', { responseType: 'blob', timeout: 120000 });
      // Server already proposes a filename via Content-Disposition; if axios
      // didn't surface it, fall back to a sane default.
      const disposition = res.headers?.['content-disposition'] || '';
      const match = /filename="([^"]+)"/.exec(disposition);
      const filename = match?.[1] || `astronomer-support-bundle-${Date.now()}.zip`;
      downloadBlob(new Blob([res.data], { type: 'application/zip' }), filename);
      toastSuccess('Support bundle downloaded');
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Failed to download support bundle';
      toastError(message);
    } finally {
      setDownloading(false);
    }
  };

  return (
    <div className="max-w-2xl space-y-6">
      <div className="rounded-lg border border-border bg-card p-6 space-y-4">
        <div className="flex items-start gap-3">
          <LifeBuoy className="h-5 w-5 text-muted-foreground flex-shrink-0 mt-0.5" />
          <div className="space-y-1">
            <h3 className="text-sm font-semibold text-foreground">Support bundle</h3>
            <p className="text-sm text-muted-foreground">
              Downloads a zip with platform metadata, cluster rows, recent audit
              log entries, and the last 200 lines of logs from each
              control-plane pod. Useful when filing a bug or escalating to
              support.
            </p>
            <p className="text-xs text-muted-foreground">
              Passwords, CA certs, encrypted tokens, credential-shaped values,
              and sensitive pod log lines are redacted. Share the bundle only
              with people authorized to triage this install.
            </p>
          </div>
        </div>
        <ActionButton
          intent="primary"
          icon={<Download className="h-4 w-4" />}
          loading={downloading}
          onClick={handleDownload}
        >
          Download support bundle
        </ActionButton>
      </div>
    </div>
  );
}
