package agent

import "k8s.io/apimachinery/pkg/runtime/schema"

// metadataInformerKinds is the P4.6 expansion set: built-in kinds watched
// metadata-only (same MsgStateUpdate shape, no bodies, no secret surface).
// Always present on any supported apiserver, so they share the main
// metadata factory; RBAC-denied informers fail to sync and are logged
// without crashing the subscriber (bounded sync wait in Run).
var metadataInformerKinds = []metadataKind{
	{"Namespace", "", "v1", schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}},
	{"Job", "batch", "v1", schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}},
	{"CronJob", "batch", "v1", schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}},
	{"Ingress", "networking.k8s.io", "v1", schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}},
	{"NetworkPolicy", "networking.k8s.io", "v1", schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}},
	{"PersistentVolume", "", "v1", schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumes"}},
	{"HorizontalPodAutoscaler", "autoscaling", "v2", schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"}},
	{"ServiceAccount", "", "v1", schema.GroupVersionResource{Version: "v1", Resource: "serviceaccounts"}},
	{"Role", "rbac.authorization.k8s.io", "v1", schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"}},
	{"RoleBinding", "rbac.authorization.k8s.io", "v1", schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"}},
	{"ClusterRole", "rbac.authorization.k8s.io", "v1", schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}},
	{"ClusterRoleBinding", "rbac.authorization.k8s.io", "v1", schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"}},
	{"ResourceQuota", "", "v1", schema.GroupVersionResource{Version: "v1", Resource: "resourcequotas"}},
}
