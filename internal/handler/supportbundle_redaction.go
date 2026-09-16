package handler

import (
	"archive/zip"
	"bufio"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/redaction"
	"github.com/jackc/pgx/v5/pgtype"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
)

type sectionLog struct {
	lines []string
}

func newSectionLog() *sectionLog { return &sectionLog{} }

func (s *sectionLog) section(name string, err error) {
	if err == nil {
		s.lines = append(s.lines, name+"  OK")
		return
	}
	s.lines = append(s.lines, name+"  FAILED: "+err.Error())
}

func (s *sectionLog) skipped(name, reason string) {
	s.lines = append(s.lines, name+"  SKIPPED: "+reason)
}

func writeBundleJSON(zw *zip.Writer, name string, payload any) error {
	fw, err := zw.Create(name)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(fw)
	enc.SetIndent("", "  ")
	return enc.Encode(redaction.Payload(payload))
}

func writeRedactedLogStream(dst interface{ Write([]byte) (int, error) }, src interface{ Read([]byte) (int, error) }) error {
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if _, err := fmt.Fprintln(dst, redaction.SensitiveLine(scanner.Text())); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func timestamptzString(value pgtype.Timestamptz) any {
	if !value.Valid {
		return nil
	}
	return value.Time.UTC().Format(time.RFC3339)
}

// sumSecretBytes is a cheap "how big is this helm release blob" probe for
// the helm-releases.json section. We don't include the actual data —
// helm releases are compressed JSON manifests that can run to hundreds
// of kilobytes and would balloon the bundle.
func sumSecretBytes(s corev1.Secret) int {
	total := 0
	for _, v := range s.Data {
		total += len(v)
	}
	return total
}

func summarizeContainers(statuses []corev1.ContainerStatus) []map[string]any {
	out := make([]map[string]any, 0, len(statuses))
	for _, cs := range statuses {
		entry := map[string]any{
			"name":          cs.Name,
			"image":         cs.Image,
			"ready":         cs.Ready,
			"restart_count": cs.RestartCount,
		}
		if cs.State.Waiting != nil {
			entry["state"] = "Waiting"
			entry["reason"] = cs.State.Waiting.Reason
		} else if cs.State.Running != nil {
			entry["state"] = "Running"
			entry["started_at"] = cs.State.Running.StartedAt
		} else if cs.State.Terminated != nil {
			entry["state"] = "Terminated"
			entry["reason"] = cs.State.Terminated.Reason
			entry["exit_code"] = cs.State.Terminated.ExitCode
		}
		out = append(out, entry)
	}
	return out
}

func summarizeNetworkPolicy(policy networkingv1.NetworkPolicy) map[string]any {
	entry := map[string]any{
		"name":               policy.Name,
		"namespace":          policy.Namespace,
		"created":            policy.CreationTimestamp,
		"pod_selector":       policy.Spec.PodSelector.MatchLabels,
		"policy_types":       networkPolicyTypes(policy.Spec.PolicyTypes),
		"ingress_rule_count": len(policy.Spec.Ingress),
		"egress_rule_count":  len(policy.Spec.Egress),
	}
	if policy.Spec.PodSelector.MatchExpressions != nil {
		entry["pod_selector_match_expressions"] = len(policy.Spec.PodSelector.MatchExpressions)
	}
	return entry
}

func networkPolicyTypes(types []networkingv1.PolicyType) []string {
	out := make([]string, 0, len(types))
	for _, policyType := range types {
		out = append(out, string(policyType))
	}
	return out
}

func summarizeIngress(ing networkingv1.Ingress) map[string]any {
	hosts := make([]string, 0)
	for _, rule := range ing.Spec.Rules {
		if rule.Host != "" {
			hosts = append(hosts, rule.Host)
		}
	}
	entry := map[string]any{
		"name":        ing.Name,
		"namespace":   ing.Namespace,
		"class_name":  ingressClassName(ing),
		"hosts":       hosts,
		"tls_secrets": ingressTLSSecretNames(ing),
		"addresses":   ingressLoadBalancerAddresses(ing),
		"created":     ing.CreationTimestamp,
	}
	return entry
}

func ingressClassName(ing networkingv1.Ingress) string {
	if ing.Spec.IngressClassName == nil {
		return ""
	}
	return *ing.Spec.IngressClassName
}

func ingressTLSSecretNames(ing networkingv1.Ingress) []string {
	out := make([]string, 0, len(ing.Spec.TLS))
	seen := map[string]bool{}
	for _, tls := range ing.Spec.TLS {
		if tls.SecretName == "" || seen[tls.SecretName] {
			continue
		}
		out = append(out, tls.SecretName)
		seen[tls.SecretName] = true
	}
	return out
}

func ingressLoadBalancerAddresses(ing networkingv1.Ingress) []string {
	out := make([]string, 0, len(ing.Status.LoadBalancer.Ingress))
	for _, lb := range ing.Status.LoadBalancer.Ingress {
		if lb.Hostname != "" {
			out = append(out, lb.Hostname)
			continue
		}
		if lb.IP != "" {
			out = append(out, lb.IP)
		}
	}
	return out
}

func summarizeTLSSecret(secret corev1.Secret) map[string]any {
	entry := map[string]any{
		"name":    secret.Name,
		"present": true,
		"type":    string(secret.Type),
		"created": secret.CreationTimestamp,
	}
	certs, err := parseCertificateSummaries(secret.Data[corev1.TLSCertKey])
	if err != nil {
		entry["parse_error"] = err.Error()
		return entry
	}
	entry["certificates"] = certs
	return entry
}

func parseCertificateSummaries(raw []byte) ([]map[string]any, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("tls.crt missing")
	}
	out := make([]map[string]any, 0, 1)
	rest := raw
	for {
		block, remaining := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = remaining
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"subject":    cert.Subject.String(),
			"issuer":     cert.Issuer.String(),
			"dns_names":  cert.DNSNames,
			"not_before": cert.NotBefore.UTC().Format(time.RFC3339),
			"not_after":  cert.NotAfter.UTC().Format(time.RFC3339),
			"is_expired": time.Now().After(cert.NotAfter),
			"serial":     fmt.Sprintf("%X", cert.SerialNumber),
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no PEM certificate blocks found")
	}
	return out, nil
}
