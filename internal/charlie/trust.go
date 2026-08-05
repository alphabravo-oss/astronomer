package charlie

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/google/uuid"
)

const (
	localCAValidity  = 365 * 24 * time.Hour
	leafValidity     = 90 * 24 * time.Hour
	DualTrustOverlap = 24 * time.Hour
)

var serviceDNSLabelPattern = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$`)

type LocalTrustConfig struct {
	InstallationID  string
	BridgeServerDNS string
	MCPServerDNS    string
	Now             time.Time
}

type LocalPublicTrust struct {
	CACertificatePEM        string `json:"ca_certificate_pem"`
	BridgeClientCertificate string `json:"bridge_client_certificate_pem"`
	BridgeClientIdentityURI string `json:"bridge_client_identity_uri"`
	MCPServerCertificate    string `json:"mcp_server_certificate_pem"`
	MCPServerIdentityURI    string `json:"mcp_server_identity_uri"`
}

// AgentTrustMaterial is written directly to a Kubernetes Secret. The json:"-"
// tags prevent an accidental handler serialization from disclosing private
// material while a reconciler explicitly maps the fields to Secret.Data.
type AgentTrustMaterial struct {
	CACertificatePEM        string `json:"-"`
	BridgeServerCertificate string `json:"-"`
	BridgeServerPrivateKey  string `json:"-"`
	BridgeServerIdentityURI string `json:"-"`
	MCPClientCertificate    string `json:"-"`
	MCPClientPrivateKey     string `json:"-"`
	MCPClientIdentityURI    string `json:"-"`
}

type GeneratedLocalTrust struct {
	Public              LocalPublicTrust
	EncryptedLocalTrust string
	Agent               AgentTrustMaterial      `json:"-"`
	Astronomer          AstronomerTrustMaterial `json:"-"`
	ExpiresAt           time.Time
}

// AstronomerTrustMaterial is ephemeral output for direct materialization into
// the server's private MCP listener Secret. The same key is also sealed inside
// EncryptedLocalTrust; json:"-" prevents accidental API serialization.
type AstronomerTrustMaterial struct {
	BridgeClientPrivateKey string `json:"-"`
	MCPServerPrivateKey    string `json:"-"`
}

type RotatedLocalTrust struct {
	GeneratedLocalTrust
	OverlapUntil  time.Time
	CurrentCAPEM  string
	PreviousCAPEM string
}

type persistedLocalTrust struct {
	CAPrivateKey           string               `json:"ca_private_key_pem"`
	BridgeClientPrivateKey string               `json:"bridge_client_private_key_pem"`
	MCPServerPrivateKey    string               `json:"mcp_server_private_key_pem"`
	Previous               *persistedLocalTrust `json:"previous,omitempty"`
	PreviousTrustExpiresAt *time.Time           `json:"previous_trust_expires_at,omitempty"`
}

type LocalIdentityURIs struct {
	BridgeClient string
	BridgeServer string
	MCPServer    string
	MCPClient    string
}

func ExpectedLocalIdentityURIs(installationID string) (LocalIdentityURIs, error) {
	id, err := uuid.Parse(strings.TrimSpace(installationID))
	if err != nil {
		return LocalIdentityURIs{}, fmt.Errorf("Charlie installation identity must be a UUID")
	}
	base := "spiffe://astronomer.local/installations/" + id.String() + "/charlie/"
	return LocalIdentityURIs{
		BridgeClient: base + "bridge-client", BridgeServer: base + "bridge-server",
		MCPServer: base + "mcp-server", MCPClient: base + "mcp-client",
	}, nil
}

// GenerateLocalTrust creates four purpose-separated mTLS leaf identities. The
// two agent-side keys are returned only for direct Secret materialization. The
// Astronomer-owned reusable keys are sealed immediately with auth.Encryptor.
func GenerateLocalTrust(encryptor *auth.Encryptor, cfg LocalTrustConfig) (GeneratedLocalTrust, error) {
	if encryptor == nil {
		return GeneratedLocalTrust{}, fmt.Errorf("Astronomer encryption is required for Charlie local trust")
	}
	if !validServiceDNS(cfg.BridgeServerDNS) || !validServiceDNS(cfg.MCPServerDNS) || cfg.BridgeServerDNS == cfg.MCPServerDNS {
		return GeneratedLocalTrust{}, fmt.Errorf("Charlie bridge and MCP require distinct exact service DNS names")
	}
	identities, err := ExpectedLocalIdentityURIs(cfg.InstallationID)
	if err != nil {
		return GeneratedLocalTrust{}, err
	}
	now := cfg.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	caCertPEM, caKeyPEM, caCert, caKey, err := createLocalCA(now)
	if err != nil {
		return GeneratedLocalTrust{}, err
	}
	bridgeClientCert, bridgeClientKey, err := issueLeaf(caCert, caKey, now, "astronomer-bridge-client", nil, identities.BridgeClient, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	if err != nil {
		return GeneratedLocalTrust{}, err
	}
	bridgeServerCert, bridgeServerKey, err := issueLeaf(caCert, caKey, now, "charlie-agent-bridge-server", []string{cfg.BridgeServerDNS}, identities.BridgeServer, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	if err != nil {
		return GeneratedLocalTrust{}, err
	}
	mcpServerCert, mcpServerKey, err := issueLeaf(caCert, caKey, now, "astronomer-mcp-server", []string{cfg.MCPServerDNS}, identities.MCPServer, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	if err != nil {
		return GeneratedLocalTrust{}, err
	}
	mcpClientCert, mcpClientKey, err := issueLeaf(caCert, caKey, now, "charlie-agent-mcp-client", nil, identities.MCPClient, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	if err != nil {
		return GeneratedLocalTrust{}, err
	}

	persisted, err := json.Marshal(persistedLocalTrust{
		CAPrivateKey: caKeyPEM, BridgeClientPrivateKey: bridgeClientKey,
		MCPServerPrivateKey: mcpServerKey,
	})
	if err != nil {
		return GeneratedLocalTrust{}, fmt.Errorf("encode Charlie local trust: %w", err)
	}
	sealed, err := encryptor.Encrypt(string(persisted))
	if err != nil {
		return GeneratedLocalTrust{}, fmt.Errorf("encrypt Charlie local trust: %w", err)
	}

	return GeneratedLocalTrust{
		Public: LocalPublicTrust{
			CACertificatePEM: caCertPEM, BridgeClientCertificate: bridgeClientCert,
			BridgeClientIdentityURI: identities.BridgeClient,
			MCPServerCertificate:    mcpServerCert, MCPServerIdentityURI: identities.MCPServer,
		},
		EncryptedLocalTrust: sealed,
		Agent: AgentTrustMaterial{
			CACertificatePEM: caCertPEM, BridgeServerCertificate: bridgeServerCert,
			BridgeServerPrivateKey: bridgeServerKey, BridgeServerIdentityURI: identities.BridgeServer,
			MCPClientCertificate: mcpClientCert, MCPClientPrivateKey: mcpClientKey,
			MCPClientIdentityURI: identities.MCPClient,
		},
		Astronomer: AstronomerTrustMaterial{BridgeClientPrivateKey: bridgeClientKey, MCPServerPrivateKey: mcpServerKey},
		ExpiresAt:  now.Add(leafValidity),
	}, nil
}

// RotateLocalTrust issues a fresh four-certificate set while retaining the
// immediately previous local trust for exactly 24 hours. Both CA certificates
// are published during overlap; previous private material stays encrypted.
func RotateLocalTrust(encryptor *auth.Encryptor, cfg LocalTrustConfig, previous GeneratedLocalTrust) (RotatedLocalTrust, error) {
	if encryptor == nil || strings.TrimSpace(previous.EncryptedLocalTrust) == "" || strings.TrimSpace(previous.Public.CACertificatePEM) == "" {
		return RotatedLocalTrust{}, fmt.Errorf("previous encrypted Charlie trust is required for rotation")
	}
	now := cfg.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
		cfg.Now = now
	}
	current, err := GenerateLocalTrust(encryptor, cfg)
	if err != nil {
		return RotatedLocalTrust{}, err
	}
	previousPersisted, err := decryptPersistedTrust(encryptor, previous.EncryptedLocalTrust)
	if err != nil {
		return RotatedLocalTrust{}, fmt.Errorf("decrypt previous Charlie trust: %w", err)
	}
	currentPersisted, err := decryptPersistedTrust(encryptor, current.EncryptedLocalTrust)
	if err != nil {
		return RotatedLocalTrust{}, fmt.Errorf("decrypt current Charlie trust: %w", err)
	}
	previousPersisted.Previous = nil
	previousPersisted.PreviousTrustExpiresAt = nil
	overlapUntil := now.Add(DualTrustOverlap)
	currentPersisted.Previous = &previousPersisted
	currentPersisted.PreviousTrustExpiresAt = &overlapUntil
	encoded, err := json.Marshal(currentPersisted)
	if err != nil {
		return RotatedLocalTrust{}, fmt.Errorf("encode rotated Charlie trust: %w", err)
	}
	current.EncryptedLocalTrust, err = encryptor.Encrypt(string(encoded))
	if err != nil {
		return RotatedLocalTrust{}, fmt.Errorf("encrypt rotated Charlie trust: %w", err)
	}
	currentCA := current.Public.CACertificatePEM
	previousCA := previous.Public.CACertificatePEM
	current.Public.CACertificatePEM = currentCA + previousCA
	current.Agent.CACertificatePEM = currentCA + previousCA
	return RotatedLocalTrust{
		GeneratedLocalTrust: current, OverlapUntil: overlapUntil,
		CurrentCAPEM: currentCA, PreviousCAPEM: previousCA,
	}, nil
}

// PruneExpiredPreviousTrust removes encrypted previous key material once the
// overlap expires. It is idempotent before and after expiry.
func PruneExpiredPreviousTrust(encryptor *auth.Encryptor, sealed string, now time.Time) (string, bool, error) {
	if encryptor == nil {
		return "", false, fmt.Errorf("Astronomer encryption is required for Charlie trust pruning")
	}
	persisted, err := decryptPersistedTrust(encryptor, sealed)
	if err != nil {
		return "", false, err
	}
	if persisted.Previous == nil || persisted.PreviousTrustExpiresAt == nil || now.UTC().Before(persisted.PreviousTrustExpiresAt.UTC()) {
		return sealed, false, nil
	}
	persisted.Previous = nil
	persisted.PreviousTrustExpiresAt = nil
	raw, err := json.Marshal(persisted)
	if err != nil {
		return "", false, err
	}
	pruned, err := encryptor.Encrypt(string(raw))
	return pruned, err == nil, err
}

func decryptPersistedTrust(encryptor *auth.Encryptor, sealed string) (persistedLocalTrust, error) {
	plaintext, err := encryptor.Decrypt(sealed)
	if err != nil {
		return persistedLocalTrust{}, err
	}
	var persisted persistedLocalTrust
	if err := json.Unmarshal([]byte(plaintext), &persisted); err != nil {
		return persistedLocalTrust{}, err
	}
	return persisted, nil
}

func createLocalCA(now time.Time) (string, string, *x509.Certificate, ed25519.PrivateKey, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", nil, nil, fmt.Errorf("generate Charlie local CA key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return "", "", nil, nil, err
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "Astronomer Charlie Local CA"},
		NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(localCAValidity),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		return "", "", nil, nil, fmt.Errorf("create Charlie local CA: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return "", "", nil, nil, fmt.Errorf("parse Charlie local CA: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return "", "", nil, nil, fmt.Errorf("marshal Charlie local CA key: %w", err)
	}
	return pemString("CERTIFICATE", der), pemString("PRIVATE KEY", keyDER), cert, privateKey, nil
}

func issueLeaf(ca *x509.Certificate, caKey ed25519.PrivateKey, now time.Time, commonName string, dnsNames []string, identity string, usages []x509.ExtKeyUsage) (string, string, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate %s key: %w", commonName, err)
	}
	serial, err := randomSerial()
	if err != nil {
		return "", "", err
	}
	identityURI, err := url.Parse(identity)
	if err != nil || identityURI.Scheme != "spiffe" || identityURI.Host != "astronomer.local" {
		return "", "", fmt.Errorf("invalid %s installation identity URI", commonName)
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: commonName},
		NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(leafValidity),
		DNSNames: dnsNames, ExtKeyUsage: usages,
		URIs:     []*url.URL{identityURI},
		KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, publicKey, caKey)
	if err != nil {
		return "", "", fmt.Errorf("create %s certificate: %w", commonName, err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return "", "", fmt.Errorf("marshal %s key: %w", commonName, err)
	}
	return pemString("CERTIFICATE", der), pemString("PRIVATE KEY", keyDER), nil
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("generate certificate serial: %w", err)
	}
	return serial, nil
}

func pemString(kind string, der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}))
}

func validServiceDNS(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "*") || net.ParseIP(value) != nil || !strings.HasSuffix(value, ".svc") || len(value) > 253 {
		return false
	}
	labels := strings.Split(value, ".")
	if len(labels) < 3 {
		return false
	}
	for _, label := range labels {
		if !serviceDNSLabelPattern.MatchString(label) {
			return false
		}
	}
	return true
}
