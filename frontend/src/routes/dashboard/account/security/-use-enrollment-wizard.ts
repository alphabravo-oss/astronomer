import { useEffect, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { toastApiError } from "@/lib/toast";
import {
  startTotpEnrollment,
  type TotpEnrollStart,
} from "@/lib/api/account-security";

export type WizardStep = "scan" | "verify" | "codes";

export function useEnrollmentWizard() {
  const [step, setStep] = useState<WizardStep>("scan");
  const [enrollment, setEnrollment] = useState<TotpEnrollStart | null>(null);
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null);
  const [acknowledged, setAcknowledged] = useState(false);

  const startMut = useMutation({
    mutationFn: () => startTotpEnrollment(),
    onSuccess: (data) => setEnrollment(data),
    onError: (err: Error) =>
      toastApiError("", err, "Failed to start enrollment"),
  });

  useEffect(() => {
    startMut.mutate();
    // start once on mount
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return {
    step,
    setStep,
    enrollment,
    recoveryCodes,
    setRecoveryCodes,
    acknowledged,
    setAcknowledged,
    startMut,
  };
}
