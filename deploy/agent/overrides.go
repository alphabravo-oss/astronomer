package agenttemplate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/yaml"
)

const (
	maxAgentOverridesBytes       = 32 << 10
	maxAgentTolerations          = 16
	maxAgentNodeSelectorTerms    = 16
	maxAgentSelectorRequirements = 64
)

// AgentOverrides is the deliberately bounded, per-cluster portion of the
// agent PodSpec operators may customize. Security context, service account,
// Linux node selector, volumes, probes and the agent's own environment remain
// platform-owned and cannot be changed through this API.
type AgentOverrides struct {
	Tolerations []AgentToleration `json:"tolerations,omitempty"`
	Affinity    *AgentAffinity    `json:"affinity,omitempty"`
	Resources   *AgentResources   `json:"resources,omitempty"`
	Proxy       *AgentProxy       `json:"proxy,omitempty"`
}

type AgentToleration struct {
	Key               string `json:"key"`
	Operator          string `json:"operator,omitempty"`
	Value             string `json:"value,omitempty"`
	Effect            string `json:"effect,omitempty"`
	TolerationSeconds *int64 `json:"toleration_seconds,omitempty"`
}

// AgentAffinity supports Kubernetes node affinity. Pod affinity is omitted on
// purpose: the singleton agent has no stable peer workload and an operator-set
// pod selector can make the management channel permanently unschedulable.
type AgentAffinity struct {
	Node *AgentNodeAffinity `json:"node,omitempty"`
}

type AgentNodeAffinity struct {
	Required  []AgentNodeSelectorTerm          `json:"required,omitempty"`
	Preferred []AgentPreferredNodeSelectorTerm `json:"preferred,omitempty"`
}

type AgentNodeSelectorTerm struct {
	MatchExpressions []AgentNodeSelectorRequirement `json:"match_expressions,omitempty"`
	MatchFields      []AgentNodeSelectorRequirement `json:"match_fields,omitempty"`
}

type AgentPreferredNodeSelectorTerm struct {
	Weight     int32                 `json:"weight"`
	Preference AgentNodeSelectorTerm `json:"preference"`
}

type AgentNodeSelectorRequirement struct {
	Key      string   `json:"key"`
	Operator string   `json:"operator"`
	Values   []string `json:"values,omitempty"`
}

type AgentResources struct {
	Requests AgentResourceValues `json:"requests,omitempty"`
	Limits   AgentResourceValues `json:"limits,omitempty"`
}

type AgentResourceValues struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
}

// AgentProxy maps to the standard proxy variables only. Arbitrary environment
// variable names are intentionally not accepted.
type AgentProxy struct {
	HTTPProxy  string `json:"http_proxy,omitempty"`
	HTTPSProxy string `json:"https_proxy,omitempty"`
	NoProxy    string `json:"no_proxy,omitempty"`
}

func (o *AgentOverrides) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("agent_overrides must be an object")
	}
	type plain AgentOverrides
	var decoded plain
	if err := decodeStrictAgentJSON(data, &decoded); err != nil {
		return err
	}
	*o = AgentOverrides(decoded)
	return o.Validate()
}

func decodeStrictAgentJSON(data []byte, out any) error {
	if len(data) > maxAgentOverridesBytes {
		return fmt.Errorf("agent_overrides exceeds %d bytes", maxAgentOverridesBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("invalid agent_overrides: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("invalid agent_overrides: multiple JSON values")
	}
	return nil
}

func (o AgentOverrides) Validate() error {
	encoded, err := json.Marshal(o)
	if err != nil || len(encoded) > maxAgentOverridesBytes {
		return fmt.Errorf("agent_overrides exceeds %d bytes", maxAgentOverridesBytes)
	}
	if len(o.Tolerations) > maxAgentTolerations {
		return fmt.Errorf("agent_overrides.tolerations supports at most %d entries", maxAgentTolerations)
	}
	for i, toleration := range o.Tolerations {
		if err := validateAgentToleration(toleration); err != nil {
			return fmt.Errorf("agent_overrides.tolerations[%d]: %w", i, err)
		}
	}
	if err := validateAgentAffinity(o.Affinity); err != nil {
		return fmt.Errorf("agent_overrides.affinity: %w", err)
	}
	if err := validateAgentResources(o.Resources); err != nil {
		return fmt.Errorf("agent_overrides.resources: %w", err)
	}
	if err := validateAgentProxy(o.Proxy); err != nil {
		return fmt.Errorf("agent_overrides.proxy: %w", err)
	}
	return nil
}

func validateAgentToleration(t AgentToleration) error {
	if t.Key == "" || len(k8svalidation.IsQualifiedName(t.Key)) > 0 {
		return fmt.Errorf("key must be a non-empty Kubernetes qualified name")
	}
	if t.Key == "node-role.kubernetes.io/control-plane" || t.Key == "node-role.kubernetes.io/master" {
		return fmt.Errorf("platform control-plane tolerations cannot be overridden")
	}
	operator := t.Operator
	if operator == "" {
		operator = "Equal"
	}
	if operator != "Equal" && operator != "Exists" {
		return fmt.Errorf("operator must be Equal or Exists")
	}
	if operator == "Exists" && t.Value != "" {
		return fmt.Errorf("value must be empty when operator is Exists")
	}
	if len(t.Value) > 253 || strings.ContainsAny(t.Value, "\r\n\x00") {
		return fmt.Errorf("value is invalid")
	}
	switch t.Effect {
	case "", "NoSchedule", "PreferNoSchedule", "NoExecute":
	default:
		return fmt.Errorf("effect must be NoSchedule, PreferNoSchedule, or NoExecute")
	}
	if t.TolerationSeconds != nil {
		if t.Effect != "NoExecute" || *t.TolerationSeconds < 0 || *t.TolerationSeconds > 604800 {
			return fmt.Errorf("toleration_seconds requires NoExecute and must be between 0 and 604800")
		}
	}
	return nil
}

func validateAgentAffinity(affinity *AgentAffinity) error {
	if affinity == nil || affinity.Node == nil {
		return nil
	}
	node := affinity.Node
	if len(node.Required)+len(node.Preferred) > maxAgentNodeSelectorTerms {
		return fmt.Errorf("supports at most %d node selector terms", maxAgentNodeSelectorTerms)
	}
	for i, term := range node.Required {
		if err := validateAgentNodeSelectorTerm(term); err != nil {
			return fmt.Errorf("node.required[%d]: %w", i, err)
		}
	}
	for i, preferred := range node.Preferred {
		if preferred.Weight < 1 || preferred.Weight > 100 {
			return fmt.Errorf("node.preferred[%d].weight must be between 1 and 100", i)
		}
		if err := validateAgentNodeSelectorTerm(preferred.Preference); err != nil {
			return fmt.Errorf("node.preferred[%d].preference: %w", i, err)
		}
	}
	return nil
}

func validateAgentNodeSelectorTerm(term AgentNodeSelectorTerm) error {
	requirements := append(append([]AgentNodeSelectorRequirement{}, term.MatchExpressions...), term.MatchFields...)
	if len(requirements) == 0 || len(requirements) > maxAgentSelectorRequirements {
		return fmt.Errorf("must contain 1 to %d match requirements", maxAgentSelectorRequirements)
	}
	for i, requirement := range requirements {
		if requirement.Key == "" || len(k8svalidation.IsQualifiedName(requirement.Key)) > 0 {
			return fmt.Errorf("requirement[%d].key must be a Kubernetes qualified name", i)
		}
		switch requirement.Operator {
		case "In", "NotIn":
			if len(requirement.Values) == 0 || len(requirement.Values) > 32 {
				return fmt.Errorf("requirement[%d] needs 1 to 32 values", i)
			}
		case "Exists", "DoesNotExist":
			if len(requirement.Values) != 0 {
				return fmt.Errorf("requirement[%d] cannot have values for %s", i, requirement.Operator)
			}
		case "Gt", "Lt":
			if len(requirement.Values) != 1 || !regexp.MustCompile(`^[0-9]+$`).MatchString(requirement.Values[0]) {
				return fmt.Errorf("requirement[%d] needs one non-negative integer value", i)
			}
		default:
			return fmt.Errorf("requirement[%d].operator is invalid", i)
		}
		for _, value := range requirement.Values {
			if len(value) > 63 || strings.ContainsAny(value, "\r\n\x00") {
				return fmt.Errorf("requirement[%d] contains an invalid value", i)
			}
		}
	}
	return nil
}

func validateAgentResources(resources *AgentResources) error {
	if resources == nil {
		return nil
	}
	requestCPU, err := positiveQuantity("requests.cpu", resources.Requests.CPU)
	if err != nil {
		return err
	}
	requestMemory, err := positiveQuantity("requests.memory", resources.Requests.Memory)
	if err != nil {
		return err
	}
	limitCPU, err := positiveQuantity("limits.cpu", resources.Limits.CPU)
	if err != nil {
		return err
	}
	limitMemory, err := positiveQuantity("limits.memory", resources.Limits.Memory)
	if err != nil {
		return err
	}
	if requestCPU != nil && limitCPU != nil && requestCPU.Cmp(*limitCPU) > 0 {
		return fmt.Errorf("requests.cpu cannot exceed limits.cpu")
	}
	if requestMemory != nil && limitMemory != nil && requestMemory.Cmp(*limitMemory) > 0 {
		return fmt.Errorf("requests.memory cannot exceed limits.memory")
	}
	return nil
}

func positiveQuantity(name, value string) (*resource.Quantity, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > 32 || strings.TrimSpace(value) != value {
		return nil, fmt.Errorf("%s is invalid", name)
	}
	quantity, err := resource.ParseQuantity(value)
	if err != nil || quantity.Sign() <= 0 {
		return nil, fmt.Errorf("%s must be a positive Kubernetes quantity", name)
	}
	return &quantity, nil
}

func validateAgentProxy(proxy *AgentProxy) error {
	if proxy == nil {
		return nil
	}
	for name, value := range map[string]string{"http_proxy": proxy.HTTPProxy, "https_proxy": proxy.HTTPSProxy} {
		if value == "" {
			continue
		}
		if len(value) > 2048 || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("%s is invalid", name)
		}
		parsed, err := url.Parse(value)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("%s must be an http(s) proxy URL without credentials, query, or fragment", name)
		}
	}
	if len(proxy.NoProxy) > 4096 || strings.ContainsAny(proxy.NoProxy, "\r\n\x00") {
		return fmt.Errorf("no_proxy is invalid")
	}
	for _, part := range strings.Split(proxy.NoProxy, ",") {
		part = strings.TrimSpace(part)
		if part != "" && !regexp.MustCompile(`^[A-Za-z0-9.*:_/\-]+$`).MatchString(part) {
			return fmt.Errorf("no_proxy contains an invalid entry")
		}
	}
	return nil
}

// CanonicalJSON and Digest use normalized typed data, so map ordering or input
// whitespace can never create a spurious rollout.
func (o AgentOverrides) CanonicalJSON() ([]byte, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(o)
}

func (o AgentOverrides) Digest() (string, error) {
	data, err := o.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (o AgentOverrides) resourcesYAML() string {
	requests := map[string]string{"cpu": "100m", "memory": "128Mi"}
	limits := map[string]string{"cpu": "500m", "memory": "512Mi"}
	if o.Resources != nil {
		if o.Resources.Requests.CPU != "" {
			requests["cpu"] = o.Resources.Requests.CPU
		}
		if o.Resources.Requests.Memory != "" {
			requests["memory"] = o.Resources.Requests.Memory
		}
		if o.Resources.Limits.CPU != "" {
			limits["cpu"] = o.Resources.Limits.CPU
		}
		if o.Resources.Limits.Memory != "" {
			limits["memory"] = o.Resources.Limits.Memory
		}
	}
	return indentedYAML(map[string]any{"requests": requests, "limits": limits}, 12)
}

func (o AgentOverrides) tolerationsYAML() string {
	items := []map[string]any{
		{"key": "node-role.kubernetes.io/control-plane", "operator": "Exists", "effect": "NoSchedule"},
		{"key": "node-role.kubernetes.io/master", "operator": "Exists", "effect": "NoSchedule"},
	}
	for _, t := range o.Tolerations {
		item := map[string]any{"key": t.Key, "operator": t.Operator}
		if item["operator"] == "" {
			item["operator"] = "Equal"
		}
		if t.Value != "" {
			item["value"] = t.Value
		}
		if t.Effect != "" {
			item["effect"] = t.Effect
		}
		if t.TolerationSeconds != nil {
			item["tolerationSeconds"] = *t.TolerationSeconds
		}
		items = append(items, item)
	}
	return indentedYAML(items, 8)
}

func (o AgentOverrides) affinityYAML() string {
	if o.Affinity == nil || o.Affinity.Node == nil {
		return ""
	}
	node := o.Affinity.Node
	value := map[string]any{}
	if len(node.Required) > 0 {
		value["requiredDuringSchedulingIgnoredDuringExecution"] = map[string]any{"nodeSelectorTerms": affinityTerms(node.Required)}
	}
	if len(node.Preferred) > 0 {
		preferred := make([]map[string]any, 0, len(node.Preferred))
		for _, item := range node.Preferred {
			preferred = append(preferred, map[string]any{"weight": item.Weight, "preference": affinityTerm(item.Preference)})
		}
		value["preferredDuringSchedulingIgnoredDuringExecution"] = preferred
	}
	return "      affinity:\n" + indentedYAML(map[string]any{"nodeAffinity": value}, 8)
}

func affinityTerms(terms []AgentNodeSelectorTerm) []map[string]any {
	out := make([]map[string]any, 0, len(terms))
	for _, term := range terms {
		out = append(out, affinityTerm(term))
	}
	return out
}

func affinityTerm(term AgentNodeSelectorTerm) map[string]any {
	out := map[string]any{}
	if len(term.MatchExpressions) > 0 {
		out["matchExpressions"] = selectorRequirements(term.MatchExpressions)
	}
	if len(term.MatchFields) > 0 {
		out["matchFields"] = selectorRequirements(term.MatchFields)
	}
	return out
}

func selectorRequirements(values []AgentNodeSelectorRequirement) []map[string]any {
	out := make([]map[string]any, 0, len(values))
	for _, value := range values {
		item := map[string]any{"key": value.Key, "operator": value.Operator}
		if len(value.Values) > 0 {
			item["values"] = value.Values
		}
		out = append(out, item)
	}
	return out
}

func (o AgentOverrides) proxyEnvYAML() string {
	if o.Proxy == nil {
		return ""
	}
	values := map[string]string{"HTTP_PROXY": o.Proxy.HTTPProxy, "HTTPS_PROXY": o.Proxy.HTTPSProxy, "NO_PROXY": o.Proxy.NoProxy}
	names := make([]string, 0, len(values))
	for name, value := range values {
		if value != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var out strings.Builder
	for _, name := range names {
		out.WriteString("            - name: " + name + "\n")
		out.WriteString("              value: \"" + escapeYAMLDoubleQuoted(values[name]) + "\"\n")
	}
	return strings.TrimSuffix(out.String(), "\n")
}

func indentedYAML(value any, spaces int) string {
	data, _ := yaml.Marshal(value)
	prefix := strings.Repeat(" ", spaces)
	return prefix + strings.ReplaceAll(strings.TrimSuffix(string(data), "\n"), "\n", "\n"+prefix)
}

// ApplyToPodSpec applies the same effective values RenderInstallYAML emits.
// It is used by the in-cluster self-upgrader so a configuration-only change
// and an image change converge on one PodSpec rather than two implementations.
func (o AgentOverrides) ApplyToPodSpec(spec *corev1.PodSpec, containerIndex int) error {
	if spec == nil || containerIndex < 0 || containerIndex >= len(spec.Containers) {
		return fmt.Errorf("agent container is missing")
	}
	if err := o.Validate(); err != nil {
		return err
	}
	resources := o.Resources
	if resources == nil {
		resources = &AgentResources{}
	}
	parse := func(value, fallback string) resource.Quantity {
		if value == "" {
			value = fallback
		}
		return resource.MustParse(value)
	}
	spec.Containers[containerIndex].Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    parse(resources.Requests.CPU, "100m"),
			corev1.ResourceMemory: parse(resources.Requests.Memory, "128Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    parse(resources.Limits.CPU, "500m"),
			corev1.ResourceMemory: parse(resources.Limits.Memory, "512Mi"),
		},
	}
	spec.Tolerations = []corev1.Toleration{
		{Key: "node-role.kubernetes.io/control-plane", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
		{Key: "node-role.kubernetes.io/master", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
	}
	for _, value := range o.Tolerations {
		operator := corev1.TolerationOperator(value.Operator)
		if operator == "" {
			operator = corev1.TolerationOpEqual
		}
		spec.Tolerations = append(spec.Tolerations, corev1.Toleration{
			Key: value.Key, Operator: operator, Value: value.Value,
			Effect: corev1.TaintEffect(value.Effect), TolerationSeconds: value.TolerationSeconds,
		})
	}
	spec.Affinity = nil
	if o.Affinity != nil && o.Affinity.Node != nil {
		node := o.Affinity.Node
		affinity := &corev1.NodeAffinity{}
		if len(node.Required) > 0 {
			affinity.RequiredDuringSchedulingIgnoredDuringExecution = &corev1.NodeSelector{NodeSelectorTerms: coreNodeSelectorTerms(node.Required)}
		}
		for _, value := range node.Preferred {
			affinity.PreferredDuringSchedulingIgnoredDuringExecution = append(affinity.PreferredDuringSchedulingIgnoredDuringExecution, corev1.PreferredSchedulingTerm{
				Weight: value.Weight, Preference: coreNodeSelectorTerm(value.Preference),
			})
		}
		spec.Affinity = &corev1.Affinity{NodeAffinity: affinity}
	}
	container := &spec.Containers[containerIndex]
	proxyNames := map[string]bool{"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true}
	env := container.Env[:0]
	for _, value := range container.Env {
		if !proxyNames[value.Name] {
			env = append(env, value)
		}
	}
	if o.Proxy != nil {
		for _, value := range []corev1.EnvVar{{Name: "HTTP_PROXY", Value: o.Proxy.HTTPProxy}, {Name: "HTTPS_PROXY", Value: o.Proxy.HTTPSProxy}, {Name: "NO_PROXY", Value: o.Proxy.NoProxy}} {
			if value.Value != "" {
				env = append(env, value)
			}
		}
	}
	container.Env = env
	return nil
}

func coreNodeSelectorTerms(values []AgentNodeSelectorTerm) []corev1.NodeSelectorTerm {
	out := make([]corev1.NodeSelectorTerm, 0, len(values))
	for _, value := range values {
		out = append(out, coreNodeSelectorTerm(value))
	}
	return out
}

func coreNodeSelectorTerm(value AgentNodeSelectorTerm) corev1.NodeSelectorTerm {
	convert := func(values []AgentNodeSelectorRequirement) []corev1.NodeSelectorRequirement {
		out := make([]corev1.NodeSelectorRequirement, 0, len(values))
		for _, value := range values {
			out = append(out, corev1.NodeSelectorRequirement{Key: value.Key, Operator: corev1.NodeSelectorOperator(value.Operator), Values: value.Values})
		}
		return out
	}
	return corev1.NodeSelectorTerm{MatchExpressions: convert(value.MatchExpressions), MatchFields: convert(value.MatchFields)}
}
