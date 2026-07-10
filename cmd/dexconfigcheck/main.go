// dexconfigcheck validates a prepared Dex runtime document without ever
// printing its credential-bearing content. It is shipped in astronomer-shell
// and used by the Helm preflight hook before a Secret-volume cutover.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const version = "1"

type config struct {
	Issuer  string `yaml:"issuer"`
	Storage struct {
		Type   string `yaml:"type"`
		Config struct {
			InCluster bool `yaml:"inCluster"`
		} `yaml:"config"`
	} `yaml:"storage"`
	Web struct {
		HTTP string `yaml:"http"`
	} `yaml:"web"`
	OAuth2 struct {
		SkipApprovalScreen bool `yaml:"skipApprovalScreen"`
	} `yaml:"oauth2"`
	StaticClients []struct {
		ID           string   `yaml:"id"`
		RedirectURIs []string `yaml:"redirectURIs"`
		Secret       string   `yaml:"secret"`
		Public       bool     `yaml:"public"`
		TrustedPeers []string `yaml:"trustedPeers"`
	} `yaml:"staticClients"`
	Connectors []struct {
		Type   string         `yaml:"type"`
		ID     string         `yaml:"id"`
		Name   string         `yaml:"name"`
		Config map[string]any `yaml:"config"`
	} `yaml:"connectors"`
	Expiry struct {
		IDTokens    string `yaml:"idTokens"`
		SigningKeys string `yaml:"signingKeys"`
		Refresh     struct {
			ReuseInterval     string `yaml:"reuseInterval"`
			ValidIfNotUsedFor string `yaml:"validIfNotUsedFor"`
			AbsoluteLifetime  string `yaml:"absoluteLifetime"`
		} `yaml:"refreshTokens"`
	} `yaml:"expiry"`
	Logger struct {
		Level  string `yaml:"level"`
		Format string `yaml:"format"`
	} `yaml:"logger"`
	Frontend struct {
		Issuer  string `yaml:"issuer"`
		LogoURL string `yaml:"logoURL"`
		Dir     string `yaml:"dir"`
		Theme   string `yaml:"theme"`
	} `yaml:"frontend"`
	GRPC struct {
		Addr string `yaml:"addr"`
	} `yaml:"grpc"`
	Telemetry struct {
		HTTP string `yaml:"http"`
	} `yaml:"telemetry"`
}

func main() {
	maxBytes := flag.Int64("max-bytes", 1<<20, "maximum decoded YAML bytes")
	showVersion := flag.Bool("version", false, "print validator contract version")
	flag.Parse()
	if *showVersion {
		_, _ = io.WriteString(os.Stdout, version+"\n")
		return
	}
	if *maxBytes < 1 || *maxBytes > 16<<20 {
		fail("invalid size bound")
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, *maxBytes+1))
	if err != nil || int64(len(raw)) > *maxBytes {
		fail("Dex configuration exceeds the bounded input size")
	}
	var document config
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil {
		fail("Dex configuration is not valid YAML")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		fail("Dex configuration must contain exactly one YAML document")
	}
	if err := validate(document); err != nil {
		fail(err.Error())
	}
}

func fail(message string) {
	_, _ = io.WriteString(os.Stderr, "dexconfigcheck: "+message+"\n")
	os.Exit(1)
}

func validate(document config) error {
	if err := secureURL(document.Issuer); err != nil {
		return fmt.Errorf("issuer must be a credential-free canonical https URL")
	}
	if document.Storage.Type != "kubernetes" || !document.Storage.Config.InCluster {
		return fmt.Errorf("storage must be kubernetes with inCluster enabled")
	}
	host, port, err := net.SplitHostPort(document.Web.HTTP)
	if err != nil || strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
		return fmt.Errorf("web.http must be an explicit host:port listener")
	}
	if len(document.StaticClients) == 0 {
		return fmt.Errorf("at least one static client is required")
	}
	ids := map[string]struct{}{}
	for _, client := range document.StaticClients {
		if strings.TrimSpace(client.ID) == "" || strings.TrimSpace(client.ID) != client.ID {
			return fmt.Errorf("static client ids must be canonical non-empty strings")
		}
		if _, exists := ids[client.ID]; exists {
			return fmt.Errorf("static client ids must be unique")
		}
		ids[client.ID] = struct{}{}
		if len(client.RedirectURIs) == 0 {
			return fmt.Errorf("every static client requires a redirect URI")
		}
		for _, redirect := range client.RedirectURIs {
			if err := redirectURL(redirect); err != nil {
				return fmt.Errorf("static client redirect URI is invalid")
			}
		}
		if client.Public && client.Secret != "" {
			return fmt.Errorf("public static clients must not have a secret")
		}
		if !client.Public && client.Secret == "" {
			return fmt.Errorf("confidential static clients require a secret")
		}
		for _, peer := range client.TrustedPeers {
			if strings.TrimSpace(peer) == "" || strings.TrimSpace(peer) != peer || peer == client.ID {
				return fmt.Errorf("static client trustedPeers is invalid")
			}
		}
	}
	for _, client := range document.StaticClients {
		peers := map[string]struct{}{}
		for _, peer := range client.TrustedPeers {
			if _, exists := ids[peer]; !exists {
				return fmt.Errorf("static client trustedPeers references an unknown client")
			}
			if _, duplicate := peers[peer]; duplicate {
				return fmt.Errorf("static client trustedPeers contains a duplicate")
			}
			peers[peer] = struct{}{}
		}
	}
	connectorIDs := map[string]struct{}{}
	for _, connector := range document.Connectors {
		if strings.TrimSpace(connector.Type) == "" || strings.TrimSpace(connector.ID) == "" || strings.TrimSpace(connector.Name) == "" || connector.Config == nil {
			return fmt.Errorf("connectors require non-empty type, id, name, and config")
		}
		if _, duplicate := connectorIDs[connector.ID]; duplicate {
			return fmt.Errorf("connector ids must be unique")
		}
		connectorIDs[connector.ID] = struct{}{}
	}
	return nil
}

func secureURL(raw string) error {
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || strings.TrimSpace(raw) != raw || u.Host != strings.ToLower(u.Host) {
		return fmt.Errorf("invalid URL")
	}
	return nil
}

func redirectURL(raw string) error {
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || strings.TrimSpace(raw) != raw || u.Scheme != strings.ToLower(u.Scheme) || u.Host != strings.ToLower(u.Host) {
		return fmt.Errorf("invalid URL")
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1") {
		return nil
	}
	return fmt.Errorf("insecure URL")
}
