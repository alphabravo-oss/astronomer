package charliequalification

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
)

// CandidateDeployer owns one fixed qualification environment. Implementations
// must bind every mutation to Candidate's immutable commit and four digests;
// the HTTP surface deliberately accepts no namespace, command, chart path, URL,
// values, or credential input.
type CandidateDeployer interface {
	Deploy(context.Context, Candidate) error
}

type CandidateDeployHook struct {
	token    string
	deployer CandidateDeployer
	mu       sync.Mutex
	bound    *Candidate
}

type candidateDeployRequest struct {
	Schema    string    `json:"schema"`
	Candidate Candidate `json:"candidate"`
}

type candidateDeployResponse struct {
	Accepted           bool   `json:"accepted"`
	CandidateCommit    string `json:"candidate_commit"`
	CandidateVersion   string `json:"candidate_version"`
	CentralImageDigest string `json:"central_image_digest"`
	AgentImageDigest   string `json:"agent_image_digest"`
	CentralChartDigest string `json:"central_chart_digest"`
	AgentChartDigest   string `json:"agent_chart_digest"`
}

func NewCandidateDeployHook(token string, deployer CandidateDeployer) (*CandidateDeployHook, error) {
	token = strings.TrimSpace(token)
	if len(token) < 32 || len(token) > 512 || deployer == nil {
		return nil, errors.New("candidate deploy hook requires a strong bearer token and fixed deployer")
	}
	return &CandidateDeployHook{token: token, deployer: deployer}, nil
}

func (h *CandidateDeployHook) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodPost || r.URL.Path != "/v1/candidate" {
			writeError(w, http.StatusNotFound, "route_unknown")
			return
		}
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if len(provided) != len(h.token) || subtle.ConstantTimeCompare([]byte(provided), []byte(h.token)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		h.deploy(w, r)
	})
}

func (h *CandidateDeployHook) deploy(w http.ResponseWriter, r *http.Request) {
	limited := http.MaxBytesReader(w, r.Body, maxRequestBytes)
	defer func() { _ = limited.Close() }()
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	var request candidateDeployRequest
	if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		request.Schema != "charlie.live-candidate-deploy/v1" || !validCandidate(request.Candidate) {
		writeError(w, http.StatusBadRequest, "request_invalid")
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.bound == nil || *h.bound != request.Candidate {
		if err := h.deployer.Deploy(r.Context(), request.Candidate); err != nil {
			writeError(w, http.StatusServiceUnavailable, "candidate_deploy_failed")
			return
		}
		candidate := request.Candidate
		h.bound = &candidate
	}
	writeJSON(w, http.StatusOK, candidateDeployResponse{
		Accepted: true, CandidateCommit: request.Candidate.Commit, CandidateVersion: request.Candidate.Version,
		CentralImageDigest: request.Candidate.CentralImageDigest, AgentImageDigest: request.Candidate.AgentImageDigest,
		CentralChartDigest: request.Candidate.CentralChartDigest, AgentChartDigest: request.Candidate.AgentChartDigest,
	})
}
