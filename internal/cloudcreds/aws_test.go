package cloudcreds

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAWSResolverStaticSessionToken(t *testing.T) {
	r := NewAWSResolver()
	got, err := r.ResolveAWS(context.Background(), map[string]string{"access_key_id": "access", "secret_access_key": "secret", "session_token": "token"})
	if err != nil || got.SessionToken != "token" || got.CanExpire {
		t.Fatalf("static temporary credentials not preserved: %+v, %v", got, err)
	}
}

func TestAWSResolverAssumeRoleCachesAndRefreshesOnceUnderRace(t *testing.T) {
	var calls atomic.Int32
	var nowMu sync.Mutex
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		if req.URL.Query().Get("Action") != "AssumeRole" || req.URL.Query().Get("RoleArn") == "" {
			t.Errorf("unexpected STS request: %s", req.URL.RawQuery)
		}
		if req.Header.Get("X-Amz-Security-Token") != "base-token" {
			t.Errorf("base session token was not signed")
		}
		nowMu.Lock()
		expires := now.Add(15 * time.Minute)
		nowMu.Unlock()
		if _, err := fmt.Fprintf(w, `<AssumeRoleResponse><AssumeRoleResult><Credentials><AccessKeyId>assumed-access</AccessKeyId><SecretAccessKey>assumed-secret</SecretAccessKey><SessionToken>assumed-token</SessionToken><Expiration>%s</Expiration></Credentials></AssumeRoleResult></AssumeRoleResponse>`, expires.Format(time.RFC3339)); err != nil {
			t.Errorf("write STS response: %v", err)
		}
	}))
	defer server.Close()
	r := NewAWSResolver()
	r.HTTPClient = server.Client()
	r.Endpoint = server.URL
	r.Now = func() time.Time { nowMu.Lock(); defer nowMu.Unlock(); return now }
	blob := map[string]string{"access_key_id": "base-access", "secret_access_key": "base-secret", "session_token": "base-token", "assume_role_arn": "arn:aws:iam::123456789012:role/test", "region": "us-east-1"}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := r.ResolveAWS(context.Background(), blob)
			if err != nil || got.SessionToken != "assumed-token" {
				t.Errorf("resolve: %+v %v", got, err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("expected one role exchange, got %d", calls.Load())
	}
	nowMu.Lock()
	now = now.Add(11 * time.Minute)
	nowMu.Unlock()
	if _, err := r.ResolveAWS(context.Background(), blob); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected bounded refresh before expiry, got %d calls", calls.Load())
	}
}

func TestAWSResolverFailuresAreTypedAndRedacted(t *testing.T) {
	secret := "do-not-leak-secret"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		if _, err := fmt.Fprintf(w, `<ErrorResponse><Error><Code>AccessDenied</Code><Message>%s</Message></Error></ErrorResponse>`, secret); err != nil {
			t.Errorf("write STS error response: %v", err)
		}
	}))
	defer server.Close()
	r := NewAWSResolver()
	r.HTTPClient = server.Client()
	r.Endpoint = server.URL
	_, err := r.ResolveAWS(context.Background(), map[string]string{"access_key_id": "access", "secret_access_key": secret, "assume_role_arn": "arn:aws:iam::123456789012:role/test"})
	typed, ok := err.(*AWSCredentialError)
	if !ok || typed.Kind != AWSFailurePermission {
		t.Fatalf("expected permission error, got %#v", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "123456789012") {
		t.Fatalf("error leaked private material: %v", err)
	}
}

func TestAWSResolverThrottleClassification(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		if _, err := fmt.Fprint(w, `<ErrorResponse><Error><Code>ThrottlingException</Code></Error></ErrorResponse>`); err != nil {
			t.Errorf("write STS throttle response: %v", err)
		}
	}))
	defer server.Close()
	r := NewAWSResolver()
	r.HTTPClient = server.Client()
	r.Endpoint = server.URL
	_, err := r.ResolveAWS(context.Background(), map[string]string{"access_key_id": "access", "secret_access_key": "secret", "assume_role_arn": "arn:aws:iam::123456789012:role/test"})
	if typed, ok := err.(*AWSCredentialError); !ok || typed.Kind != AWSFailureThrottled {
		t.Fatalf("expected throttle, got %#v", err)
	}
}
