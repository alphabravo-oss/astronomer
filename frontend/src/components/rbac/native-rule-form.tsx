import { useState } from "react";
import { Field } from "@/components/form/fields";
import { Input } from "@/components/ui/input";
import { ActionButton } from "@/components/ui/action-button";
import { ModalShell } from "@/components/ui/modal-shell";
import { RemoteClusterPicker } from "@/components/clusters/remote-cluster-picker";
import type {
  CreateNativeRuleRequest,
  NativeRuleVerb,
} from "@/lib/api/native-rbac";
import { nativeRuleError } from "./native-rule-model";

const verbs: NativeRuleVerb[] = [
  "read",
  "list",
  "watch",
  "create",
  "update",
  "delete",
  "*",
];
export function NativeRuleForm({
  onReview,
  onClose,
}: {
  onReview: (value: CreateNativeRuleRequest) => void;
  onClose: () => void;
}) {
  const [value, setValue] = useState<CreateNativeRuleRequest>({
    userId: "",
    clusterId: "",
    namespace: "",
    apiGroup: "",
    resource: "",
    verbs: ["read"],
  });
  const [allClusters, setAllClusters] = useState(false);
  const error =
    nativeRuleError(value) ||
    (!allClusters && !value.clusterId
      ? "Select a cluster or explicitly grant all clusters."
      : undefined);
  return (
    <ModalShell
      title="New native resource grant"
      onClose={onClose}
      footer={
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            disabled={!!error}
            onClick={() =>
              onReview({
                ...value,
                clusterId: allClusters ? undefined : value.clusterId,
              })
            }
          >
            Review grant
          </ActionButton>
        </>
      }
    >
      <p className="text-sm text-muted-foreground">
        Native grants add access; they do not restrict existing roles. The API
        rejects grants beyond your own authority. Use an explicit user UUID;
        global user enumeration is not required.
      </p>
      <div className="space-y-4">
        <Field label="User UUID">
          <Input
            value={value.userId}
            onChange={(event) =>
              setValue({ ...value, userId: event.target.value.trim() })
            }
          />
        </Field>
        <Field label="Cluster">
          <RemoteClusterPicker
            value={value.clusterId ?? ""}
            disabled={allClusters}
            onChange={(clusterId) => setValue({ ...value, clusterId })}
          />
        </Field>
        <label className="flex gap-2">
          <Input
            type="checkbox"
            checked={allClusters}
            onChange={(event) => {
              setAllClusters(event.target.checked);
              setValue({ ...value, clusterId: "" });
            }}
          />
          All clusters (including future clusters)
        </label>
        <Field
          label="Namespace"
          description="Empty grants every namespace in the selected cluster scope."
        >
          <Input
            value={value.namespace}
            onChange={(event) =>
              setValue({ ...value, namespace: event.target.value.trim() })
            }
          />
        </Field>
        <Field
          label="API group"
          description="Empty means the core API group. An asterisk means all groups permitted by the server."
        >
          <Input
            value={value.apiGroup}
            onChange={(event) =>
              setValue({ ...value, apiGroup: event.target.value.trim() })
            }
          />
        </Field>
        <Field label="Plural resource">
          <Input
            value={value.resource}
            onChange={(event) =>
              setValue({ ...value, resource: event.target.value.trim() })
            }
          />
        </Field>
        <fieldset>
          <legend>Allowed verbs</legend>
          <div className="flex flex-wrap gap-3">
            {verbs.map((verb) => (
              <label className="flex gap-1" key={verb}>
                <Input
                  type="checkbox"
                  checked={value.verbs.includes(verb)}
                  onChange={(event) =>
                    setValue({
                      ...value,
                      verbs: event.target.checked
                        ? [...value.verbs, verb]
                        : value.verbs.filter((item) => item !== verb),
                    })
                  }
                />
                {verb === "*" ? "All allowed verbs" : verb}
              </label>
            ))}
          </div>
        </fieldset>
        {error && (
          <p role="status" className="text-sm text-muted-foreground">
            {error}
          </p>
        )}
      </div>
    </ModalShell>
  );
}
