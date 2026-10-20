package config

import "github.com/alphabravocompany/astronomer-go/internal/envconfig"

// StandaloneProxyEnv carries the inputs of the `server grafana-proxy` and
// `server loki-auth` sub-commands. They are separate processes with their own
// composition root, but the environment is still read here so that
// internal/config stays the only package that touches the process environment
// (guide ch.01).
type StandaloneProxyEnv struct {
	ListenAddr        string
	GrafanaUpstream   string
	AstronomerURL     string
	GrafanaPublicPath string
	GrafanaProxyKey   string
	LokiUpstream      string
	HashesPath        string
	ACLPath           string
	QueryKeyPath      string
}

// LoadStandaloneProxyEnv reads the standalone proxy inputs from the environment.
func LoadStandaloneProxyEnv() (StandaloneProxyEnv, error) {
	v := envconfig.NewViper("")
	if err := envconfig.BindEnv(v, "listen_addr", "grafana_upstream", "astronomer_url", "grafana_public_path",
		"grafana_proxy_key", "loki_upstream", "hashes_path", "acl_path", "query_key_path"); err != nil {
		return StandaloneProxyEnv{}, err
	}
	return StandaloneProxyEnv{
		ListenAddr:        v.GetString("listen_addr"),
		GrafanaUpstream:   v.GetString("grafana_upstream"),
		AstronomerURL:     v.GetString("astronomer_url"),
		GrafanaPublicPath: v.GetString("grafana_public_path"),
		GrafanaProxyKey:   v.GetString("grafana_proxy_key"),
		LokiUpstream:      v.GetString("loki_upstream"),
		HashesPath:        v.GetString("hashes_path"),
		ACLPath:           v.GetString("acl_path"),
		QueryKeyPath:      v.GetString("query_key_path"),
	}, nil
}
