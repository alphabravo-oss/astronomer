package contract

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"time"

	bridgewire "github.com/alphabravocompany/astronomer-go/internal/charlie/contract/internal/wire"
)

const (
	bridgeServiceName = "charlie-agent-bridge"
	bridgePort        = 7443
	bridgeBasePath    = "/bridge/v1"
)

var namespacePattern = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$`)

// Client exposes the generated Product Bridge operations. Its generated wire
// constructor is isolated by Go's internal-package rule; this is the only
// production constructor and it accepts no host or URL.
type Client struct {
	bridgewire.ClientWithResponsesInterface
	endpoint   string
	httpClient *http.Client
	transport  *http.Transport
}

// FeatureAvailability is implemented by the backend-owned Charlie availability
// state. A caller cannot construct even the local bridge client while the
// product feature is unavailable.
type FeatureAvailability interface {
	AllowsConfiguration() bool
}

// RuntimeAvailability is deliberately stricter than FeatureAvailability. An
// installed agent is a configuration surface until the product-local
// connection is explicitly active; it must not become a runtime transport just
// because the optional feature exists.
type RuntimeAvailability interface {
	FeatureAvailability
	AllowsRuntime() bool
}

// NewLocalClient constructs an mTLS client for the fixed, cluster-local Charlie
// agent Service. Proxy environment variables are deliberately ignored so this
// traffic cannot escape through an HTTP proxy.
func NewLocalClient(availability FeatureAvailability, namespace, expectedServerIdentity string, tlsConfig *tls.Config) (*Client, error) {
	if availability == nil || !availability.AllowsConfiguration() {
		return nil, fmt.Errorf("Charlie feature is unavailable")
	}
	if !namespacePattern.MatchString(namespace) {
		return nil, fmt.Errorf("Charlie agent namespace is not a DNS label")
	}
	if tlsConfig == nil || tlsConfig.RootCAs == nil || len(tlsConfig.Certificates) == 0 {
		return nil, fmt.Errorf("Charlie Product Bridge requires product-owned mutual TLS")
	}
	identity, err := url.Parse(expectedServerIdentity)
	if err != nil || !identity.IsAbs() || identity.Scheme != "spiffe" || identity.Host == "" || identity.RawQuery != "" || identity.Fragment != "" {
		return nil, fmt.Errorf("Charlie Product Bridge requires an exact installation identity")
	}

	serverName := fmt.Sprintf("%s.%s.svc", bridgeServiceName, namespace)
	localTLS := tlsConfig.Clone()
	localTLS.MinVersion = tls.VersionTLS13
	localTLS.ServerName = serverName
	priorVerify := localTLS.VerifyConnection
	localTLS.VerifyConnection = func(state tls.ConnectionState) error {
		if priorVerify != nil {
			if err := priorVerify(state); err != nil {
				return err
			}
		}
		if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
			return fmt.Errorf("Charlie Product Bridge certificate is unverified")
		}
		leaf := state.PeerCertificates[0]
		serverAuth := false
		for _, usage := range leaf.ExtKeyUsage {
			if usage == x509.ExtKeyUsageServerAuth {
				serverAuth = true
			}
		}
		if !serverAuth || len(leaf.URIs) != 1 || leaf.URIs[0].String() != identity.String() {
			return fmt.Errorf("Charlie Product Bridge installation identity does not match")
		}
		return nil
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = localTLS
	transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = 5 * time.Second
	transport.ResponseHeaderTimeout = 15 * time.Second
	transport.ExpectContinueTimeout = time.Second
	transport.IdleConnTimeout = 90 * time.Second
	transport.MaxIdleConns = 32
	transport.MaxIdleConnsPerHost = 16
	transport.MaxConnsPerHost = 32
	httpClient := &http.Client{Transport: transport}
	endpoint := fmt.Sprintf("https://%s.%s.svc:%d%s", bridgeServiceName, namespace, bridgePort, bridgeBasePath)
	generated, err := bridgewire.NewClientWithResponses(endpoint, bridgewire.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("create local Charlie Product Bridge client: %w", err)
	}
	return &Client{ClientWithResponsesInterface: generated, endpoint: endpoint, httpClient: httpClient, transport: transport}, nil
}

// NewRuntimeClient refuses to construct a runtime transport while the
// integration is merely installed or configured. Callers should discard an
// existing client immediately when their live availability gate closes.
func NewRuntimeClient(availability RuntimeAvailability, namespace, expectedServerIdentity string, tlsConfig *tls.Config) (*Client, error) {
	if availability == nil || !availability.AllowsRuntime() {
		return nil, fmt.Errorf("Charlie runtime is inactive")
	}
	return NewLocalClient(availability, namespace, expectedServerIdentity, tlsConfig)
}

// Endpoint reports the fixed local Service endpoint for diagnostics. It never
// includes credentials or a central Charlie address.
func (c *Client) Endpoint() string {
	if c == nil {
		return ""
	}
	return c.endpoint
}

// CloseIdleConnections releases pooled local-agent connections during disable,
// certificate rotation, uninstall, or shutdown.
func (c *Client) CloseIdleConnections() {
	if c != nil && c.transport != nil {
		c.transport.CloseIdleConnections()
	}
}

// Generated Product Bridge model aliases keep consumers out of internal/wire.
type (
	Action                                   = bridgewire.Action
	ArtifactCredentialAcknowledgementRequest = bridgewire.ArtifactCredentialAcknowledgementRequest
	ArtifactCredentialClaimRequest           = bridgewire.ArtifactCredentialClaimRequest
	ArtifactCredentialLease                  = bridgewire.ArtifactCredentialLease
	ArtifactCredentialLeaseState             = bridgewire.ArtifactCredentialLeaseState
	ActivationRequest                        = bridgewire.ActivationRequest
	Approval                                 = bridgewire.Approval
	ApprovalDecision                         = bridgewire.ApprovalDecision
	ApprovalDecisionDecision                 = bridgewire.ApprovalDecisionDecision
	ApprovalManifest                         = bridgewire.ApprovalManifest
	ApprovalManifestResource                 = bridgewire.ApprovalManifestResource
	ApprovalReviewSummary                    = bridgewire.ApprovalReviewSummary
	ApprovalReviewSummaryEffect              = bridgewire.ApprovalReviewSummaryEffect
	ApprovalReviewSummaryRisk                = bridgewire.ApprovalReviewSummaryRisk
	BridgeStatus                             = bridgewire.BridgeStatus
	CreateInvestigation                      = bridgewire.CreateInvestigation
	CreateMessage                            = bridgewire.CreateMessage
	CreateSession                            = bridgewire.CreateSession
	CreateSessionActorType                   = bridgewire.CreateSessionActorType
	CredentialRevocationReceipt              = bridgewire.CredentialRevocationReceipt
	CredentialRevocationReceiptState         = bridgewire.CredentialRevocationReceiptState
	CredentialRevocationRequest              = bridgewire.CredentialRevocationRequest
	CredentialRevocationRequestReason        = bridgewire.CredentialRevocationRequestReason
	CredentialRevocationStatus               = bridgewire.CredentialRevocationStatus
	CredentialRevocationStatusPurpose        = bridgewire.CredentialRevocationStatusPurpose
	CredentialRevocationStatusState          = bridgewire.CredentialRevocationStatusState
	ErrorEnvelope                            = bridgewire.ErrorEnvelope
	FindingChange                            = bridgewire.FindingChange
	FindingChangeOperation                   = bridgewire.FindingChangeOperation
	FindingChangePage                        = bridgewire.FindingChangePage
	FindingEnvelope                          = bridgewire.FindingEnvelope
	FindingProjectionSummary                 = bridgewire.FindingProjectionSummary
	FindingSummary                           = bridgewire.FindingSummary
	FindingTransition                        = bridgewire.FindingTransition
	Health                                   = bridgewire.Health
	HistoryItem                              = bridgewire.HistoryItem
	InvestigationReceipt                     = bridgewire.InvestigationReceipt
	IdempotentCommand                        = bridgewire.IdempotentCommand
	Mode                                     = bridgewire.Mode
	ModeRequest                              = bridgewire.ModeRequest
	ModeResponse                             = bridgewire.ModeResponse
	OpaqueId                                 = bridgewire.OpaqueId
	IntegrationRediscoveryRequest            = bridgewire.RequestBridgeIntegrationRediscoveryJSONRequestBody
	ResourceReference                        = bridgewire.ResourceReference
	Session                                  = bridgewire.Session
	SessionState                             = bridgewire.SessionState
	TurnReceipt                              = bridgewire.TurnReceipt
	UntrustedContext                         = bridgewire.UntrustedContext
)

// IntegrationRediscoveryReceipt is deliberately content-free. The product
// learns that its exact installation catalog changed, but capability details
// and all observed product content remain behind the bridge boundary.
type IntegrationRediscoveryReceipt struct {
	IntegrationID       string `json:"integration_id"`
	IntegrationRevision string `json:"integration_revision"`
	DisclosureDigest    string `json:"disclosure_digest"`
	CapabilityCount     int    `json:"capability_count"`
	State               string `json:"state"`
}

const (
	ArtifactCredentialLeaseActive              = bridgewire.ArtifactCredentialLeaseStateActive
	ArtifactCredentialLeasePending             = bridgewire.ArtifactCredentialLeaseStatePending
	ArtifactCredentialLeaseRevoked             = bridgewire.ArtifactCredentialLeaseStateRevoked
	ArtifactCredentialLeaseSuperseded          = bridgewire.ArtifactCredentialLeaseStateSuperseded
	FindingChangeDelete                        = bridgewire.FindingChangeOperationDelete
	FindingChangeUpsert                        = bridgewire.FindingChangeOperationUpsert
	CredentialRevocationSchemaV1               = bridgewire.CredentialRevocationReceiptSchemaCharlieCredentialRevocationv1
	CredentialRevocationPending                = bridgewire.CredentialRevocationReceiptStatePendingCallerRevocation
	CredentialRevocationComplete               = bridgewire.CredentialRevocationReceiptStateRevoked
	CredentialRevocationProductDisconnect      = bridgewire.CredentialRevocationRequestReasonProductDisconnect
	CredentialStatePending                     = bridgewire.CredentialRevocationStatusStatePendingRevocation
	CredentialStateRevoked                     = bridgewire.CredentialRevocationStatusStateRevoked
	RevocationCredentialPurposeAgentEnrollment = bridgewire.CredentialRevocationStatusPurposeAgentEnrollment
	RevocationCredentialPurposeArtifactPull    = bridgewire.CredentialRevocationStatusPurposeArtifactPull
	RevocationCredentialPurposeProductAgent    = bridgewire.CredentialRevocationStatusPurposeProductAgent
	RevocationCredentialPurposeProductClient   = bridgewire.CredentialRevocationStatusPurposeProductClient
)

type HistoryPage struct {
	Data       []HistoryItem `json:"data"`
	NextCursor *string       `json:"next_cursor,omitempty"`
}
