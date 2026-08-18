import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  CheckCircle2,
  Loader2,
  Shield,
  Unplug,
  Upload,
} from "lucide-react";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";
import { parseOnboardingFile } from "@/components/charlie/admin-utils";
import {
  consumeCharlieConnect,
  consumeCharlieOnboarding,
  disconnectCharlie,
  getCharlieActivation,
  getCharlieConnection,
  validateCharlieConnect,
  validateCharlieOnboarding,
  type CharlieOnboardingView,
} from "@/lib/api/charlie-admin";
import {
  Field,
  Meta,
  Section,
  Unavailable,
  button,
  emptyOnboarding,
  primary,
} from "./shared";

export function ConnectionTab({ localOnly = false }: { localOnly?: boolean } = {}) {
  const qc = useQueryClient();
  const activation = useQuery({
    queryKey: queryKeys.charlie.activation,
    queryFn: getCharlieActivation,
    enabled: !localOnly,
    retry: false,
    staleTime: 15_000,
  });
  const connection = useQuery({
    queryKey: queryKeys.charlie.adminConnection,
    queryFn: getCharlieConnection,
    enabled: localOnly,
    retry: false,
  });
  const [endpoint, setEndpoint] = useState("");
  const [connectToken, setConnectToken] = useState("");
  const [input, setInput] = useState(emptyOnboarding);
  const [fileName, setFileName] = useState("");
  const [fileError, setFileError] = useState("");
  const [validated, setValidated] = useState<CharlieOnboardingView>();
  const [confirm, setConfirm] = useState<"consume" | "disconnect" | null>(null);
  const [disclosure, setDisclosure] = useState(false);
  const useToken = connectToken.trim().length > 0;
  const connected = localOnly
    ? Boolean(connection.data?.connected)
    : activation.data?.activated === true;
  const loading = localOnly
    ? connection.isLoading && !connection.data
    : activation.isLoading && !activation.data;
  const failed = localOnly ? connection.isError : activation.isError;
  const retry = () => void (localOnly ? connection.refetch() : activation.refetch());
  const accepted = () => {
    setValidated(undefined);
    setEndpoint("");
    setConnectToken("");
    setInput(emptyOnboarding);
    setFileName("");
    setConfirm(null);
    setDisclosure(false);
    void qc.invalidateQueries({ queryKey: queryKeys.charlie.activation });
    void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminConnection });
    toastSuccess("Charlie connected");
  };
  const validate = useMutation({
    mutationFn: () =>
      useToken
        ? validateCharlieConnect({ endpoint, connectToken })
        : validateCharlieOnboarding(input),
    onSuccess: (value) => {
      setValidated(value);
      toastSuccess("Charlie connection validated locally");
    },
    onError: (e) => toastApiError("Charlie connection validation failed", e),
  });
  const consume = useMutation({
    mutationFn: () =>
      useToken
        ? consumeCharlieConnect({ endpoint, connectToken })
        : consumeCharlieOnboarding(input),
    onSuccess: accepted,
    onError: (e) => toastApiError("Charlie connect failed", e),
  });
  const disconnect = useMutation({
    mutationFn: disconnectCharlie,
    onSuccess: () => {
      setConfirm(null);
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.activation });
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminConnection });
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminMode });
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminAgent });
      toastSuccess("Charlie disconnected");
    },
    onError: (e) => toastApiError("Charlie disconnect failed", e),
  });
  const load = async (file: File | undefined) => {
    setFileError("");
    setValidated(undefined);
    if (!file) return;
    try {
      const raw = parseOnboardingFile(await file.text());
      const wrapper =
        raw.package &&
        typeof raw.package === "object" &&
        !Array.isArray(raw.package)
          ? raw
          : undefined;
      const string = (...keys: string[]) => {
        for (const k of keys) {
          const v = raw[k];
          if (typeof v === "string") return v;
        }
        return "";
      };
      setInput({
        package: (wrapper?.package as Record<string, unknown>) ?? raw,
        signingPublicKey: string("signing_public_key", "signingPublicKey"),
        confirmedSigningKeyId: string(
          "confirmed_signing_key_id",
          "confirmedSigningKeyId",
        ),
        confirmedSigningFingerprint: string(
          "confirmed_signing_fingerprint",
          "confirmedSigningFingerprint",
        ),
        expectedDeploymentId: string(
          "expected_deployment_id",
          "expectedDeploymentId",
        ),
        expectedRouteId: string("expected_route_id", "expectedRouteId"),
      });
      setFileName(file.name);
    } catch (e) {
      setFileName("");
      setInput(emptyOnboarding);
      setFileError(e instanceof Error ? e.message : "Invalid package");
    }
  };
  const complete = useToken
    ? Boolean(endpoint.trim() && connectToken.trim().startsWith("charlie.connect.v1."))
    : Object.keys(input.package).length > 0 &&
      input.signingPublicKey &&
      input.confirmedSigningKeyId &&
      /^[a-f0-9]{64}$/.test(input.confirmedSigningFingerprint) &&
      input.expectedDeploymentId &&
      input.expectedRouteId;

  if (loading) {
    return (
      <Section title="Charlie connection">
        <Loader2 className="h-5 w-5 animate-spin motion-reduce:animate-none" />
      </Section>
    );
  }
  if (failed) {
    return <Unavailable name="Charlie connection" retry={retry} />;
  }

  if (connected) {
    return (
      <>
        <Section
          title="Charlie is connected"
          description="This installation has one Charlie connection. Disconnect it before connecting to the same or another Charlie."
        >
          <dl>
            <Meta
              label="Charlie endpoint"
              value={
                (localOnly ? connection.data?.endpoint : activation.data?.endpoint) ||
                "—"
              }
            />
          </dl>
          <p className="rounded-lg border border-border bg-muted/20 p-3 text-sm text-muted-foreground">
            Charlie chat, findings, and the product agent stay available until you
            disconnect. Connecting a different Charlie is a new one-time token after
            disconnect.
          </p>
          <button
            onClick={() => setConfirm("disconnect")}
            disabled={disconnect.isPending}
            className={`${button} border-status-error text-status-error`}
          >
            <Unplug className="h-4 w-4" />
            Disconnect
          </button>
        </Section>
        <ConfirmDialog
          open={confirm === "disconnect"}
          onClose={() => setConfirm(null)}
          onConfirm={() => disconnect.mutate()}
          title="Disconnect Charlie"
          description="This deactivates the Charlie connection. Charlie navigation, findings, sessions, and the product agent stop. Emergency Disable is different — it only fails closed while keeping the connection. Reconnecting requires a new signed onboarding package."
          confirmText="Disconnect"
          confirmValue="DISCONNECT CHARLIE"
          variant="destructive"
          loading={disconnect.isPending}
        />
      </>
    );
  }

  if (localOnly) {
    return (
      <Section
        title="Charlie is not connected"
        description="Enable Charlie and paste a connect token to bind this installation."
      />
    );
  }

  return (
    <div className="max-w-3xl">
      <Section
        title="Connect Charlie"
        description="In Charlie, create a deployment connection. Paste the Charlie endpoint and one-time connect token here. Astronomer then installs the agent and turns the Charlie UI on."
      >
        <p className="rounded-lg border bg-muted/30 p-3 text-xs text-muted-foreground">
          Model providers, knowledge packs, and routes stay in Charlie. This
          token is not a durable Charlie API key.
        </p>
        <Field label="Charlie endpoint" value={endpoint} set={setEndpoint} />
        <label className="space-y-1 text-sm">
          <span className="block font-medium">Connect token</span>
          <textarea
            className="min-h-28 w-full rounded-md border border-border bg-background px-3 py-2 font-mono text-xs"
            value={connectToken}
            onChange={(e) => setConnectToken(e.target.value)}
            placeholder="charlie.connect.v1."
            spellCheck={false}
          />
        </label>
        <details className="rounded-lg border border-border p-3">
          <summary className="cursor-pointer text-sm font-medium">Air-gapped package file</summary>
          <div className="mt-3 space-y-3">
            <label className="flex cursor-pointer items-center justify-center gap-2 rounded-lg border border-dashed border-border p-5 text-sm hover:bg-accent">
              <Upload className="h-4 w-4" />
              <span>{fileName || "Choose JSON package"}</span>
              <input
                type="file"
                accept="application/json,.json"
                className="sr-only"
                aria-describedby="charlie-package-safety"
                onChange={(e) => void load(e.target.files?.[0])}
              />
            </label>
            <p
              id="charlie-package-safety"
              className="text-xs text-muted-foreground"
            >
              The selected file name is visible; credential values and package
              content are never added to the page.
            </p>
            {fileError && (
              <p role="alert" className="text-sm text-status-error">
                {fileError}
              </p>
            )}
            <div className="grid gap-3 sm:grid-cols-2">
              <Field
                label="Signing public key"
                value={input.signingPublicKey}
                set={(v) => setInput((x) => ({ ...x, signingPublicKey: v }))}
              />
              <Field
                label="Signing key ID"
                value={input.confirmedSigningKeyId}
                set={(v) => setInput((x) => ({ ...x, confirmedSigningKeyId: v }))}
              />
              <Field
                label="Confirmed SHA-256 fingerprint"
                value={input.confirmedSigningFingerprint}
                set={(v) =>
                  setInput((x) => ({ ...x, confirmedSigningFingerprint: v }))
                }
              />
              <Field
                label="Expected deployment ID"
                value={input.expectedDeploymentId}
                set={(v) => setInput((x) => ({ ...x, expectedDeploymentId: v }))}
              />
              <Field
                label="Expected route ID"
                value={input.expectedRouteId}
                set={(v) => setInput((x) => ({ ...x, expectedRouteId: v }))}
              />
            </div>
          </div>
        </details>
        <button
          disabled={!complete || validate.isPending}
          onClick={() => validate.mutate()}
          className={primary}
        >
          {validate.isPending ? (
            <Loader2 className="h-4 w-4 animate-spin motion-reduce:animate-none" />
          ) : (
            <Shield className="h-4 w-4" />
          )}
          {useToken ? "Validate connection" : "Validate signature locally"}
        </button>
        {validated && (
          <div
            role="status"
            className="space-y-3 rounded-lg border border-status-success/30 bg-status-success/5 p-4"
          >
            <div className="flex items-center gap-2 text-sm font-medium">
              <CheckCircle2 className="h-4 w-4 text-status-success" />
              Validated package
            </div>
            <dl className="grid gap-3 sm:grid-cols-2">
              <Meta label="Package ID" value={validated.packageId} />
              <Meta label="Product" value={validated.productId} />
              <Meta label="Deployment" value={validated.deploymentId} />
              <Meta label="Logical agent" value={validated.logicalAgentId} />
              <Meta label="Route" value={validated.routeId} />
              <Meta
                label="Allowed routes"
                value={validated.allowedRouteIds.join(", ")}
              />
              <Meta label="Package schema" value={validated.schema} />
              <Meta
                label="Central API version"
                value={validated.centralApiVersion}
              />
              <Meta label="Replicas" value={validated.replicaCount} />
              <Meta label="Issued" value={validated.issuedAt} />
              <Meta label="Expires" value={validated.expiresAt} />
              <Meta label="Signing key ID" value={validated.signingKeyId} />
              <Meta
                label="Signing fingerprint"
                value={validated.signingFingerprint}
              />
              <Meta label="Package digest" value={validated.packageDigest} />
              <Meta
                label="Central trust fingerprint"
                value={validated.centralTrustFingerprint}
              />
              <Meta label="Agent image" value={validated.artifact.image} />
              <Meta
                label="Image manifest digest"
                value={validated.artifact.manifestDigest}
              />
              <Meta label="Agent chart" value={validated.artifact.chart} />
              <Meta
                label="Chart digest"
                value={validated.artifact.chartDigest}
              />
              <Meta label="Validation state" value={validated.state} />
            </dl>
            <label className="flex items-start gap-2 text-xs">
              <input
                type="checkbox"
                checked={disclosure}
                onChange={(e) => setDisclosure(e.target.checked)}
                className="mt-0.5"
              />
              <span>
                I reviewed the package source, expected deployment and route,
                signing fingerprint, and understand that changed capabilities or
                authority require a new disclosure acknowledgement.
              </span>
            </label>
            <button
              disabled={!disclosure}
              onClick={() => setConfirm("consume")}
              className={primary}
            >
              Connect Charlie
            </button>
          </div>
        )}
      </Section>
      <ConfirmDialog
        open={confirm === "consume"}
        onClose={() => setConfirm(null)}
        onConfirm={() => consume.mutate()}
        title="Connect Charlie"
        description="This consumes the one-time Charlie connection and installs the product agent. Existing authority is never expanded by the browser."
        confirmText="Connect"
        confirmValue={`CONNECT ${validated?.deploymentId ?? ""}`}
        loading={consume.isPending}
      />
    </div>
  );
}
