/**
 * Account → Security page. Houses the TOTP enrollment / disable / recovery-codes
 * flow for the logged-in user.
 *
 * The flow has three steady states and a multi-step wizard for enrollment:
 *  - Not enrolled  → "Enable 2FA" launches the 3-step wizard.
 *  - Enrolled      → status banner + "Disable 2FA" (password + current code).
 *  - Wizard step 3 → recovery codes shown once; user must check "I've saved
 *                    these" before leaving the page (codes won't be re-shown).
 *
 * Backend endpoints are documented in lib/api/account-security.ts; this page
 * deliberately makes no decisions about TOTP secrets (the QR + otpauth URL
 * come down pre-rendered).
 */

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { PageHeader, PageShell } from "@/components/ui/page";
import { QueryStates } from "@/components/ui/query-states";
import { getTotpStatus, type TotpStatus } from "@/lib/api/account-security";
import { EnrolledCard, NotEnrolledCard } from "./-cards";
import { DisableDialog, RegenerateDialog } from "./-dialogs";
import { EnrollmentWizard } from "./-enrollment-wizard";

const TOTP_STATUS_KEY = ["account", "security", "totp", "status"] as const;

export function AccountSecurityPage() {
  const qc = useQueryClient();
  const statusQuery = useQuery<TotpStatus>({
    queryKey: TOTP_STATUS_KEY,
    queryFn: getTotpStatus,
  });

  const [dialog, setDialog] = useState<"wizard" | "disable" | "regen" | null>(
    null,
  );
  const status = statusQuery.data;

  const refresh = () => qc.invalidateQueries({ queryKey: TOTP_STATUS_KEY });

  return (
    <PageShell>
      <PageHeader
        title="Security"
        description="Two-factor authentication and recovery codes for your account."
      />

      <QueryStates query={statusQuery} permission="account:read">
        {(loadedStatus) =>
          loadedStatus.enrolled ? (
            <EnrolledCard
              status={loadedStatus}
              onDisable={() => setDialog("disable")}
              onRegenerate={() => setDialog("regen")}
            />
          ) : (
            <NotEnrolledCard onEnable={() => setDialog("wizard")} />
          )
        }
      </QueryStates>

      {dialog === "wizard" && (
        <EnrollmentWizard
          onClose={() => setDialog(null)}
          onDone={() => {
            setDialog(null);
            refresh();
          }}
        />
      )}

      {dialog === "disable" && status?.enrolled && (
        <DisableDialog
          onClose={() => setDialog(null)}
          onDone={() => {
            setDialog(null);
            refresh();
          }}
        />
      )}

      {dialog === "regen" && status?.enrolled && (
        <RegenerateDialog
          onClose={() => setDialog(null)}
          onDone={() => {
            setDialog(null);
            refresh();
          }}
        />
      )}
    </PageShell>
  );
}
