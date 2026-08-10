import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  CheckCircle2,
  Loader2,
  Shield,
  Trash2,
  Upload,
} from "lucide-react";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";
import { parseOnboardingFile } from "@/components/charlie/admin-utils";
import {
  consumeCharlieOnboarding,
  disconnectCharlie,
  getCharlieConnection,
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
  const connection = useQuery({
    queryKey: queryKeys.charlie.adminConnection,
    queryFn: getCharlieConnection,
    retry: false,
  });
  const [input, setInput] = useState(emptyOnboarding);
  const [fileName, setFileName] = useState("");
  const [fileError, setFileError] = useState("");
  const [validated, setValidated] = useState<CharlieOnboardingView>();
  const [confirm, setConfirm] = useState<"consume" | "disconnect" | null>(null);
  const [disclosure, setDisclosure] = useState(false);
  const validate = useMutation({
    mutationFn: validateCharlieOnboarding,
    onSuccess: (value) => {
      setValidated(value);
      toastSuccess("Charlie package signature validated locally");
    },
    onError: (e) => toastApiError("Package validation failed", e),
  });
  const consume = useMutation({
    mutationFn: consumeCharlieOnboarding,
    onSuccess: () => {
      setValidated(undefined);
      setInput(emptyOnboarding);
      setFileName("");
      setConfirm(null);
      setDisclosure(false);
      void qc.invalidateQueries({
        queryKey: queryKeys.charlie.adminConnection,
      });
      toastSuccess("Charlie connection and product agent onboarding started");
    },
    onError: (e) => toastApiError("Charlie onboarding failed", e),
  });
  const disconnect = useMutation({
    mutationFn: disconnectCharlie,
    onSuccess: () => {
      setConfirm(null);
      void qc.invalidateQueries({
        queryKey: queryKeys.charlie.adminConnection,
      });
      toastSuccess("Charlie disconnected");
    },
    onError: (e) => toastApiError("Disconnect failed", e),
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
  const complete =
    Object.keys(input.package).length > 0 &&
    input.signingPublicKey &&
    input.confirmedSigningKeyId &&
    /^[a-f0-9]{64}$/.test(input.confirmedSigningFingerprint) &&
    input.expectedDeploymentId &&
    input.expectedRouteId;
  const confirmValue = connection.data?.connected
    ? `REPLACE ${validated?.deploymentId ?? ""}`
    : `CONNECT ${validated?.deploymentId ?? ""}`;
  return (
    <div className="grid gap-4 xl:grid-cols-2">
      <Section
        title="Current connection"
        description="Only non-secret identifiers and digests are displayed. Credentials and private keys are never returned."
      >
        {connection.isLoading ? (
          <Loader2 className="h-5 w-5 animate-spin motion-reduce:animate-none" />
        ) : connection.isError ? (
          <Unavailable
            name="Connection status"
            retry={() => void connection.refetch()}
          />
        ) : (
          <dl className="grid grid-cols-2 gap-4">
            <Meta
              label="Status"
              value={connection.data?.connected ? "Connected" : "Not connected"}
            />
            <Meta label="Product" value={connection.data?.productId} />
            <Meta label="Deployment" value={connection.data?.deploymentId} />
            <Meta label="Route" value={connection.data?.routeId} />
            <Meta
              label="Central version"
              value={connection.data?.centralVersion}
            />
            <Meta
              label="Signing key ID"
              value={connection.data?.signingKeyId}
            />
            <Meta
              label="Signing fingerprint"
              value={connection.data?.signingFingerprint}
            />
            <Meta
              label="Package digest"
              value={connection.data?.packageDigest}
            />
            <Meta
              label="Disclosure digest"
              value={connection.data?.disclosureDigest}
            />
            <Meta
              label="Disclosure acknowledged"
              value={connection.data?.disclosureAcknowledged ? "Yes" : "No"}
            />
          </dl>
        )}
        {connection.data?.connected && (
          <button
            onClick={() => setConfirm("disconnect")}
            className={`${button} mt-4 text-status-error`}
          >
            <Trash2 className="h-4 w-4" />
            Disconnect
          </button>
        )}
      </Section>
      {!localOnly && <Section
        title="Connect or replace Charlie"
        description="Upload a signed onboarding package. It is held only in memory and sent to local validation; package contents are never rendered or persisted by the browser."
      >
        <p className="rounded-lg border bg-muted/30 p-3 text-xs text-muted-foreground">
          Model providers, LLM routing, RAG, knowledge sources, and agentic
          workflows remain administered in the separate Charlie service.
          Astronomer stores only the product trust, route, mode, policy, and
          product-agent integration needed to expose authorized capabilities.
        </p>
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
        <button
          disabled={!complete || validate.isPending}
          onClick={() => validate.mutate(input)}
          className={primary}
        >
          {validate.isPending ? (
            <Loader2 className="h-4 w-4 animate-spin motion-reduce:animate-none" />
          ) : (
            <Shield className="h-4 w-4" />
          )}
          Validate signature locally
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
              {connection.data?.connected
                ? "Replace connection"
                : "Connect Charlie"}
            </button>
          </div>
        )}
      </Section>}
      <ConfirmDialog
        open={confirm === "consume"}
        onClose={() => setConfirm(null)}
        onConfirm={() => consume.mutate(input)}
        title={
          connection.data?.connected
            ? "Replace Charlie connection"
            : "Connect Charlie"
        }
        description="This consumes the one-time package and installs or reconciles the separate Charlie product agent. Existing authority is never expanded by the browser."
        confirmText={
          connection.data?.connected ? "Replace connection" : "Connect"
        }
        confirmValue={confirmValue}
        loading={consume.isPending}
      />
      <ConfirmDialog
        open={confirm === "disconnect"}
        onClose={() => setConfirm(null)}
        onConfirm={() => disconnect.mutate()}
        title="Disconnect Charlie"
        description="This stops the Charlie integration while preserving Astronomer audit records."
        confirmText="Disconnect"
        confirmValue="DISCONNECT CHARLIE"
        variant="destructive"
        loading={disconnect.isPending}
      />
    </div>
  );
}
