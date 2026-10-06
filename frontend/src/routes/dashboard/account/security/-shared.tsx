import { useMemo } from "react";
import { Copy, Download, AlertTriangle } from "lucide-react";
import { toastSuccess } from "@/lib/toast";
import { downloadBlob } from "@/lib/utils";
import { Input } from "@/components/ui/input";
import { ActionButton } from "@/components/ui/action-button";

export function RecoveryCodesBlock({
  codes,
  acknowledged,
  onAcknowledge,
  onFinish,
}: {
  codes: string[];
  acknowledged: boolean;
  onAcknowledge: (v: boolean) => void;
  onFinish: () => void;
}) {
  const codesText = useMemo(() => codes.join("\n"), [codes]);

  const download = () => {
    downloadBlob(
      codesText + "\n",
      "astronomer-recovery-codes.txt",
      "text/plain",
    );
  };

  const copy = () => {
    navigator.clipboard.writeText(codesText);
    toastSuccess("Recovery codes copied to clipboard");
  };

  return (
    <div className="space-y-4">
      <div className="flex items-start gap-3 p-3 rounded-md bg-status-warning/10 border border-status-warning/30">
        <AlertTriangle className="h-4 w-4 text-status-warning shrink-0 mt-0.5" />
        <p className="text-xs text-status-warning">
          Save these 10 single-use recovery codes somewhere safe. They will{" "}
          <strong>not</strong> be shown again. Each one lets you log in once
          without your authenticator.
        </p>
      </div>
      <pre className="rounded-md border border-border bg-muted/40 p-4 text-sm font-mono text-foreground grid grid-cols-2 gap-x-6 gap-y-1 leading-6">
        {codes.map((c) => (
          <span key={c}>{c}</span>
        ))}
      </pre>
      <div className="flex items-center gap-2">
        <ActionButton onClick={copy} icon={<Copy className="h-4 w-4" />}>
          Copy
        </ActionButton>
        <ActionButton
          onClick={download}
          icon={<Download className="h-4 w-4" />}
        >
          Download as text
        </ActionButton>
      </div>
      <label className="flex items-center gap-2 text-sm text-foreground">
        <Input
          type="checkbox"
          checked={acknowledged}
          onChange={(e) => onAcknowledge(e.target.checked)}
          className="rounded-sm border-border"
        />
        I&apos;ve saved my recovery codes somewhere safe
      </label>
      <div className="flex items-center justify-end gap-2 pt-2 border-t border-border">
        <ActionButton
          onClick={onFinish}
          disabled={!acknowledged}
          intent="primary"
        >
          Done
        </ActionButton>
      </div>
    </div>
  );
}

export function CodeInput({
  id,
  value,
  onChange,
  autoFocus,
}: {
  id?: string;
  value: string;
  onChange: (v: string) => void;
  autoFocus?: boolean;
}) {
  // Single 6-char numeric input: simpler than 6 separate boxes, paste works
  // out of the box, and it still feels good with `inputMode=numeric`.
  return (
    <Input
      id={id}
      type="text"
      inputMode="numeric"
      pattern="[0-9]*"
      maxLength={6}
      value={value}
      data-initial-focus={autoFocus}
      onChange={(e) => onChange(e.target.value.replace(/\D/g, "").slice(0, 6))}
      placeholder="123 456"
      className="w-full h-12 px-3 rounded-md border border-border bg-background text-center text-2xl font-mono tracking-[0.4em] text-foreground focus:outline-hidden focus:ring-2 focus:ring-ring"
      autoComplete="one-time-code"
    />
  );
}
