package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type apiResponse struct {
	Status   int
	Location string
	Body     any
}

type rawAPIResponse struct {
	Status      int
	ContentType string
	Body        []byte
}

func qualificationHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func requestAPI(ctx context.Context, client *http.Client, base *url.URL, token, method, path string, body any, idempotencyKey string, expected ...int) (apiResponse, error) {
	target, err := relativeAPIURL(base, path)
	if err != nil {
		return apiResponse{}, err
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return apiResponse{}, fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return apiResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return apiResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil || len(raw) > maxBodyBytes {
		return apiResponse{}, errors.New("response body could not be read within the size limit")
	}
	result := apiResponse{Status: resp.StatusCode, Location: resp.Header.Get("Location")}
	if len(bytes.TrimSpace(raw)) != 0 {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&result.Body); err != nil {
			return result, errors.New("response was not valid JSON")
		}
		if object, ok := result.Body.(map[string]any); ok {
			if explicit, exists := object["ok"]; exists && explicit == false {
				return result, errors.New("response explicitly reported ok:false")
			}
		}
	}
	for _, status := range expected {
		if resp.StatusCode == status {
			return result, nil
		}
	}
	return result, fmt.Errorf("%s %s returned HTTP %d", method, path, resp.StatusCode)
}

func requestRawAPI(ctx context.Context, client *http.Client, base *url.URL, token, method, path string, expected ...int) (rawAPIResponse, error) {
	target, err := relativeAPIURL(base, path)
	if err != nil {
		return rawAPIResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), nil)
	if err != nil {
		return rawAPIResponse{}, err
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return rawAPIResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil || len(raw) > maxBodyBytes {
		return rawAPIResponse{}, errors.New("response body could not be read within the size limit")
	}
	result := rawAPIResponse{Status: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), Body: raw}
	for _, status := range expected {
		if resp.StatusCode == status {
			return result, nil
		}
	}
	return result, fmt.Errorf("%s %s returned HTTP %d", method, path, resp.StatusCode)
}

type operationObservation struct {
	ID     string
	Status string
	Body   any
}

func operationFromResponse(response apiResponse) (operationObservation, error) {
	data, err := valueAtPath(response.Body, "data")
	if err != nil {
		return operationObservation{}, errors.New("operation response lacked data")
	}
	object, ok := data.(map[string]any)
	if !ok {
		return operationObservation{}, errors.New("operation data was not an object")
	}
	id, _ := object["id"].(string)
	status, _ := object["status"].(string)
	if id == "" || status == "" {
		return operationObservation{}, errors.New("operation response lacked id or status")
	}
	return operationObservation{ID: id, Status: strings.ToLower(status), Body: response.Body}, nil
}

func pollOperation(ctx context.Context, client *http.Client, base *url.URL, token, path, expectedID string, interval time.Duration) (operationObservation, error) {
	if expectedID == "" || interval <= 0 {
		return operationObservation{}, errors.New("operation ID and positive polling interval are required")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		response, err := requestAPI(ctx, client, base, token, http.MethodGet, path, nil, "", http.StatusOK)
		if err != nil {
			if ctx.Err() != nil {
				return operationObservation{}, fmt.Errorf("operation %s did not complete: %w", expectedID, ctx.Err())
			}
			return operationObservation{}, err
		}
		observation, err := operationFromResponse(response)
		if err != nil {
			return operationObservation{}, err
		}
		if observation.ID != expectedID {
			return operationObservation{}, fmt.Errorf("operation endpoint returned %s instead of %s", observation.ID, expectedID)
		}
		switch observation.Status {
		case "completed", "succeeded", "success":
			return observation, nil
		case "failed", "error", "canceled", "cancelled", "superseded":
			detail := operationFailureDetail(observation.Body)
			if detail != "" {
				return observation, fmt.Errorf("operation %s reached terminal state %s: %s", expectedID, observation.Status, detail)
			}
			return observation, fmt.Errorf("operation %s reached terminal state %s", expectedID, observation.Status)
		case "pending", "running", "accepted", "queued":
		default:
			return observation, fmt.Errorf("operation %s returned unknown state %q", expectedID, observation.Status)
		}
		select {
		case <-ctx.Done():
			return observation, fmt.Errorf("operation %s did not complete: %w", expectedID, ctx.Err())
		case <-ticker.C:
		}
	}
}

func operationFailureDetail(body any) string {
	data, err := objectAtPath(body, "data")
	if err != nil {
		return ""
	}
	for _, key := range []string{"deliveryMessage", "errorMessage"} {
		if value := strings.TrimSpace(stringField(data, key)); value != "" {
			return value
		}
	}
	return ""
}
