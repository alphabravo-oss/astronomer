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

func evaluateGatekeeperCanary(ctx context.Context, client *http.Client, execution executionContext, clusterID, phase string, interval time.Duration) (result dimensionResult) {
	suffix := digest([]byte(execution.RunID + "-gatekeeper-" + phase))[:8]
	kind := "K8sAstronomerQualification" + strings.ToUpper(suffix[:1]) + suffix[1:]
	plural := strings.ToLower(kind)
	templateName := plural
	constraintName := "astr-qual-" + suffix
	namespaceName := "astr-qual-denied-" + suffix
	k8sBase := "/api/v1/clusters/" + url.PathEscape(clusterID) + "/k8s"
	templatePath := k8sBase + "/apis/templates.gatekeeper.sh/v1/constrainttemplates/" + templateName
	constraintPath := k8sBase + "/apis/constraints.gatekeeper.sh/v1beta1/" + plural + "/" + constraintName
	namespacePath := k8sBase + "/api/v1/namespaces/" + namespaceName
	templateCreated, constraintCreated, namespaceCreated := false, false, false
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cleanupErrors := []string{}
		for _, item := range []struct {
			created bool
			path    string
		}{{namespaceCreated, namespacePath}, {constraintCreated, constraintPath}, {templateCreated, templatePath}} {
			if !item.created {
				continue
			}
			if _, err := requestAPI(cleanupCtx, client, execution.Base, execution.Token, http.MethodDelete, item.path, nil, "", http.StatusOK, http.StatusAccepted, http.StatusNotFound); err != nil {
				cleanupErrors = append(cleanupErrors, err.Error())
			}
		}
		if len(cleanupErrors) > 0 {
			result = failedCanary(clusterID, fmt.Errorf("Gatekeeper canary cleanup failed: %s", strings.Join(cleanupErrors, "; ")))
		}
	}()
	rego := fmt.Sprintf(`package %s

violation[{"msg": msg}] {
  input.review.kind.kind == "Namespace"
  input.review.object.metadata.name == %q
  not input.review.object.metadata.labels["qualification.astronomer.io/allowed"]
  msg := "Astronomer qualification denial"
}`, strings.ToLower(kind), namespaceName)
	template := map[string]any{
		"apiVersion": "templates.gatekeeper.sh/v1", "kind": "ConstraintTemplate",
		"metadata": map[string]any{"name": templateName, "labels": map[string]any{"app.kubernetes.io/managed-by": "astronomer-qualification"}},
		"spec": map[string]any{
			"crd":     map[string]any{"spec": map[string]any{"names": map[string]any{"kind": kind}, "validation": map[string]any{"openAPIV3Schema": map[string]any{"type": "object"}}}},
			"targets": []any{map[string]any{"target": "admission.k8s.gatekeeper.sh", "rego": rego}},
		},
	}
	if _, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost,
		k8sBase+"/apis/templates.gatekeeper.sh/v1/constrainttemplates", template, "", http.StatusCreated); err != nil {
		return failedCanary(clusterID, fmt.Errorf("create Gatekeeper ConstraintTemplate: %w", err))
	}
	templateCreated = true
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if err := pollGatekeeperTemplate(ctx, client, execution, templatePath, interval); err != nil {
		return failedCanary(clusterID, err)
	}
	constraint := map[string]any{
		"apiVersion": "constraints.gatekeeper.sh/v1beta1", "kind": kind,
		"metadata": map[string]any{"name": constraintName, "labels": map[string]any{"app.kubernetes.io/managed-by": "astronomer-qualification"}},
		"spec":     map[string]any{"enforcementAction": "deny", "match": map[string]any{"kinds": []any{map[string]any{"apiGroups": []string{""}, "kinds": []string{"Namespace"}}}}},
	}
	if _, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost,
		k8sBase+"/apis/constraints.gatekeeper.sh/v1beta1/"+plural, constraint, "", http.StatusCreated); err != nil {
		return failedCanary(clusterID, fmt.Errorf("create Gatekeeper Constraint: %w", err))
	}
	constraintCreated = true
	deniedNamespace := map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": namespaceName}}
	allowedNamespace := map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": namespaceName, "labels": map[string]any{"qualification.astronomer.io/allowed": "true", "app.kubernetes.io/managed-by": "astronomer-qualification"}}}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		denied, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost,
			k8sBase+"/api/v1/namespaces", deniedNamespace, "", http.StatusForbidden)
		if err == nil && responseMentions(denied.Body, "Astronomer qualification denial") {
			allowed, createErr := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodPost,
				k8sBase+"/api/v1/namespaces", allowedNamespace, "", http.StatusCreated)
			if createErr != nil {
				return failedCanary(clusterID, fmt.Errorf("Gatekeeper denied the compliant namespace: %w", createErr))
			}
			namespaceCreated = true
			raw, _ := json.Marshal(map[string]any{"denied": denied.Body, "allowed": allowed.Body})
			now := time.Now().UTC()
			return dimensionResult{Name: "functional_canary", State: "PASS", Reason: "Gatekeeper denied the violating namespace and admitted the same namespace after its required label was supplied", ObservedAt: now, HTTPStatus: http.StatusForbidden, ArtifactSHA: digest(raw), SampleAt: &now, TargetClusterID: clusterID}
		}
		if denied.Status == http.StatusCreated || denied.Status == http.StatusConflict {
			namespaceCreated = true
			_, _ = requestAPI(ctx, client, execution.Base, execution.Token, http.MethodDelete, namespacePath, nil, "", http.StatusOK, http.StatusAccepted, http.StatusNotFound)
			_ = waitK8sDeleted(ctx, client, execution, namespacePath, interval)
			namespaceCreated = false
		}
		select {
		case <-ctx.Done():
			return failedCanary(clusterID, fmt.Errorf("Gatekeeper did not enforce the qualification constraint before timeout: %w", ctx.Err()))
		case <-ticker.C:
		}
	}
}

func pollGatekeeperTemplate(ctx context.Context, client *http.Client, execution executionContext, path string, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		response, err := requestAPI(ctx, client, execution.Base, execution.Token, http.MethodGet, path, nil, "", http.StatusOK)
		if err == nil {
			object, _ := response.Body.(map[string]any)
			status, _ := object["status"].(map[string]any)
			if status["created"] == true {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("Gatekeeper ConstraintTemplate did not become established: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
