package handler

import (
	"fmt"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func flattenNamedResources(clusterID, resourceType string, payload map[string]any) []map[string]any {
	items := objectItems(payload)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		switch resourceType {
		case "services":
			out = append(out, flattenService(clusterID, item))
		case "ingresses":
			out = append(out, flattenIngress(clusterID, item))
		case "networkpolicies":
			out = append(out, flattenNetworkPolicy(clusterID, item))
		case "persistentvolumes":
			out = append(out, flattenPV(clusterID, item))
		case "persistentvolumeclaims":
			out = append(out, flattenPVC(clusterID, item))
		case "storageclasses":
			out = append(out, flattenStorageClass(clusterID, item))
		case "gateways":
			out = append(out, flattenGateway(clusterID, item))
		case "httproutes":
			out = append(out, flattenRouteResource(clusterID, item))
		case "grpcroutes":
			out = append(out, flattenRouteResource(clusterID, item))
		case "tlsroutes":
			out = append(out, flattenRouteResource(clusterID, item))
		case "tcproutes":
			out = append(out, flattenRouteResource(clusterID, item))
		case "udproutes":
			out = append(out, flattenRouteResource(clusterID, item))
		case "gatewayclasses":
			out = append(out, flattenGatewayClass(clusterID, item))
		case "referencegrants":
			out = append(out, flattenReferenceGrant(clusterID, item))
		default:
			out = append(out, flattenGeneric(clusterID, item))
		}
	}
	return out
}

func flattenGenericResources(clusterID, resourceType string, payload map[string]any) []map[string]any {
	items := objectItems(payload)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		entry := flattenGeneric(clusterID, item)
		switch resourceType {
		case "configmaps", "secrets":
			if data, ok := nestedMap(item, "data"); ok {
				entry["dataCount"] = len(data)
			}
			if resourceType == "secrets" {
				entry["type"] = stringValue(item, "type")
			}
		case "jobs":
			entry["completions"] = intValue(item, "status", "succeeded")
			entry["succeeded"] = intValue(item, "status", "succeeded")
			entry["failed"] = intValue(item, "status", "failed")
			entry["active"] = intValue(item, "status", "active")
			entry["status"] = defaultString(stringValue(item, "status", "conditions", "0", "type"), "Unknown")
		case "cronjobs":
			entry["schedule"] = stringValue(item, "spec", "schedule")
			entry["suspend"] = boolValue(item, "spec", "suspend")
			entry["activeCount"] = sliceLen(item, "status", "active")
		case "hpa":
			entry["minReplicas"] = intValue(item, "spec", "minReplicas")
			entry["maxReplicas"] = intValue(item, "spec", "maxReplicas")
			entry["currentReplicas"] = intValue(item, "status", "currentReplicas")
			entry["desiredReplicas"] = intValue(item, "status", "desiredReplicas")
			entry["targetKind"] = stringValue(item, "spec", "scaleTargetRef", "kind")
			entry["targetName"] = stringValue(item, "spec", "scaleTargetRef", "name")
		case "resourcequotas":
			entry["hard"] = nestedStringMap(item, "status", "hard")
			entry["used"] = nestedStringMap(item, "status", "used")
		case "limitranges":
			if limits, ok := nestedSlice(item, "spec", "limits"); ok {
				entry["limits"] = limits
			}
		case "poddisruptionbudgets":
			entry["minAvailable"] = stringValue(item, "spec", "minAvailable")
			entry["maxUnavailable"] = stringValue(item, "spec", "maxUnavailable")
			entry["currentHealthy"] = intValue(item, "status", "currentHealthy")
			entry["desiredHealthy"] = intValue(item, "status", "desiredHealthy")
		case "crds":
			entry["group"] = stringValue(item, "spec", "group")
			entry["kind"] = stringValue(item, "spec", "names", "kind")
			entry["scope"] = stringValue(item, "spec", "scope")
			entry["version"] = crdVersion(item)
		case "serviceaccounts":
			entry["secretsCount"] = sliceLen(item, "secrets")
		case "k8s-clusterroles", "k8s-roles":
			entry["rulesCount"] = sliceLen(item, "rules")
		case "k8s-clusterrolebindings", "k8s-rolebindings":
			entry["roleKind"] = stringValue(item, "roleRef", "kind")
			entry["roleName"] = stringValue(item, "roleRef", "name")
			entry["subjectsCount"] = sliceLen(item, "subjects")
		case "endpoints":
			entry["addressesCount"] = endpointAddressCount(item)
			entry["ports"] = endpointPorts(item)
		case "replicasets":
			entry["desired"] = intValue(item, "spec", "replicas")
			entry["ready"] = intValue(item, "status", "readyReplicas")
			entry["available"] = intValue(item, "status", "availableReplicas")
		}
		out = append(out, entry)
	}
	return out
}

func crdVersion(item map[string]any) string {
	if version := stringValue(item, "spec", "version"); version != "" {
		return version
	}
	versions, ok := nestedSlice(item, "spec", "versions")
	if !ok {
		return ""
	}
	for _, raw := range versions {
		version, _ := raw.(map[string]any)
		if boolValue(version, "storage") {
			return stringValueMap(version, "name")
		}
	}
	if len(versions) > 0 {
		if version, ok := versions[0].(map[string]any); ok {
			return stringValueMap(version, "name")
		}
	}
	return ""
}

func endpointAddressCount(item map[string]any) int {
	subsets, ok := nestedSlice(item, "subsets")
	if !ok {
		return 0
	}
	total := 0
	for _, raw := range subsets {
		subset, _ := raw.(map[string]any)
		total += sliceLen(subset, "addresses")
		total += sliceLen(subset, "notReadyAddresses")
	}
	return total
}

func endpointPorts(item map[string]any) string {
	subsets, ok := nestedSlice(item, "subsets")
	if !ok {
		return ""
	}
	ports := make([]string, 0)
	for _, raw := range subsets {
		subset, _ := raw.(map[string]any)
		items, _ := subset["ports"].([]any)
		for _, portRaw := range items {
			port, _ := portRaw.(map[string]any)
			number := fmt.Sprint(anyValue(port, "port"))
			if protocol := stringValueMap(port, "protocol"); protocol != "" {
				ports = append(ports, number+"/"+protocol)
				continue
			}
			ports = append(ports, number)
		}
	}
	return strings.Join(ports, ", ")
}

func flattenService(clusterID string, item map[string]any) map[string]any {
	return map[string]any{
		"name":        stringValue(item, "metadata", "name"),
		"namespace":   stringValue(item, "metadata", "namespace"),
		"clusterId":   clusterID,
		"clusterName": "",
		"type":        stringValue(item, "spec", "type"),
		"clusterIP":   stringValue(item, "spec", "clusterIP"),
		"externalIP":  firstString(item, "status", "loadBalancer", "ingress", "ip"),
		"ports":       nestedSliceOrEmpty(item, "spec", "ports"),
		"selector":    nestedStringMap(item, "spec", "selector"),
		"createdAt":   stringValue(item, "metadata", "creationTimestamp"),
	}
}

func flattenIngress(clusterID string, item map[string]any) map[string]any {
	hosts := []string{}
	paths := []map[string]any{}
	if rules, ok := nestedSlice(item, "spec", "rules"); ok {
		for _, raw := range rules {
			rule, _ := raw.(map[string]any)
			host, _ := rule["host"].(string)
			if host != "" {
				hosts = append(hosts, host)
			}
			httpRule, _ := rule["http"].(map[string]any)
			httpPaths, _ := httpRule["paths"].([]any)
			for _, pathItem := range httpPaths {
				pm, _ := pathItem.(map[string]any)
				backend, _ := pm["backend"].(map[string]any)
				service, _ := backend["service"].(map[string]any)
				port, _ := service["port"].(map[string]any)
				paths = append(paths, map[string]any{
					"host":        host,
					"path":        stringValueMap(pm, "path"),
					"pathType":    stringValueMap(pm, "pathType"),
					"serviceName": stringValueMap(service, "name"),
					"servicePort": anyValue(port, "number", "name"),
				})
			}
		}
	}
	return map[string]any{
		"name":         stringValue(item, "metadata", "name"),
		"namespace":    stringValue(item, "metadata", "namespace"),
		"clusterId":    clusterID,
		"clusterName":  "",
		"ingressClass": stringValue(item, "spec", "ingressClassName"),
		"hosts":        hosts,
		"paths":        paths,
		"tls":          sliceLen(item, "spec", "tls") > 0,
		"createdAt":    stringValue(item, "metadata", "creationTimestamp"),
	}
}

func flattenNetworkPolicy(clusterID string, item map[string]any) map[string]any {
	return map[string]any{
		"name":         stringValue(item, "metadata", "name"),
		"namespace":    stringValue(item, "metadata", "namespace"),
		"clusterId":    clusterID,
		"clusterName":  "",
		"podSelector":  nestedStringMap(item, "spec", "podSelector", "matchLabels"),
		"policyTypes":  stringSlice(item, "spec", "policyTypes"),
		"ingressRules": sliceLen(item, "spec", "ingress"),
		"egressRules":  sliceLen(item, "spec", "egress"),
		"createdAt":    stringValue(item, "metadata", "creationTimestamp"),
	}
}

// findCondition scans status.conditions[] for the first entry whose `type`
// matches conditionType. Returns ("", false) if not present.
func findCondition(item map[string]any, conditionType string) (status, reason string, ok bool) {
	conds, found := nestedSlice(item, "status", "conditions")
	if !found {
		return "", "", false
	}
	for _, raw := range conds {
		c, _ := raw.(map[string]any)
		if c == nil {
			continue
		}
		if stringValueMap(c, "type") == conditionType {
			return stringValueMap(c, "status"), stringValueMap(c, "reason"), true
		}
	}
	return "", "", false
}

func flattenGateway(clusterID string, item map[string]any) map[string]any {
	listeners := []map[string]any{}
	listenerSummary := []string{}
	if ls, ok := nestedSlice(item, "spec", "listeners"); ok {
		for _, raw := range ls {
			l, _ := raw.(map[string]any)
			if l == nil {
				continue
			}
			name := stringValueMap(l, "name")
			proto := stringValueMap(l, "protocol")
			port := intValue(l, "port")
			hostname := stringValueMap(l, "hostname")
			listeners = append(listeners, map[string]any{
				"name":     name,
				"protocol": proto,
				"port":     port,
				"hostname": hostname,
			})
			listenerSummary = append(listenerSummary, fmt.Sprintf("%s:%d", proto, port))
		}
	}
	addresses := []string{}
	if addrs, ok := nestedSlice(item, "status", "addresses"); ok {
		for _, raw := range addrs {
			a, _ := raw.(map[string]any)
			if a == nil {
				continue
			}
			if v := stringValueMap(a, "value"); v != "" {
				addresses = append(addresses, v)
			}
		}
	}
	programmedStatus, _, _ := findCondition(item, "Programmed")
	acceptedStatus, _, _ := findCondition(item, "Accepted")
	return map[string]any{
		"name":             stringValue(item, "metadata", "name"),
		"namespace":        stringValue(item, "metadata", "namespace"),
		"clusterId":        clusterID,
		"clusterName":      "",
		"gatewayClassName": stringValue(item, "spec", "gatewayClassName"),
		"listeners":        listeners,
		"listenerSummary":  listenerSummary,
		"listenerCount":    len(listeners),
		"addresses":        addresses,
		"programmed":       programmedStatus, // "True"/"False"/"Unknown"/""
		"accepted":         acceptedStatus,
		"createdAt":        stringValue(item, "metadata", "creationTimestamp"),
	}
}

// flattenRouteResource handles HTTPRoute / GRPCRoute / TLSRoute / TCPRoute /
// UDPRoute. They share the same shape that matters to the UI: parentRefs,
// hostnames (where applicable), and a rule count.
func flattenRouteResource(clusterID string, item map[string]any) map[string]any {
	parentRefs := []map[string]any{}
	parentSummary := []string{}
	if refs, ok := nestedSlice(item, "spec", "parentRefs"); ok {
		for _, raw := range refs {
			ref, _ := raw.(map[string]any)
			if ref == nil {
				continue
			}
			name := stringValueMap(ref, "name")
			ns := stringValueMap(ref, "namespace")
			section := stringValueMap(ref, "sectionName")
			parentRefs = append(parentRefs, map[string]any{
				"name":        name,
				"namespace":   ns,
				"sectionName": section,
				"kind":        stringValueMap(ref, "kind"),
			})
			label := name
			if ns != "" {
				label = ns + "/" + name
			}
			if section != "" {
				label += "#" + section
			}
			parentSummary = append(parentSummary, label)
		}
	}
	return map[string]any{
		"name":          stringValue(item, "metadata", "name"),
		"namespace":     stringValue(item, "metadata", "namespace"),
		"clusterId":     clusterID,
		"clusterName":   "",
		"hostnames":     stringSlice(item, "spec", "hostnames"),
		"parentRefs":    parentRefs,
		"parentSummary": parentSummary,
		"ruleCount":     sliceLen(item, "spec", "rules"),
		"createdAt":     stringValue(item, "metadata", "creationTimestamp"),
	}
}

func flattenGatewayClass(clusterID string, item map[string]any) map[string]any {
	acceptedStatus, _, _ := findCondition(item, "Accepted")
	return map[string]any{
		"name":           stringValue(item, "metadata", "name"),
		"clusterId":      clusterID,
		"clusterName":    "",
		"controllerName": stringValue(item, "spec", "controllerName"),
		"description":    stringValue(item, "spec", "description"),
		"accepted":       acceptedStatus,
		"createdAt":      stringValue(item, "metadata", "creationTimestamp"),
	}
}

func flattenReferenceGrant(clusterID string, item map[string]any) map[string]any {
	froms := []map[string]any{}
	tos := []map[string]any{}
	if entries, ok := nestedSlice(item, "spec", "from"); ok {
		for _, raw := range entries {
			e, _ := raw.(map[string]any)
			if e == nil {
				continue
			}
			froms = append(froms, map[string]any{
				"group":     stringValueMap(e, "group"),
				"kind":      stringValueMap(e, "kind"),
				"namespace": stringValueMap(e, "namespace"),
			})
		}
	}
	if entries, ok := nestedSlice(item, "spec", "to"); ok {
		for _, raw := range entries {
			e, _ := raw.(map[string]any)
			if e == nil {
				continue
			}
			tos = append(tos, map[string]any{
				"group": stringValueMap(e, "group"),
				"kind":  stringValueMap(e, "kind"),
				"name":  stringValueMap(e, "name"),
			})
		}
	}
	return map[string]any{
		"name":        stringValue(item, "metadata", "name"),
		"namespace":   stringValue(item, "metadata", "namespace"),
		"clusterId":   clusterID,
		"clusterName": "",
		"from":        froms,
		"to":          tos,
		"createdAt":   stringValue(item, "metadata", "creationTimestamp"),
	}
}

func flattenPV(clusterID string, item map[string]any) map[string]any {
	claimRef := ""
	if claim, ok := nestedMap(item, "spec", "claimRef"); ok {
		claimRef = stringValueMap(claim, "namespace") + "/" + stringValueMap(claim, "name")
	}
	return map[string]any{
		"name":          stringValue(item, "metadata", "name"),
		"clusterId":     clusterID,
		"clusterName":   "",
		"status":        stringValue(item, "status", "phase"),
		"capacity":      stringValue(item, "spec", "capacity", "storage"),
		"accessModes":   stringSlice(item, "spec", "accessModes"),
		"reclaimPolicy": stringValue(item, "spec", "persistentVolumeReclaimPolicy"),
		"storageClass":  stringValue(item, "spec", "storageClassName"),
		"volumeMode":    stringValue(item, "spec", "volumeMode"),
		"claimRef":      strings.TrimPrefix(claimRef, "/"),
		"createdAt":     stringValue(item, "metadata", "creationTimestamp"),
	}
}

func flattenPVC(clusterID string, item map[string]any) map[string]any {
	return map[string]any{
		"name":         stringValue(item, "metadata", "name"),
		"namespace":    stringValue(item, "metadata", "namespace"),
		"clusterId":    clusterID,
		"clusterName":  "",
		"status":       stringValue(item, "status", "phase"),
		"capacity":     stringValue(item, "status", "capacity", "storage"),
		"accessModes":  stringSlice(item, "spec", "accessModes"),
		"storageClass": stringValue(item, "spec", "storageClassName"),
		"volumeName":   stringValue(item, "spec", "volumeName"),
		"createdAt":    stringValue(item, "metadata", "creationTimestamp"),
	}
}

func flattenStorageClass(clusterID string, item map[string]any) map[string]any {
	annotations := nestedStringMap(item, "metadata", "annotations")
	return map[string]any{
		"name":                 stringValue(item, "metadata", "name"),
		"clusterId":            clusterID,
		"clusterName":          "",
		"provisioner":          stringValue(item, "provisioner"),
		"reclaimPolicy":        stringValue(item, "reclaimPolicy"),
		"volumeBindingMode":    stringValue(item, "volumeBindingMode"),
		"allowVolumeExpansion": boolValue(item, "allowVolumeExpansion"),
		"isDefault":            annotations["storageclass.kubernetes.io/is-default-class"] == "true",
		"parameters":           nestedStringMap(item, "parameters"),
		"createdAt":            stringValue(item, "metadata", "creationTimestamp"),
	}
}

func flattenGeneric(clusterID string, item map[string]any) map[string]any {
	return map[string]any{
		"name":        stringValue(item, "metadata", "name"),
		"namespace":   stringValue(item, "metadata", "namespace"),
		"clusterId":   clusterID,
		"labels":      nestedStringMap(item, "metadata", "labels"),
		"annotations": nestedStringMap(item, "metadata", "annotations"),
		"createdAt":   stringValue(item, "metadata", "creationTimestamp"),
	}
}

func objectItems(payload map[string]any) []map[string]any {
	rawItems, _ := payload["items"].([]any)
	items := make([]map[string]any, 0, len(rawItems))
	for _, raw := range rawItems {
		if item, ok := raw.(map[string]any); ok {
			items = append(items, item)
		}
	}
	return items
}

func nestedMap(value map[string]any, path ...string) (map[string]any, bool) {
	current := value
	for i, key := range path {
		v, ok := current[key]
		if !ok {
			return nil, false
		}
		if i == len(path)-1 {
			out, ok := v.(map[string]any)
			return out, ok
		}
		next, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		current = next
	}
	return nil, false
}

func nestedSlice(value map[string]any, path ...string) ([]any, bool) {
	if len(path) == 0 {
		return nil, false
	}
	current := value
	for i, key := range path {
		v, ok := current[key]
		if !ok {
			return nil, false
		}
		if i == len(path)-1 {
			out, ok := v.([]any)
			return out, ok
		}
		next, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		current = next
	}
	return nil, false
}

func nestedSliceOrEmpty(value map[string]any, path ...string) []any {
	if out, ok := nestedSlice(value, path...); ok {
		return out
	}
	return []any{}
}

func nestedStringMap(value map[string]any, path ...string) map[string]string {
	m, ok := nestedMap(value, path...)
	if !ok {
		return map[string]string{}
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func stringSlice(value map[string]any, path ...string) []string {
	raw, ok := nestedSlice(value, path...)
	if !ok {
		return []string{}
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func stringValue(value map[string]any, path ...string) string {
	current := value
	for i, key := range path {
		v, ok := current[key]
		if !ok {
			return ""
		}
		if i == len(path)-1 {
			if s, ok := v.(string); ok {
				return s
			}
			return fmt.Sprint(v)
		}
		next, ok := v.(map[string]any)
		if !ok {
			return ""
		}
		current = next
	}
	return ""
}

func stringValueMap(value map[string]any, key string) string {
	if value == nil {
		return ""
	}
	if s, ok := value[key].(string); ok {
		return s
	}
	return ""
}

func intValue(value map[string]any, path ...string) int {
	current := value
	for i, key := range path {
		v, ok := current[key]
		if !ok {
			return 0
		}
		if i == len(path)-1 {
			switch n := v.(type) {
			case float64:
				return int(n)
			case int:
				return n
			default:
				return 0
			}
		}
		next, ok := v.(map[string]any)
		if !ok {
			return 0
		}
		current = next
	}
	return 0
}

func boolValue(value map[string]any, path ...string) bool {
	current := value
	for i, key := range path {
		v, ok := current[key]
		if !ok {
			return false
		}
		if i == len(path)-1 {
			b, _ := v.(bool)
			return b
		}
		next, ok := v.(map[string]any)
		if !ok {
			return false
		}
		current = next
	}
	return false
}

func anyValue(value map[string]any, keys ...string) any {
	for _, key := range keys {
		if v, ok := value[key]; ok {
			return v
		}
	}
	return nil
}

func firstString(value map[string]any, path ...string) string {
	if len(path) < 2 {
		return ""
	}
	slice, ok := nestedSlice(value, path[:len(path)-1]...)
	if !ok || len(slice) == 0 {
		return ""
	}
	first, _ := slice[0].(map[string]any)
	return stringValueMap(first, path[len(path)-1])
}

func sliceLen(value map[string]any, path ...string) int {
	slice, ok := nestedSlice(value, path...)
	if !ok {
		return 0
	}
	return len(slice)
}

func mapUser(user sqlc.User) map[string]any {
	displayName := strings.TrimSpace(strings.TrimSpace(user.FirstName) + " " + strings.TrimSpace(user.LastName))
	if displayName == "" {
		displayName = user.Username
	}
	lastLogin := ""
	if user.LastLogin.Valid {
		lastLogin = user.LastLogin.Time.UTC().Format(timeLayout)
	}
	return map[string]any{
		"id":           user.ID.String(),
		"username":     user.Username,
		"email":        user.Email,
		"displayName":  displayName,
		"provider":     "local",
		"globalRoles":  []string{},
		"is_superuser": user.IsSuperuser,
		"enabled":      user.IsActive,
		"lastLogin":    lastLogin,
		"createdAt":    user.CreatedAt.UTC().Format(timeLayout),
	}
}

func nullableUserID(id pgtype.UUID) any {
	if id.Valid {
		return uuid.UUID(id.Bytes).String()
	}
	return nil
}

func defaultString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

const timeLayout = "2006-01-02T15:04:05Z"
