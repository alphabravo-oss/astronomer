package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func evaluateCertManagerCanary(ctx context.Context, client *http.Client, execution executionContext, clusterID, phase string, interval time.Duration) (result dimensionResult) {
	namespace := memberNamespace(execution.Config, clusterID)
	if namespace == "" {
		return failedCanary(clusterID, fmt.Errorf("no qualification namespace is configured for the cert-manager target"))
	}
	name := "astr-qual-" + digest([]byte(execution.RunID + "-cert-manager-" + phase))[:12]
	base := "/api/v1/clusters/" + url.PathEscape(clusterID) + "/k8s/apis/cert-manager.io/v1/namespaces/" + url.PathEscape(namespace)
	secretPath := "/api/v1/clusters/" + url.PathEscape(clusterID) + "/k8s/api/v1/namespaces/" + url.PathEscape(namespace) + "/secrets/" + url.PathEscape(name)
	createdIssuer, createdCertificate := false, false
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cleanupErrors := []string{}
		for _, item := range []struct {
			created bool
			path    string
		}{{createdCertificate, base + "/certificates/" + url.PathEscape(name)}, {createdIssuer, base + "/issuers/" + url.PathEscape(name)}, {true, secretPath}} {
			if !item.created {
				continue
			}
			if _, err := requestAPI(cleanupCtx, client, execution.Base, execution.Token, http.MethodDelete, item.path, nil, "", http.StatusOK, http.StatusAccepted, http.StatusNotFound); err != nil {
				cleanupErrors = append(cleanupErrors, err.Error())
			}
		}
		if len(cleanupErrors) > 0 {
			result = failedCanary(clusterID, fmt.Errorf("cert-manager canary cleanup failed: %s", strings.Join(cleanupErrors, "; ")))
		}
	}()
	issuer := map[string]any{
		"apiVersion": "cert-manager.io/v1", "kind": "Issuer",
		"metadata": map[string]any{"name": name, "namespace": namespace, "labels": map[string]any{"app.kubernetes.io/managed-by": "astronomer-qualification"}},
		"spec":     map[string]any{"selfSigned": map[string]any{}},
	}
	if _, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost, base+"/issuers", issuer, "", http.StatusCreated); err != nil {
		return failedCanary(clusterID, fmt.Errorf("create self-signed cert-manager Issuer: %w", err))
	}
	createdIssuer = true
	certificate := map[string]any{
		"apiVersion": "cert-manager.io/v1", "kind": "Certificate",
		"metadata": map[string]any{"name": name, "namespace": namespace, "labels": map[string]any{"app.kubernetes.io/managed-by": "astronomer-qualification"}},
		"spec": map[string]any{
			"secretName": name, "duration": "1h", "renewBefore": "15m", "commonName": name + ".example.invalid",
			"dnsNames": []string{name + ".example.invalid"}, "issuerRef": map[string]any{"name": name, "kind": "Issuer"},
		},
	}
	if _, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost, base+"/certificates", certificate, "", http.StatusCreated); err != nil {
		return failedCanary(clusterID, fmt.Errorf("create cert-manager Certificate: %w", err))
	}
	createdCertificate = true
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		certificateResponse, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, base+"/certificates/"+url.PathEscape(name), nil, "", http.StatusOK)
		if err != nil {
			return failedCanary(clusterID, fmt.Errorf("read cert-manager Certificate: %w", err))
		}
		certificateObject, _ := certificateResponse.Body.(map[string]any)
		if certificateReady(certificateObject) {
			secret, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, secretPath, nil, "", http.StatusOK)
			if err != nil {
				return failedCanary(clusterID, fmt.Errorf("read issued TLS Secret: %w", err))
			}
			secretObject, _ := secret.Body.(map[string]any)
			data, _ := secretObject["data"].(map[string]any)
			if stringField(secretObject, "type") != "kubernetes.io/tls" || stringField(data, "tls.crt") == "" || stringField(data, "tls.key") == "" {
				return failedCanary(clusterID, fmt.Errorf("issued Secret did not contain a TLS certificate and private key"))
			}
			raw, _ := json.Marshal(map[string]any{"certificate": certificateResponse.Body, "secret_type": secretObject["type"]})
			now := time.Now().UTC()
			return dimensionResult{Name: "functional_canary", State: "PASS", Reason: fmt.Sprintf("cert-manager issued a Ready self-signed certificate and populated TLS Secret %s/%s", namespace, name), ObservedAt: now, HTTPStatus: http.StatusOK, ArtifactSHA: digest(raw), SampleAt: &now, TargetClusterID: clusterID}
		}
		select {
		case <-ctx.Done():
			return failedCanary(clusterID, fmt.Errorf("cert-manager Certificate %s/%s did not become Ready: %w", namespace, name, ctx.Err()))
		case <-ticker.C:
		}
	}
}

func memberNamespace(config qualificationConfig, clusterID string) string {
	for _, target := range config.MemberTargets {
		if target.ClusterID == clusterID {
			return target.Namespace
		}
	}
	return ""
}

func certificateReady(object map[string]any) bool {
	status, _ := object["status"].(map[string]any)
	conditions, _ := status["conditions"].([]any)
	for _, item := range conditions {
		condition, _ := item.(map[string]any)
		if stringField(condition, "type") == "Ready" && strings.EqualFold(stringField(condition, "status"), "True") {
			return true
		}
	}
	return false
}
