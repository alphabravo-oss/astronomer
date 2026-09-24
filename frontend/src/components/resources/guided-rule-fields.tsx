import { ArrayRows, PathField } from "./guided-array-fields";
import type { GuidedSectionProps } from "./guided-resource-fields";
import {
  IngressBackendFields,
  IngressDefaultBackend,
} from "./guided-ingress-backend";

export function RoleRulesSection({ form }: GuidedSectionProps) {
  if (!["Role", "ClusterRole"].includes(form.kind)) return null;
  return (
    <ArrayRows
      form={form}
      path={["rules"]}
      label="RBAC rules"
      create={() => ({ apiGroups: [""], resources: [], verbs: ["get"] })}
    >
      {(path) => (
        <>
          <PathField
            form={form}
            path={[...path, "apiGroups"]}
            label="API groups"
            type="csv"
          />
          <PathField
            form={form}
            path={[...path, "resources"]}
            label="Resources"
            type="csv"
          />
          <PathField
            form={form}
            path={[...path, "verbs"]}
            label="Verbs"
            type="csv"
          />
          <PathField
            form={form}
            path={[...path, "resourceNames"]}
            label="Resource names (optional)"
            type="csv"
          />
        </>
      )}
    </ArrayRows>
  );
}

export function IngressRulesSection({ form }: GuidedSectionProps) {
  if (form.kind !== "Ingress") return null;
  const newPath = () => ({
    path: "/",
    pathType: "Prefix",
    backend: { service: { name: "", port: { number: 80 } } },
  });
  return (
    <>
      <IngressDefaultBackend form={form} />
      <ArrayRows
        form={form}
        path={["spec", "rules"]}
        label="Ingress rules"
        create={() => ({ host: "", http: { paths: [newPath()] } })}
      >
        {(rule) => (
          <>
            <PathField form={form} path={[...rule, "host"]} label="Host" />
            <ArrayRows
              form={form}
              path={[...rule, "http", "paths"]}
              label="Ingress paths"
              create={newPath}
            >
              {(path) => (
                <>
                  <PathField
                    form={form}
                    path={[...path, "path"]}
                    label="Path"
                  />
                  <PathField
                    form={form}
                    path={[...path, "pathType"]}
                    label="Path type"
                    options={["Prefix", "Exact", "ImplementationSpecific"]}
                  />
                  <IngressBackendFields
                    form={form}
                    path={[...path, "backend"]}
                  />
                </>
              )}
            </ArrayRows>
          </>
        )}
      </ArrayRows>
    </>
  );
}
