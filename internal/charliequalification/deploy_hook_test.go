package charliequalification

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type recordingCandidateDeployer struct {
	calls     int
	candidate Candidate
	deployErr error
}

func (d *recordingCandidateDeployer) Deploy(_ context.Context, candidate Candidate) error {
	d.calls++
	d.candidate = candidate
	return d.deployErr
}

func TestCandidateDeployHookBindsAndAcknowledgesExactCandidate(t *testing.T) {
	deployer := &recordingCandidateDeployer{}
	token := strings.Repeat("a", 32)
	hook, err := NewCandidateDeployHook(token, deployer)
	if err != nil {
		t.Fatal(err)
	}
	candidate := qualificationCandidate()
	body, _ := json.Marshal(candidateDeployRequest{Schema: "charlie.live-candidate-deploy/v1", Candidate: candidate})

	request := httptest.NewRequest(http.MethodPost, "/v1/candidate", strings.NewReader(string(body)))
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	hook.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || deployer.calls != 1 || deployer.candidate != candidate {
		t.Fatalf("first deploy = status %d calls %d candidate %#v", recorder.Code, deployer.calls, deployer.candidate)
	}
	var response candidateDeployResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || !response.Accepted ||
		response.CandidateCommit != candidate.Commit || response.CandidateVersion != candidate.Version ||
		response.CentralImageDigest != candidate.CentralImageDigest || response.AgentImageDigest != candidate.AgentImageDigest ||
		response.CentralChartDigest != candidate.CentralChartDigest || response.AgentChartDigest != candidate.AgentChartDigest {
		t.Fatalf("unexpected exact acknowledgement: %#v err=%v", response, err)
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/candidate", strings.NewReader(string(body)))
	request.Header.Set("Authorization", "Bearer "+token)
	recorder = httptest.NewRecorder()
	hook.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || deployer.calls != 1 {
		t.Fatalf("idempotent deploy = status %d calls %d", recorder.Code, deployer.calls)
	}

	other := candidate
	other.Commit = repeatHex('b')
	body, _ = json.Marshal(candidateDeployRequest{Schema: "charlie.live-candidate-deploy/v1", Candidate: other})
	request = httptest.NewRequest(http.MethodPost, "/v1/candidate", strings.NewReader(string(body)))
	request.Header.Set("Authorization", "Bearer "+token)
	recorder = httptest.NewRecorder()
	hook.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || deployer.calls != 2 || deployer.candidate != other {
		t.Fatalf("sequential candidate deploy = status %d calls %d candidate %#v", recorder.Code, deployer.calls, deployer.candidate)
	}
}

func TestCandidateDeployHookFailsClosed(t *testing.T) {
	deployer := &recordingCandidateDeployer{deployErr: errors.New("coded failure")}
	token := strings.Repeat("a", 32)
	hook, err := NewCandidateDeployHook(token, deployer)
	if err != nil {
		t.Fatal(err)
	}
	candidate := qualificationCandidate()
	body, _ := json.Marshal(candidateDeployRequest{Schema: "charlie.live-candidate-deploy/v1", Candidate: candidate})
	tests := map[string]struct {
		auth    string
		payload string
		status  int
	}{
		"unauthorized":  {"Bearer wrong", string(body), http.StatusUnauthorized},
		"malformed":     {"Bearer " + token, `{"schema":"charlie.live-candidate-deploy/v1","candidate":{},"extra":true}`, http.StatusBadRequest},
		"deploy failed": {"Bearer " + token, string(body), http.StatusServiceUnavailable},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/candidate", strings.NewReader(test.payload))
			request.Header.Set("Authorization", test.auth)
			recorder := httptest.NewRecorder()
			hook.Handler().ServeHTTP(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d", recorder.Code, test.status)
			}
		})
	}
}
