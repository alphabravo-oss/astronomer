import { useMemo, useState } from "react";
import {
  Check,
  Copy,
  Download,
  Eye,
  EyeOff,
  FileKey2,
  LockKeyhole,
} from "lucide-react";

import type { K8sObject } from "@/components/resources/resource-detail-model";
import {
  KeyValueTable,
  Section,
} from "@/components/resources/resource-overview-primitives";
import { useClusterResourcePermission } from "@/lib/permission-hooks";
import { cn, copyToClipboard, formatBytes } from "@/lib/utils";

interface SecretDataOverviewProps {
  obj: K8sObject;
  clusterId: string;
}

interface DecodedSecretValue {
  bytes: Uint8Array;
  text?: string;
  display?: string;
  kind: "JSON" | "PEM" | "Text" | "Binary";
  mimeType: string;
  extension: string;
}

function base64Bytes(value: string): Uint8Array {
  const normalized = value.replace(/\s/g, "");
  const binary = atob(normalized);
  return Uint8Array.from(binary, (character) => character.charCodeAt(0));
}

function utf8(bytes: Uint8Array): string | undefined {
  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch {
    return undefined;
  }
}

function looksLikeBase64(value: string): boolean {
  const normalized = value.replace(/\s/g, "");
  return (
    normalized.length >= 16 &&
    normalized.length % 4 === 0 &&
    /^[A-Za-z0-9+/]+={0,2}$/.test(normalized)
  );
}

function normalizeDockerConfig(value: unknown): unknown {
  if (!value || typeof value !== "object") return value;
  const document = structuredClone(value) as {
    auths?: Record<
      string,
      { auth?: string; username?: string; password?: string }
    >;
  };
  for (const auth of Object.values(document.auths ?? {})) {
    if ((!auth.username || !auth.password) && auth.auth) {
      try {
        const decoded = utf8(base64Bytes(auth.auth)) ?? "";
        const delimiter = decoded.indexOf(":");
        if (delimiter >= 0) {
          auth.username = decoded.slice(0, delimiter);
          auth.password = decoded.slice(delimiter + 1);
        }
      } catch {
        // Preserve the source document when a registry emitted malformed auth.
      }
    }
    delete auth.auth;
  }
  return document;
}

export function decodeSecretValue(
  encoded: string,
  key: string,
  secretType: string,
): DecodedSecretValue {
  let bytes: Uint8Array;
  try {
    bytes = base64Bytes(encoded);
  } catch {
    return {
      bytes: new Uint8Array(),
      kind: "Binary",
      mimeType: "application/octet-stream",
      extension: "bin",
    };
  }
  let text = utf8(bytes);

  // Helm stores a second encoded release payload inside Secret.data. Decode
  // that layer too; if it is compressed/protobuf, present the actual bytes as
  // a downloadable binary instead of dumping another base64 string.
  if (
    secretType.startsWith("helm.sh/release") &&
    text &&
    looksLikeBase64(text)
  ) {
    try {
      bytes = base64Bytes(text);
      text = utf8(bytes);
    } catch {
      // The first decoded layer remains the best available representation.
    }
  }

  if (text !== undefined) {
    const trimmed = text.trim();
    if (trimmed.startsWith("-----BEGIN ") && trimmed.includes("-----END ")) {
      return {
        bytes,
        text,
        display: text,
        kind: "PEM",
        mimeType: "application/x-pem-file",
        extension: key.includes("key") ? "key" : "pem",
      };
    }
    if (trimmed.startsWith("{") || trimmed.startsWith("[")) {
      try {
        let parsed: unknown = JSON.parse(text);
        if (
          key === ".dockerconfigjson" ||
          key === ".dockercfg" ||
          secretType === "kubernetes.io/dockerconfigjson"
        ) {
          parsed = normalizeDockerConfig(parsed);
        }
        const display = JSON.stringify(parsed, null, 2);
        return {
          bytes: new TextEncoder().encode(display),
          text: display,
          display,
          kind: "JSON",
          mimeType: "application/json",
          extension: "json",
        };
      } catch {
        // Valid UTF-8 that only resembles JSON should remain ordinary text.
      }
    }
    return {
      bytes,
      text,
      display: text,
      kind: "Text",
      mimeType: "text/plain;charset=utf-8",
      extension: "txt",
    };
  }
  return {
    bytes,
    kind: "Binary",
    mimeType: "application/octet-stream",
    extension: "bin",
  };
}

function downloadValue(name: string, key: string, decoded: DecodedSecretValue) {
  const safeKey = key.replace(/[^a-zA-Z0-9._-]+/g, "-");
  const blob = new Blob([decoded.bytes as BlobPart], {
    type: decoded.mimeType,
  });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = `${name}-${safeKey}.${decoded.extension}`;
  anchor.click();
  URL.revokeObjectURL(url);
}

function SecretValuePanel({
  secretName,
  secretType,
  name,
  encoded,
}: {
  secretName: string;
  secretType: string;
  name: string;
  encoded: string;
}) {
  const [revealed, setRevealed] = useState(false);
  const [copied, setCopied] = useState(false);
  const decoded = useMemo(
    () => decodeSecretValue(encoded, name, secretType),
    [encoded, name, secretType],
  );
  const copy = async () => {
    if (!decoded.text) return;
    if (await copyToClipboard(decoded.text)) {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    }
  };

  return (
    <article className="overflow-hidden rounded-lg border border-border bg-card shadow-sm">
      <header className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-4 py-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="break-all font-mono text-sm font-semibold text-foreground">
              {name}
            </h3>
            <span className="rounded-full bg-muted px-2 py-0.5 text-2xs font-medium text-muted-foreground">
              {decoded.kind}
            </span>
            <span className="text-2xs tabular-nums text-muted-foreground">
              {formatBytes(decoded.bytes.byteLength)}
            </span>
          </div>
        </div>
        <div className="flex items-center gap-1.5">
          <button
            type="button"
            onClick={() => setRevealed((value) => !value)}
            aria-label={revealed ? `Hide ${name}` : `Show ${name}`}
            aria-pressed={revealed}
            className={cn(
              "inline-flex h-8 items-center gap-1.5 rounded-md border px-2.5 text-xs transition-colors",
              revealed
                ? "border-primary/40 bg-primary/10 text-primary"
                : "border-border text-muted-foreground hover:bg-accent hover:text-foreground",
            )}
          >
            {revealed ? (
              <EyeOff className="h-3.5 w-3.5" />
            ) : (
              <Eye className="h-3.5 w-3.5" />
            )}
            {revealed ? "Hide" : "Show"}
          </button>
          <button
            type="button"
            onClick={() => void copy()}
            disabled={!revealed || !decoded.text}
            aria-label={`Copy ${name}`}
            className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border px-2.5 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:cursor-not-allowed disabled:opacity-40"
          >
            {copied ? (
              <Check className="h-3.5 w-3.5 text-status-success" />
            ) : (
              <Copy className="h-3.5 w-3.5" />
            )}
            {copied ? "Copied" : "Copy"}
          </button>
          <button
            type="button"
            onClick={() => downloadValue(secretName, name, decoded)}
            disabled={!revealed}
            aria-label={`Download ${name}`}
            className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border px-2.5 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:cursor-not-allowed disabled:opacity-40"
          >
            <Download className="h-3.5 w-3.5" /> Download
          </button>
        </div>
      </header>
      <div className="relative min-h-40 bg-muted/15">
        {revealed ? (
          decoded.display !== undefined ? (
            <pre
              className="max-h-[32rem] min-h-40 overflow-auto whitespace-pre-wrap break-words p-4 font-mono text-xs leading-5 text-foreground"
              aria-live="polite"
            >
              {decoded.display}
            </pre>
          ) : (
            <div className="flex min-h-40 flex-col items-center justify-center gap-2 px-6 py-8 text-center">
              <FileKey2 className="h-7 w-7 text-muted-foreground" />
              <p className="text-sm font-medium text-foreground">
                Binary secret data
              </p>
              <p className="max-w-md text-xs text-muted-foreground">
                This value is not valid UTF-8. Download opens the actual decoded{" "}
                bytes; Astronomer will not replace them with base64 text.
              </p>
            </div>
          )
        ) : (
          <div className="flex min-h-40 flex-col items-center justify-center gap-3 px-6 py-8 text-center">
            <div className="flex h-10 w-10 items-center justify-center rounded-full bg-muted">
              <LockKeyhole className="h-5 w-5 text-muted-foreground" />
            </div>
            <div>
              <p className="font-mono text-base tracking-[0.35em] text-muted-foreground">
                ••••••••
              </p>
              <p className="mt-2 text-xs text-muted-foreground">
                Masked by default. Reveal is available only to principals with{" "}
                secrets:read on this cluster.
              </p>
            </div>
          </div>
        )}
      </div>
    </article>
  );
}

export function SecretDataOverview({
  obj,
  clusterId,
}: SecretDataOverviewProps) {
  const permission = useClusterResourcePermission(clusterId, "secrets", "read");
  const secretType = obj.type ?? "Opaque";
  const values = Object.entries(obj.data ?? {}).sort(([left], [right]) =>
    left.localeCompare(right),
  );

  if (!permission.allowed) {
    return (
      <div className="rounded-lg border border-status-error/30 bg-status-error/5 px-4 py-5">
        <div className="flex items-center gap-2 text-sm font-semibold text-status-error">
          <LockKeyhole className="h-4 w-4" /> Secret values are restricted
        </div>
        <p className="mt-2 text-xs text-muted-foreground">
          Your current role does not grant secrets:read for this cluster.
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <Section title="Secret">
        <KeyValueTable
          entries={[
            ["type", secretType],
            ["keys", String(values.length)],
            ["display", "Decoded values · masked by default"],
          ]}
        />
      </Section>
      <Section title={`Data (${values.length})`}>
        {values.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            This Secret contains no data values.
          </p>
        ) : (
          <div className="space-y-4">
            {values.map(([name, encoded]) => (
              <SecretValuePanel
                key={name}
                secretName={obj.metadata?.name ?? "secret"}
                secretType={secretType}
                name={name}
                encoded={encoded}
              />
            ))}
          </div>
        )}
      </Section>
    </div>
  );
}
