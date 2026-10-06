import type {
  ContainerSpec,
  ContainerStateDetail,
  ContainerStatus,
  K8sObject,
} from "@/components/resources/resource-detail-model";

export interface ContainerRow {
  spec: ContainerSpec;
  status?: ContainerStatus;
  kind: "Application" | "Init" | "Ephemeral";
}

export interface Diagnostic {
  title: string;
  message?: string;
  tone: "healthy" | "warning" | "error";
}

export function stateEntry(status?: ContainerStatus): {
  state: string;
  detail?: ContainerStateDetail;
} {
  if (!status?.state) return { state: "Unknown" };
  for (const name of ["waiting", "terminated", "running"]) {
    const detail = status.state[name];
    if (detail) {
      return {
        state: name.charAt(0).toUpperCase() + name.slice(1),
        detail,
      };
    }
  }
  return { state: "Unknown" };
}

export function lastTermination(
  status?: ContainerStatus,
): ContainerStateDetail | undefined {
  return status?.lastState?.terminated;
}

export function diagnosticForPod(obj: K8sObject): Diagnostic {
  const status = obj.status ?? {};
  const allStatuses = [
    ...(status.initContainerStatuses ?? []),
    ...(status.containerStatuses ?? []),
    ...(status.ephemeralContainerStatuses ?? []),
  ];

  for (const container of allStatuses) {
    const current = stateEntry(container);
    if (current.state === "Waiting" && current.detail?.reason) {
      return {
        title: `${container.name ?? "Container"}: ${current.detail.reason}`,
        message: current.detail.message,
        tone:
          current.detail.reason === "CrashLoopBackOff" ||
          current.detail.reason === "ImagePullBackOff" ||
          current.detail.reason === "ErrImagePull"
            ? "error"
            : "warning",
      };
    }
    if (
      current.state === "Terminated" &&
      (current.detail?.exitCode ?? 0) !== 0
    ) {
      return {
        title: `${container.name ?? "Container"}: ${current.detail?.reason || `Exited ${current.detail?.exitCode}`}`,
        message: current.detail?.message,
        tone: "error",
      };
    }
    const previous = lastTermination(container);
    if (
      (container.restartCount ?? 0) > 0 &&
      (previous?.reason === "OOMKilled" || (previous?.exitCode ?? 0) !== 0)
    ) {
      return {
        title: `${container.name ?? "Container"} restarted after ${previous?.reason || `exit ${previous?.exitCode}`}`,
        message: previous?.message,
        tone: previous?.reason === "OOMKilled" ? "error" : "warning",
      };
    }
  }

  const importantConditions = new Set([
    "PodScheduled",
    "Initialized",
    "ContainersReady",
    "Ready",
  ]);
  const failedCondition = (status.conditions ?? []).find(
    (condition) =>
      importantConditions.has(String(condition.type ?? "")) &&
      String(condition.status ?? "") !== "True",
  );
  if (failedCondition) {
    return {
      title: String(
        failedCondition.reason || failedCondition.type || "Pod is not ready",
      ),
      message: String(failedCondition.message || ""),
      tone:
        String(failedCondition.type) === "PodScheduled" ? "error" : "warning",
    };
  }
  if (status.reason || status.message) {
    return {
      title: status.reason || status.phase || "Pod needs attention",
      message: status.message,
      tone: status.phase === "Failed" ? "error" : "warning",
    };
  }
  if (status.phase !== "Running" && status.phase !== "Succeeded") {
    return {
      title: `Pod is ${status.phase || "not ready"}`,
      tone: status.phase === "Failed" ? "error" : "warning",
    };
  }
  return {
    title:
      status.phase === "Succeeded"
        ? "Pod completed successfully"
        : "Pod is healthy and ready",
    tone: "healthy",
  };
}

function statusByName(statuses: ContainerStatus[] | undefined) {
  return new Map((statuses ?? []).map((status) => [status.name ?? "", status]));
}

export function containerRows(obj: K8sObject): ContainerRow[] {
  const spec = obj.spec ?? {};
  const status = obj.status ?? {};
  const appStatuses = statusByName(status.containerStatuses);
  const initStatuses = statusByName(status.initContainerStatuses);
  const ephemeralStatuses = statusByName(status.ephemeralContainerStatuses);
  return [
    ...(spec.initContainers ?? []).map((container) => ({
      spec: container,
      status: initStatuses.get(container.name ?? ""),
      kind: "Init" as const,
    })),
    ...(spec.containers ?? []).map((container) => ({
      spec: container,
      status: appStatuses.get(container.name ?? ""),
      kind: "Application" as const,
    })),
    ...(spec.ephemeralContainers ?? []).map((container) => ({
      spec: container,
      status: ephemeralStatuses.get(container.name ?? ""),
      kind: "Ephemeral" as const,
    })),
  ];
}

export function probeSummary(
  probe?: Record<string, unknown>,
): string | undefined {
  if (!probe) return undefined;
  const handler = ["httpGet", "tcpSocket", "grpc", "exec"].find(
    (key) => probe[key],
  );
  const timing = [
    probe.initialDelaySeconds != null
      ? `delay ${String(probe.initialDelaySeconds)}s`
      : "",
    probe.periodSeconds != null ? `every ${String(probe.periodSeconds)}s` : "",
    probe.timeoutSeconds != null
      ? `timeout ${String(probe.timeoutSeconds)}s`
      : "",
  ].filter(Boolean);
  return [handler || "configured", ...timing].join(" · ");
}

export function environmentSummary(container: ContainerSpec): string[] {
  const rows: string[] = [];
  for (const env of container.env ?? []) {
    if (!env.name) continue;
    const source = env.valueFrom ?? {};
    const secret = source.secretKeyRef as
      { name?: string; key?: string } | undefined;
    const config = source.configMapKeyRef as
      { name?: string; key?: string } | undefined;
    const field = source.fieldRef as { fieldPath?: string } | undefined;
    const resource = source.resourceFieldRef as
      { resource?: string } | undefined;
    if (secret) {
      rows.push(
        `${env.name} ← Secret ${secret.name ?? "?"}/${secret.key ?? "?"}`,
      );
    } else if (config) {
      rows.push(
        `${env.name} ← ConfigMap ${config.name ?? "?"}/${config.key ?? "?"}`,
      );
    } else if (field) {
      rows.push(`${env.name} ← ${field.fieldPath ?? "pod field"}`);
    } else if (resource) {
      rows.push(`${env.name} ← ${resource.resource ?? "container resource"}`);
    } else {
      rows.push(`${env.name}=${env.value ?? ""}`);
    }
  }
  for (const source of container.envFrom ?? []) {
    const secret = source.secretRef as { name?: string } | undefined;
    const config = source.configMapRef as { name?: string } | undefined;
    if (secret) rows.push(`All keys from Secret ${secret.name ?? "?"}`);
    if (config) rows.push(`All keys from ConfigMap ${config.name ?? "?"}`);
  }
  return rows;
}

export function referenceRows(obj: K8sObject) {
  const spec = obj.spec ?? {};
  const refs: Array<{
    type: string;
    resourceType: string;
    name: string;
    detail: string;
    secret?: boolean;
  }> = [];
  const add = (
    type: string,
    resourceType: string,
    name: unknown,
    detail: string,
    secret = false,
  ) => {
    if (typeof name !== "string" || !name) return;
    if (
      refs.some((ref) => ref.resourceType === resourceType && ref.name === name)
    )
      return;
    refs.push({ type, resourceType, name, detail, secret });
  };

  add("Node", "nodes", spec.nodeName, "Scheduling target");
  add(
    "Service account",
    "serviceaccounts",
    spec.serviceAccountName,
    "Pod identity",
  );
  for (const pullSecret of spec.imagePullSecrets ?? []) {
    add("Secret", "secrets", pullSecret.name, "Image pull credentials", true);
  }
  for (const volume of spec.volumes ?? []) {
    const pvc = volume.persistentVolumeClaim as
      { claimName?: string } | undefined;
    const secret = volume.secret as { secretName?: string } | undefined;
    const config = volume.configMap as { name?: string } | undefined;
    add(
      "PersistentVolumeClaim",
      "persistentvolumeclaims",
      pvc?.claimName,
      `Volume ${volume.name ?? ""}`,
    );
    add("ConfigMap", "configmaps", config?.name, `Volume ${volume.name ?? ""}`);
    add(
      "Secret",
      "secrets",
      secret?.secretName,
      `Volume ${volume.name ?? ""}`,
      true,
    );
    const projected = volume.projected as
      { sources?: Array<Record<string, unknown>> } | undefined;
    for (const source of projected?.sources ?? []) {
      const projectedSecret = source.secret as { name?: string } | undefined;
      const projectedConfig = source.configMap as { name?: string } | undefined;
      add(
        "Secret",
        "secrets",
        projectedSecret?.name,
        `Projected volume ${volume.name ?? ""}`,
        true,
      );
      add(
        "ConfigMap",
        "configmaps",
        projectedConfig?.name,
        `Projected volume ${volume.name ?? ""}`,
      );
    }
  }
  for (const container of [
    ...(spec.initContainers ?? []),
    ...(spec.containers ?? []),
    ...(spec.ephemeralContainers ?? []),
  ]) {
    for (const env of container.env ?? []) {
      const secret = env.valueFrom?.secretKeyRef as
        { name?: string } | undefined;
      const config = env.valueFrom?.configMapKeyRef as
        { name?: string } | undefined;
      add(
        "Secret",
        "secrets",
        secret?.name,
        `Environment · ${container.name}`,
        true,
      );
      add(
        "ConfigMap",
        "configmaps",
        config?.name,
        `Environment · ${container.name}`,
      );
    }
    for (const source of container.envFrom ?? []) {
      const secret = source.secretRef as { name?: string } | undefined;
      const config = source.configMapRef as { name?: string } | undefined;
      add(
        "Secret",
        "secrets",
        secret?.name,
        `Environment · ${container.name}`,
        true,
      );
      add(
        "ConfigMap",
        "configmaps",
        config?.name,
        `Environment · ${container.name}`,
      );
    }
  }
  return refs;
}
