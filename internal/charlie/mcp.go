package charlie

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const maxMCPRequestBytes = 1 << 20

type MCPHandler struct {
	guard             *ActionGuard
	active            func(context.Context) bool
	expectedClientURI string
}

func NewMCPHandler(guard *ActionGuard, active func(context.Context) bool, expectedClientURI string) (*MCPHandler, error) {
	parsed, err := url.Parse(expectedClientURI)
	if guard == nil || active == nil || err != nil || !parsed.IsAbs() || parsed.Scheme != "spiffe" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("Charlie MCP requires an action guard, live activation, and exact SPIFFE client identity")
	}
	return &MCPHandler{guard: guard, active: active, expectedClientURI: parsed.String()}, nil
}

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

func (h *MCPHandler) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	started := time.Now()
	method := "unknown"
	statusWriter := &mcpStatusWriter{ResponseWriter: w}
	w = statusWriter
	defer func() { observeMCPCall(method, mcpHTTPOutcome(statusWriter.status), started) }()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	if request.Method != http.MethodPost || request.URL.Path != "/mcp" {
		writeMCPHTTPError(w, http.StatusNotFound, "not_found")
		return
	}
	if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
		writeMCPHTTPError(w, http.StatusUnauthorized, "unsupported_credential")
		return
	}
	if !verifiedMCPClient(request, h.expectedClientURI) {
		writeMCPHTTPError(w, http.StatusUnauthorized, "client_identity_invalid")
		return
	}
	if mediaType := strings.ToLower(strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0])); mediaType != "application/json" {
		writeMCPHTTPError(w, http.StatusUnsupportedMediaType, "content_type_invalid")
		return
	}
	limited := http.MaxBytesReader(w, request.Body, maxMCPRequestBytes)
	defer func() { _ = limited.Close() }()
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	var rpc mcpRequest
	if err := decoder.Decode(&rpc); err != nil || decoder.Decode(&struct{}{}) != io.EOF || rpc.JSONRPC != "2.0" || rpc.Method == "" {
		writeMCPResponse(w, http.StatusBadRequest, mcpResponse{JSONRPC: "2.0", ID: rpc.ID, Error: &mcpError{Code: -32600, Message: "invalid_request"}})
		return
	}
	if rpc.Method == "notifications/initialized" {
		method = rpc.Method
		if len(rpc.ID) != 0 {
			writeMCPResponse(w, http.StatusBadRequest, mcpResponse{JSONRPC: "2.0", Error: &mcpError{Code: -32600, Message: "invalid_request"}})
			return
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if !validJSONRPCID(rpc.ID) {
		writeMCPResponse(w, http.StatusBadRequest, mcpResponse{JSONRPC: "2.0", Error: &mcpError{Code: -32600, Message: "invalid_request"}})
		return
	}
	method = rpc.Method

	switch rpc.Method {
	case "initialize":
		writeMCPResponse(w, http.StatusOK, mcpResponse{JSONRPC: "2.0", ID: rpc.ID, Result: map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]string{"name": "astronomer-charlie-mcp", "version": "1.0.0"},
			"instructions":    "Discover only the current digest-pinned Astronomer management-plane tools.",
		}})
	case "tools/list":
		tools := mcpToolsFor(h.guard.executor)
		writeMCPResponse(w, http.StatusOK, mcpResponse{JSONRPC: "2.0", ID: rpc.ID, Result: map[string]any{"tools": tools, "disclosureDigest": capabilityDisclosureDigest(tools)}})
	case "tools/call":
		if !h.active(request.Context()) {
			writeMCPHTTPError(w, http.StatusServiceUnavailable, "integration_inactive")
			return
		}
		h.handleCall(w, request, rpc)
	default:
		writeMCPResponse(w, http.StatusNotFound, mcpResponse{JSONRPC: "2.0", ID: rpc.ID, Error: &mcpError{Code: -32601, Message: "method_not_found"}})
	}
}

type mcpStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *mcpStatusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *mcpStatusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func mcpHTTPOutcome(status int) string {
	switch {
	case status >= 200 && status < 300:
		return "success"
	case status == http.StatusUnauthorized:
		return "unauthorized"
	case status == http.StatusForbidden:
		return "denied"
	case status == http.StatusServiceUnavailable:
		return "inactive"
	case status == http.StatusNotFound:
		return "not_found"
	case status >= 400 && status < 500:
		return "invalid"
	default:
		return "failed"
	}
}

func (h *MCPHandler) handleCall(w http.ResponseWriter, request *http.Request, rpc mcpRequest) {
	var params struct {
		Name      string                     `json:"name"`
		Arguments map[string]json.RawMessage `json:"arguments"`
		Meta      struct {
			Action ActionEnvelope `json:"charlie/action"`
		} `json:"_meta"`
	}
	decoder := json.NewDecoder(bytes.NewReader(rpc.Params))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&params); err != nil || decoder.Decode(&struct{}{}) != io.EOF || params.Name == "" || params.Meta.Action.Capability != params.Name {
		writeMCPResponse(w, http.StatusBadRequest, mcpResponse{JSONRPC: "2.0", ID: rpc.ID, Error: &mcpError{Code: -32602, Message: "invalid_params"}})
		return
	}
	if request.Header.Get("Idempotency-Key") != params.Meta.Action.ActionID || params.Meta.Action.IdempotencyKey != params.Meta.Action.ActionID {
		writeMCPResponse(w, http.StatusBadRequest, mcpResponse{JSONRPC: "2.0", ID: rpc.ID, Error: &mcpError{Code: -32602, Message: "idempotency_key_invalid"}})
		return
	}
	arguments, err := json.Marshal(params.Arguments)
	if err != nil {
		writeMCPResponse(w, http.StatusBadRequest, mcpResponse{JSONRPC: "2.0", ID: rpc.ID, Error: &mcpError{Code: -32602, Message: "invalid_params"}})
		return
	}
	params.Meta.Action.Arguments = arguments
	result := h.guard.Execute(request.Context(), params.Meta.Action)
	status := http.StatusOK
	if !result.Allowed {
		status = http.StatusForbidden
	}
	if descriptor, found := capabilityByName(params.Name); found && descriptor.Effect == EffectWrite {
		w.Header().Set("X-Charlie-Verification-Method", capabilityVerificationMethod(descriptor))
		verificationStatus := "failed"
		if result.State == "succeeded" && result.Verified {
			verificationStatus = "succeeded"
		}
		w.Header().Set("X-Charlie-Verification-Status", verificationStatus)
	}
	writeMCPResponse(w, status, mcpResponse{JSONRPC: "2.0", ID: rpc.ID, Result: map[string]any{
		"content":           []map[string]string{{"type": "text", "text": boundedActionSummary(result)}},
		"structuredContent": result,
		"isError":           result.State == "blocked" || result.State == "failed" || result.State == "ambiguous",
	}})
}

func verifiedMCPClient(request *http.Request, expectedURI string) bool {
	if request.TLS == nil || len(request.TLS.VerifiedChains) == 0 || len(request.TLS.PeerCertificates) == 0 {
		return false
	}
	leaf := request.TLS.PeerCertificates[0]
	clientAuth := false
	for _, usage := range leaf.ExtKeyUsage {
		if usage == x509.ExtKeyUsageClientAuth {
			clientAuth = true
		}
	}
	if !clientAuth || len(leaf.URIs) != 1 || leaf.URIs[0].String() != expectedURI {
		return false
	}
	return request.TLS.VerifiedChains[0][0].Equal(leaf)
}

func validJSONRPCID(id json.RawMessage) bool {
	if len(id) == 0 || bytes.Equal(id, []byte("null")) || len(id) > 128 {
		return false
	}
	var value any
	if json.Unmarshal(id, &value) != nil {
		return false
	}
	switch value.(type) {
	case string, float64:
		return true
	default:
		return false
	}
}

func mcpTools() []map[string]any {
	return mcpToolsFor(nil)
}

func mcpToolsFor(executor CapabilityExecutor) []map[string]any {
	catalog := append(ReadCapabilityCatalog(), WriteCapabilityCatalog()...)
	sort.Slice(catalog, func(i, j int) bool { return catalog[i].Name < catalog[j].Name })
	tools := make([]map[string]any, 0, len(catalog))
	for _, capability := range catalog {
		if availability, ok := executor.(CapabilityAvailability); ok && !availability.SupportsCapability(capability.Name) {
			continue
		}
		tool := map[string]any{
			"name":        capability.Name,
			"description": capability.Description,
			"inputSchema": capabilityJSONSchema(capability),
			"annotations": map[string]any{
				"readOnlyHint":    capability.Effect == EffectRead,
				"destructiveHint": false,
				"idempotentHint":  capability.Idempotent,
				"openWorldHint":   false,
			},
			"_meta": map[string]any{
				"charlie/capability": capabilitySafetyDisclosure(capability),
				"effect":             capability.Effect, "source": capability.Source,
				"schema_version": capability.SchemaVersion, "risk": capability.Risk,
				"target_bounds": capability.TargetBounds, "impact": capability.Impact,
				"reversibility": capability.Reversibility, "rollback": capability.Rollback,
				"auto_eligible":         capability.AutoEligible,
				"destructive":           capability.Destructive,
				"requires_precondition": capability.RequiresPrecondition,
				"requires_verification": capability.RequiresVerification,
				"managed_target_access": false,
			},
		}
		tools = append(tools, tool)
	}
	return tools
}

func capabilityVerificationMethod(capability CapabilityDescriptor) string {
	return capability.Name + ".postcondition"
}

// capabilitySafetyDisclosure is the Charlie-owned, versioned extension to
// standard MCP tool annotations. Read tools can be inferred conservatively
// without it; write tools are rejected by Charlie unless every safety property
// below is present and passes central policy compilation.
func capabilitySafetyDisclosure(capability CapabilityDescriptor) map[string]any {
	disclosure := map[string]any{
		"schema":        "charlie.mcp-capability/v1",
		"name":          capability.Name,
		"effect":        capability.Effect,
		"risk":          capability.Risk,
		"destructive":   capability.Destructive,
		"auto_eligible": false,
		"timeout_ms":    capability.TimeoutSeconds * 1000,
		"reversible":    false,
	}
	if capability.Effect == EffectWrite {
		disclosure["auto_eligible"] = capability.AutoEligible
		disclosure["idempotency"] = map[string]any{"required": true, "key": "action_id"}
		disclosure["preconditions"] = []string{"Target and product policy remain within the disclosed management-plane bounds"}
		disclosure["expected_impact"] = capability.Impact
		disclosure["post_verification"] = map[string]any{"required": true, "method": capabilityVerificationMethod(capability)}
	}
	return disclosure
}

func CapabilityDisclosureDigest() string {
	return capabilityDisclosureDigest(mcpTools())
}

func capabilityDisclosureDigest(tools []map[string]any) string {
	encoded, err := json.Marshal(map[string]any{"version": "astronomer-mcp-disclosure/v1", "tools": tools})
	if err != nil {
		return ""
	}
	return digestBytes(encoded)
}

func boundedActionSummary(result ActionResult) string {
	if result.State == "succeeded" {
		return "Astronomer completed and verified the bounded action."
	}
	if result.State == "failed" || result.State == "ambiguous" {
		return "Astronomer could not verify the bounded action; no follow-on action is permitted."
	}
	return "Astronomer denied the action under current product policy: " + string(result.Code)
}

func writeMCPHTTPError(w http.ResponseWriter, status int, code string) {
	writeMCPResponse(w, status, mcpResponse{JSONRPC: "2.0", Error: &mcpError{Code: -32000, Message: code}})
}

func writeMCPResponse(w http.ResponseWriter, status int, response mcpResponse) {
	w.WriteHeader(status)
	encoder := json.NewEncoder(io.Writer(w))
	_ = encoder.Encode(response)
}
