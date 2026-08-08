package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMetricSourcesLoadSeparatePrivateBearerFiles(t *testing.T) {
	directory := t.TempDir()
	firstToken := filepath.Join(directory, "first.token")
	secondToken := filepath.Join(directory, "second.token")
	config := filepath.Join(directory, "metrics.json")
	if err := os.WriteFile(firstToken, []byte(strings.Repeat("a", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondToken, []byte(strings.Repeat("b", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	body := `{"sources":[{"url":"https://central.example/metrics","bearer_token_file":"` + firstToken + `"},{"url":"https://product.example/metrics","bearer_token_file":"` + secondToken + `"}]}`
	if err := os.WriteFile(config, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	sources, err := metricSourcesFromFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 || sources[0].Token != strings.Repeat("a", 32) || sources[1].Token != strings.Repeat("b", 32) {
		t.Fatalf("metric sources were not independently bound: %#v", sources)
	}
}

func TestMetricSourcesRejectPublicTokenFileAndInlineSecretField(t *testing.T) {
	directory := t.TempDir()
	token := filepath.Join(directory, "token")
	config := filepath.Join(directory, "metrics.json")
	if err := os.WriteFile(token, []byte(strings.Repeat("a", 32)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(token, 0o644); err != nil {
		t.Fatal(err)
	}
	body := `{"sources":[{"url":"https://central.example/metrics","bearer_token_file":"` + token + `"}]}`
	if err := os.WriteFile(config, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := metricSourcesFromFile(config); err == nil {
		t.Fatal("group/world-readable metrics bearer file accepted")
	}
	if err := os.Chmod(token, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(`{"sources":[{"url":"https://central.example/metrics","token":"must-not-be-inline"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := metricSourcesFromFile(config); err == nil {
		t.Fatal("inline metric secret field accepted")
	}
}

func TestFixturesRequirePrivateStrictJSONFile(t *testing.T) {
	directory := t.TempDir()
	config := filepath.Join(directory, "fixtures.json")
	body := `{"approval_once":{"approval_id":"approval-a","action_id":"action-a","capability":"astronomer.queue.retry_task","decision_request_id":"00000000-0000-4000-8000-000000000001","replay_request_id":"00000000-0000-4000-8000-000000000002"}}`
	if err := os.WriteFile(config, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	fixtures, err := fixturesFromFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if fixtures.ApprovalOnce.ApprovalID != "approval-a" || fixtures.ApprovalOnce.ActionID != "action-a" {
		t.Fatalf("fixture identifiers were not loaded exactly: %#v", fixtures.ApprovalOnce)
	}
	if err := os.Chmod(config, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := fixturesFromFile(config); err == nil {
		t.Fatal("group/world-readable qualification fixture file accepted")
	}
	if err := os.Chmod(config, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(`{"approval_once":{},"unexpected":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixturesFromFile(config); err == nil {
		t.Fatal("unknown qualification fixture field accepted")
	}
}

func TestAnswerFixturesLoadExactNonSecretProofValues(t *testing.T) {
	directory := t.TempDir()
	config := filepath.Join(directory, "fixtures.json")
	body := `{
		"versioned_rag_grounded":{
			"stimulus":{"client_session_id":"10000000-0000-4000-8000-000000000003","client_message_id":"20000000-0000-4000-8000-000000000003","abort_request_id":"40000000-0000-4000-8000-000000000003","intent":"qualification_versioned_rag","resource_type":"installation","resource_id":"qualification-rag-installation","message":"Return the corrected canary."},
			"corrected_revision_marker":"CORRECTED-REVISION-CANARY","product_version_marker":"PRODUCT-VERSION-1.1","citation_id":"chunk-version-1-1","citation_title":"Qualification guide","citation_source":"knowledge://astronomer/version-1-1#chunk=0"
		},
		"general_answer":{
			"stimulus":{"client_session_id":"10000000-0000-4000-8000-000000000004","client_message_id":"20000000-0000-4000-8000-000000000004","abort_request_id":"40000000-0000-4000-8000-000000000004","intent":"qualification_general_answer","resource_type":"management_component","resource_id":"qualification-general-component","message":"Return the general canary."},
			"expected_answer_marker":"GENERAL-KUBERNETES-CANARY"
		}
	}`
	if err := os.WriteFile(config, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	fixtures, err := fixturesFromFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if fixtures.VersionedRAGGrounded.CitationSource != "knowledge://astronomer/version-1-1#chunk=0" || fixtures.GeneralAnswer.ExpectedAnswerMarker != "GENERAL-KUBERNETES-CANARY" {
		t.Fatalf("answer fixtures were not loaded exactly: %#v %#v", fixtures.VersionedRAGGrounded, fixtures.GeneralAnswer)
	}
	if err := os.WriteFile(config, []byte(strings.Replace(body, `"citation_source":`, `"unknown":true,"citation_source":`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixturesFromFile(config); err == nil {
		t.Fatal("unknown nested answer fixture field accepted")
	}
}

func TestAlertFixturesLoadOnlyMetadataProofValues(t *testing.T) {
	directory := t.TempDir()
	config := filepath.Join(directory, "fixtures.json")
	body := `{"diagnosis_alert":{"finding_id":"10000000-0000-4000-8000-000000000010","delivery_id":"20000000-0000-4000-8000-000000000010","expected_block_code":"no_safe_action","expected_workflow_state":"blocked"}}`
	if err := os.WriteFile(config, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	fixtures, err := fixturesFromFile(config)
	if err != nil || fixtures.DiagnosisAlert.ExpectedBlockCode != "no_safe_action" {
		t.Fatalf("alert metadata fixture not loaded exactly: %#v err=%v", fixtures.DiagnosisAlert, err)
	}
	if err := os.WriteFile(config, []byte(strings.Replace(body, `"delivery_id":`, `"title":"secret","delivery_id":`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixturesFromFile(config); err == nil {
		t.Fatal("raw alert content field accepted in metadata fixture")
	}
}

func TestOptionalNoCallDwellIsExplicitAndStrict(t *testing.T) {
	value, err := optionalDuration("30s")
	if err != nil || value.String() != "30s" {
		t.Fatalf("explicit dwell was not parsed: %v %v", value, err)
	}
	if _, err := optionalDuration("forever"); err == nil {
		t.Fatal("invalid dwell accepted")
	}
}
