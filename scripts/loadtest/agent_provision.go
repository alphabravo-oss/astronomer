package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
)

const maxProvisionResponseBytes = 1 << 20

// agentCredential is the one-to-one mapping between a persisted cluster row
// and the short-lived registration token minted for that cluster. The admin
// bearer used to create the fixture is deliberately not carried here.
type agentCredential struct {
	ClusterID         string
	RegistrationToken string
}

// provisionSyntheticAgentCredentials creates real imported-cluster rows and
// mints a cluster-bound registration token for each synthetic agent. It uses
// only the same authenticated public APIs as the registration wizard.
func provisionSyntheticAgentCredentials(
	ctx context.Context,
	client *http.Client,
	server string,
	adminToken string,
	count int,
	runID string,
) ([]agentCredential, error) {
	if count < 1 {
		return nil, fmt.Errorf("agent count must be at least 1")
	}
	if strings.TrimSpace(adminToken) == "" {
		return nil, fmt.Errorf("admin bearer token is required to provision synthetic agents")
	}
	base, err := normalizedManagementURL(server)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	runLabel := strings.ToLower(strings.ReplaceAll(runID, "_", "-"))
	if runLabel == "" || len(runLabel) > 20 {
		return nil, fmt.Errorf("load-test run ID must contain 1..20 RFC-1123-compatible characters")
	}
	for _, char := range runLabel {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return nil, fmt.Errorf("load-test run ID must contain 1..20 RFC-1123-compatible characters")
		}
	}
	if runLabel[0] == '-' || runLabel[len(runLabel)-1] == '-' {
		return nil, fmt.Errorf("load-test run ID must contain 1..20 RFC-1123-compatible characters")
	}

	credentials := make([]agentCredential, count)
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(registrationConcur)
	for index := 0; index < count; index++ {
		index := index
		group.Go(func() error {
			credential, provisionErr := provisionSyntheticAgentCredential(
				groupCtx, client, base, adminToken, runLabel, index,
			)
			// Preserve a created cluster ID even when token minting or response
			// validation fails, so the fail-fast path can decommission the
			// partial fixture instead of leaking it into the next scale run.
			credentials[index] = credential
			if provisionErr != nil {
				return fmt.Errorf("agent %04d: %w", index, provisionErr)
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cleanupProvisionedClusters(cleanupCtx, client, base, adminToken, credentials)
		return nil, err
	}
	for index, credential := range credentials {
		if _, err := uuid.Parse(credential.ClusterID); err != nil || credential.RegistrationToken == "" {
			return nil, fmt.Errorf("agent %04d: incomplete cluster credential mapping", index)
		}
	}
	return credentials, nil
}

func provisionSyntheticAgentCredential(
	ctx context.Context,
	client *http.Client,
	base string,
	adminToken string,
	runLabel string,
	index int,
) (agentCredential, error) {
	name := fmt.Sprintf("loadtest-%s-%04d", runLabel, index)
	createBody := map[string]any{
		"name":         name,
		"display_name": fmt.Sprintf("Load test %s agent %04d", runLabel, index),
		"environment":  "scale",
		"provider":     "loadtest",
		"distribution": "synthetic",
		"region":       "synthetic",
		"labels":       map[string]string{"astronomer.io/loadtest-run": runLabel},
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := doAdminJSON(ctx, client, http.MethodPost, base+"/api/v1/clusters/", adminToken, createBody, http.StatusCreated, &created); err != nil {
		return agentCredential{}, fmt.Errorf("create cluster: %w", err)
	}
	clusterID, err := uuid.Parse(created.Data.ID)
	if err != nil {
		return agentCredential{}, fmt.Errorf("create cluster returned an invalid ID")
	}

	var registered struct {
		Data struct {
			ClusterID string `json:"cluster_id"`
			Token     string `json:"token"`
		} `json:"data"`
	}
	registerURL := fmt.Sprintf("%s/api/v1/clusters/%s/register/", base, clusterID)
	if err := doAdminJSON(ctx, client, http.MethodPost, registerURL, adminToken, nil, http.StatusCreated, &registered); err != nil {
		return agentCredential{ClusterID: clusterID.String()}, fmt.Errorf("mint registration token: %w", err)
	}
	registeredID, err := uuid.Parse(registered.Data.ClusterID)
	if err != nil || registeredID != clusterID {
		return agentCredential{ClusterID: clusterID.String()}, fmt.Errorf("registration token response did not match its cluster")
	}
	if strings.TrimSpace(registered.Data.Token) == "" {
		return agentCredential{ClusterID: clusterID.String()}, fmt.Errorf("registration token response was empty")
	}
	return agentCredential{ClusterID: clusterID.String(), RegistrationToken: registered.Data.Token}, nil
}

func normalizedManagementURL(server string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(server), "/")
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("management-plane URL must be an absolute http(s) URL")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("management-plane URL must not contain a query or fragment")
	}
	return base, nil
}

func doAdminJSON(
	ctx context.Context,
	client *http.Client,
	method string,
	endpoint string,
	adminToken string,
	payload any,
	wantStatus int,
	destination any,
) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != wantStatus {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxProvisionResponseBytes))
		return fmt.Errorf("%s returned HTTP %d", req.URL.Path, resp.StatusCode)
	}
	if destination == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxProvisionResponseBytes))
		return nil
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxProvisionResponseBytes))
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode %s response: %w", req.URL.Path, err)
	}
	return nil
}

func cleanupProvisionedClusters(
	ctx context.Context,
	client *http.Client,
	base string,
	adminToken string,
	credentials []agentCredential,
) {
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(registrationConcur)
	for _, credential := range credentials {
		credential := credential
		if credential.ClusterID == "" {
			continue
		}
		group.Go(func() error {
			endpoint := fmt.Sprintf("%s/api/v1/clusters/%s/?force=true", base, credential.ClusterID)
			// Cleanup is best-effort and deliberately discards response content,
			// which can otherwise include environment-specific error details.
			_ = doAdminJSON(groupCtx, client, http.MethodDelete, endpoint, adminToken, nil, http.StatusAccepted, nil)
			return nil
		})
	}
	_ = group.Wait()
}
