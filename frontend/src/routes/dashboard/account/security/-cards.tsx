import { RefreshCw, Shield, ShieldCheck, ShieldOff } from "lucide-react";
import { formatRelativeTime } from "@/lib/utils";
import { ActionButton } from "@/components/ui/action-button";
import { Card } from "@/components/ui/card";
import type { TotpStatus } from "@/lib/api/account-security";

export function NotEnrolledCard({ onEnable }: { onEnable: () => void }) {
  return (
    <Card padding="lg">
      <div className="flex items-start gap-4">
        <div className="shrink-0 h-10 w-10 rounded-full bg-status-warning/10 flex items-center justify-center">
          <Shield className="h-5 w-5 text-status-warning" />
        </div>
        <div className="flex-1 min-w-0">
          <h2 className="text-base font-semibold text-foreground">
            Two-factor authentication is off
          </h2>
          <p className="text-sm text-muted-foreground mt-1">
            Add a one-time-code authenticator app to protect your account from
            password leaks.
          </p>
          <ActionButton
            onClick={onEnable}
            intent="primary"
            icon={<ShieldCheck className="h-4 w-4" />}
            className="mt-4"
          >
            Enable 2FA
          </ActionButton>
        </div>
      </div>
    </Card>
  );
}

export function EnrolledCard({
  status,
  onDisable,
  onRegenerate,
}: {
  status: TotpStatus;
  onDisable: () => void;
  onRegenerate: () => void;
}) {
  return (
    <div className="space-y-4">
      <Card padding="lg">
        <div className="flex items-start gap-4">
          <div className="shrink-0 h-10 w-10 rounded-full bg-status-success/10 flex items-center justify-center">
            <ShieldCheck className="h-5 w-5 text-status-success" />
          </div>
          <div className="flex-1 min-w-0">
            <h2 className="text-base font-semibold text-foreground">
              Two-factor authentication is on
            </h2>
            <p className="text-sm text-muted-foreground mt-1">
              {status.lastUsedAt
                ? `Last used ${formatRelativeTime(status.lastUsedAt)}.`
                : "Not used yet."}
            </p>
            <ActionButton
              onClick={onDisable}
              icon={<ShieldOff className="h-4 w-4" />}
              className="mt-4"
            >
              Disable 2FA
            </ActionButton>
          </div>
        </div>
      </Card>

      <Card padding="lg">
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0">
            <h3 className="text-base font-semibold text-foreground">
              Recovery codes
            </h3>
            <p className="text-sm text-muted-foreground mt-1">
              {status.recoveryCodesRemaining} of 10 remaining. Use them if you
              lose access to your authenticator.
            </p>
          </div>
          <ActionButton
            onClick={onRegenerate}
            icon={<RefreshCw className="h-4 w-4" />}
            className="shrink-0"
          >
            Regenerate
          </ActionButton>
        </div>
      </Card>
    </div>
  );
}
