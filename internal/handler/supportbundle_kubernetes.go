package handler

import (
	"archive/zip"
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (h *SupportBundleHandler) writePods(ctx context.Context, zw *zip.Writer, log *sectionLog) {
	if h.k8s == nil || h.namespace == "" {
		log.skipped("pods.json", "k8s client not wired")
		return
	}
	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pods, err := h.k8s.CoreV1().Pods(h.namespace).List(listCtx, metav1.ListOptions{})
	if err != nil {
		log.section("pods.json", err)
		return
	}
	out := make([]map[string]any, 0, len(pods.Items))
	for _, p := range pods.Items {
		out = append(out, map[string]any{
			"name":               p.Name,
			"phase":              string(p.Status.Phase),
			"node":               p.Spec.NodeName,
			"start_time":         p.Status.StartTime,
			"container_statuses": summarizeContainers(p.Status.ContainerStatuses),
			"creation_timestamp": p.CreationTimestamp,
		})
	}
	log.section("pods.json", writeBundleJSON(zw, "pods.json", out))
}

func (h *SupportBundleHandler) writePodLogs(ctx context.Context, zw *zip.Writer, log *sectionLog) {
	if h.k8s == nil || h.namespace == "" {
		log.skipped("pod-logs/", "k8s client not wired")
		return
	}
	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pods, err := h.k8s.CoreV1().Pods(h.namespace).List(listCtx, metav1.ListOptions{})
	cancel()
	if err != nil {
		log.section("pod-logs/", err)
		return
	}
	tailLines := int64(200)
	for _, p := range pods.Items {
		for _, c := range p.Spec.Containers {
			name := fmt.Sprintf("pod-logs/%s_%s.log", p.Name, c.Name)
			logsCtx, lcancel := context.WithTimeout(ctx, 15*time.Second)
			rc, err := h.k8s.CoreV1().Pods(h.namespace).GetLogs(p.Name, &corev1.PodLogOptions{
				Container: c.Name,
				TailLines: &tailLines,
			}).Stream(logsCtx)
			if err != nil {
				log.section(name, err)
				lcancel()
				continue
			}
			fw, err := zw.Create(name)
			if err != nil {
				_ = rc.Close()
				lcancel()
				log.section(name, err)
				continue
			}
			copyErr := writeRedactedLogStream(fw, rc)
			_ = rc.Close()
			lcancel()
			log.section(name, copyErr)
		}
	}
}

// writeEvents captures the namespace's k8s Events for the last 24h or so
// (k8s default retention is 1h-ish but we'll grab whatever's there). One
// of the things support engineers ask for first when something's broken.
func (h *SupportBundleHandler) writeEvents(ctx context.Context, zw *zip.Writer, log *sectionLog) {
	if h.k8s == nil || h.namespace == "" {
		log.skipped("events.json", "k8s client not wired")
		return
	}
	lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	events, err := h.k8s.CoreV1().Events(h.namespace).List(lctx, metav1.ListOptions{Limit: 500})
	if err != nil {
		log.section("events.json", err)
		return
	}
	out := make([]map[string]any, 0, len(events.Items))
	for _, e := range events.Items {
		out = append(out, map[string]any{
			"type":            e.Type,
			"reason":          e.Reason,
			"message":         e.Message,
			"object_kind":     e.InvolvedObject.Kind,
			"object_name":     e.InvolvedObject.Name,
			"first_timestamp": e.FirstTimestamp,
			"last_timestamp":  e.LastTimestamp,
			"count":           e.Count,
		})
	}
	log.section("events.json", writeBundleJSON(zw, "events.json", out))
}

// writeHelmRelease snapshots the chart's helm release secret (kind
// helm.sh/release.v1) so support engineers can see exactly what
// values + manifest version the install is running. We strip the binary
// blob (the compressed JSON release payload itself is large + opaque)
// and surface the labels which carry version + status.
func (h *SupportBundleHandler) writeHelmRelease(ctx context.Context, zw *zip.Writer, log *sectionLog) {
	if h.k8s == nil || h.namespace == "" {
		log.skipped("helm-releases.json", "k8s client not wired")
		return
	}
	lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	secrets, err := h.k8s.CoreV1().Secrets(h.namespace).List(lctx, metav1.ListOptions{
		FieldSelector: "type=helm.sh/release.v1",
	})
	if err != nil {
		log.section("helm-releases.json", err)
		return
	}
	out := make([]map[string]any, 0, len(secrets.Items))
	for _, s := range secrets.Items {
		entry := map[string]any{
			"name":            s.Name,
			"created":         s.CreationTimestamp,
			"labels":          s.Labels,
			"data_size_bytes": sumSecretBytes(s),
		}
		out = append(out, entry)
	}
	log.section("helm-releases.json", writeBundleJSON(zw, "helm-releases.json", out))
}

// writeNetworkPolicies captures the management namespace's current
// isolation posture without including full rule bodies. It is meant to
// answer whether default-deny and explicit egress/ingress policies exist.
func (h *SupportBundleHandler) writeNetworkPolicies(ctx context.Context, zw *zip.Writer, log *sectionLog) {
	if h.k8s == nil || h.namespace == "" {
		log.skipped("networkpolicies.json", "k8s client not wired")
		return
	}
	lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	policies, err := h.k8s.NetworkingV1().NetworkPolicies(h.namespace).List(lctx, metav1.ListOptions{Limit: 500})
	if err != nil {
		log.section("networkpolicies.json", err)
		return
	}
	out := make([]map[string]any, 0, len(policies.Items))
	for _, p := range policies.Items {
		out = append(out, summarizeNetworkPolicy(p))
	}
	log.section("networkpolicies.json", writeBundleJSON(zw, "networkpolicies.json", out))
}

// writeIngressCertificates summarizes externally reachable ingress hosts and
// TLS certificate validity from Kubernetes Secret metadata/cert public data.
// It never writes tls.key or raw certificate PEM bytes.
func (h *SupportBundleHandler) writeIngressCertificates(ctx context.Context, zw *zip.Writer, log *sectionLog) {
	if h.k8s == nil || h.namespace == "" {
		log.skipped("ingress-certificates.json", "k8s client not wired")
		return
	}
	lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ingresses, err := h.k8s.NetworkingV1().Ingresses(h.namespace).List(lctx, metav1.ListOptions{Limit: 500})
	if err != nil {
		log.section("ingress-certificates.json", err)
		return
	}
	secrets, err := h.k8s.CoreV1().Secrets(h.namespace).List(lctx, metav1.ListOptions{Limit: 500})
	if err != nil {
		log.section("ingress-certificates.json", err)
		return
	}
	tlsSecrets := map[string]corev1.Secret{}
	for _, s := range secrets.Items {
		if s.Type == corev1.SecretTypeTLS {
			tlsSecrets[s.Name] = s
		}
	}

	secretRefs := map[string]bool{}
	ingressOut := make([]map[string]any, 0, len(ingresses.Items))
	for _, ing := range ingresses.Items {
		entry := summarizeIngress(ing)
		for _, name := range ingressTLSSecretNames(ing) {
			secretRefs[name] = true
		}
		ingressOut = append(ingressOut, entry)
	}
	certOut := make([]map[string]any, 0, len(secretRefs))
	for name := range secretRefs {
		secret, ok := tlsSecrets[name]
		if !ok {
			certOut = append(certOut, map[string]any{
				"name":    name,
				"present": false,
			})
			continue
		}
		certOut = append(certOut, summarizeTLSSecret(secret))
	}
	payload := map[string]any{
		"ingresses":    ingressOut,
		"certificates": certOut,
	}
	log.section("ingress-certificates.json", writeBundleJSON(zw, "ingress-certificates.json", payload))
}

// writeSchemaMigrations surfaces the migrate-binary state table. The dirty
// flag is what an L3 engineer needs to see when a release is stuck on
// migration recovery — same signal the preflight Job surfaces.
