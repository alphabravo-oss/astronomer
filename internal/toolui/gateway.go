// Package toolui separates untrusted downstream browser applications from the
// control-plane origin. It never mounts control-plane routes on tool hosts.
package toolui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/pkg/crypto"
)

const CookieName = "__Host-astronomer_tool"
const redeemPath = "/.astronomer/launch"
const sessionPurpose = "astronomer-isolated-tool-ui-v1"

var dnsName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)

type Target struct {
	Kind        string `json:"kind"`
	Cluster     string `json:"cluster,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	ServicePort string `json:"servicePort,omitempty"`
}

type session struct {
	Purpose    string    `json:"purpose"`
	Target     Target    `json:"target"`
	Host       string    `json:"host"`
	Credential string    `json:"credential"`
	Expires    time.Time `json:"expires"`
	ReturnPath string    `json:"returnPath"`
}

type Gateway struct {
	main   *url.URL
	domain string
	jwt    *auth.JWTManager
	cipher *crypto.Encryptor
	grants auth.StreamTicketBackend
	proxy  func(http.ResponseWriter, *http.Request, Target)
}

func Validate(mainURL, domain string) error {
	if domain == "" {
		return nil
	}
	u, err := url.Parse(mainURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("tool UI isolation requires an HTTPS server_url origin")
	}
	if len(domain) > 200 || !dnsName.MatchString(domain) || domain != strings.ToLower(domain) || u.Hostname() == domain || strings.HasSuffix(u.Hostname(), "."+domain) {
		return errors.New("tool_ui_domain must be a DNS suffix separate from the main server host")
	}
	mainSite, err := publicsuffix.EffectiveTLDPlusOne(u.Hostname())
	toolSite, toolErr := publicsuffix.EffectiveTLDPlusOne(domain)
	if err != nil || toolErr != nil || mainSite != toolSite {
		return errors.New("tool_ui_domain must share the server_url registrable domain for embedded UI cookies")
	}
	return nil
}

func New(mainURL, domain string, jwt *auth.JWTManager, cipher *crypto.Encryptor, grants auth.StreamTicketBackend, proxy func(http.ResponseWriter, *http.Request, Target)) (*Gateway, error) {
	if err := Validate(mainURL, domain); err != nil {
		return nil, err
	}
	if domain == "" {
		return nil, errors.New("tool UI gateway is not configured")
	}
	u, _ := url.Parse(mainURL)
	u.Path = ""
	return &Gateway{main: u, domain: domain, jwt: jwt, cipher: cipher, grants: grants, proxy: proxy}, nil
}

func (g *Gateway) host(target Target) string {
	return Host(g.domain, target)
}

// Host is stable across replicas and upgrades, and unique per logical target.
func Host(domain string, target Target) string {
	b, _ := json.Marshal(target)
	sum := sha256.Sum256(b)
	return "t-" + hex.EncodeToString(sum[:20]) + "." + domain
}

func (t Target) Prefix() string {
	switch t.Kind {
	case "grafana":
		return "/api/v1/observability/grafana/"
	case "cluster-grafana":
		return "/api/v1/clusters/" + t.Cluster + "/observability/grafana/"
	case "service":
		return "/api/v1/clusters/" + t.Cluster + "/proxy/service/" + t.Namespace + "/" + t.ServicePort + "/"
	default:
		return ""
	}
}

// Dispatch runs before main CORS, API normalization, and route matching.
func (g *Gateway) Dispatch(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g == nil {
			next.ServeHTTP(w, r)
			return
		}
		host := strings.ToLower(r.Host)
		if name, _, err := net.SplitHostPort(host); err == nil {
			host = name
		}
		if host == g.domain || strings.HasSuffix(host, "."+g.domain) {
			if r.Host != host {
				http.Error(w, "Invalid tool host", http.StatusBadRequest)
				return
			}
			g.serveTool(w, r)
			return
		}
		// Configuring permissive main CORS must never grant tool origins API access.
		if origin, err := url.Parse(r.Header.Get("Origin")); err == nil && (origin.Hostname() == g.domain || strings.HasSuffix(origin.Hostname(), "."+g.domain)) {
			http.Error(w, "tool origins cannot access the control plane", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (g *Gateway) decode(ctx context.Context, encoded, host string) (session, error) {
	var s session
	if len(encoded) > 3800 {
		return s, errors.New("invalid tool session")
	}
	plain, err := g.openToken(encoded)
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(plain, &s); err != nil {
		return s, err
	}
	if s.Purpose != sessionPurpose || s.Host != host || g.host(s.Target) != host || !s.Expires.After(time.Now()) {
		return s, errors.New("expired or out-of-scope tool session")
	}
	claims, err := g.jwt.ValidateTokenContext(ctx, s.Credential)
	if err != nil || claims.TokenType != auth.AccessToken || !claims.BrowserSession {
		return s, errors.New("parent session is not active")
	}
	return s, nil
}
