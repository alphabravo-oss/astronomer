// Package lokiauth is the ClusterIP ingest/query front for hosted Loki.
//
// Until ingest-token hashes exist (PR 5), every request is 401. auth_enabled
// on Loki only requires a client-supplied org; this process is what will
// bind org to a bearer. Public Ingress is also deferred to that PR.
package lokiauth

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// Config is env-driven. No Redis, Postgres, or ASTRONOMER_SECRET_KEY.
type Config struct {
	ListenAddr string
	Upstream   string
}

func ConfigFromEnv() (Config, error) {
	listen := strings.TrimSpace(os.Getenv("LISTEN_ADDR"))
	if listen == "" {
		listen = ":8080"
	}
	upstream := strings.TrimSpace(os.Getenv("LOKI_UPSTREAM"))
	if upstream == "" {
		return Config{}, fmt.Errorf("LOKI_UPSTREAM is required")
	}
	return Config{ListenAddr: listen, Upstream: upstream}, nil
}

func Run() error {
	cfg, err := ConfigFromEnv()
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           New(cfg),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return server.ListenAndServe()
}

// New returns a handler that 401s every path until token hashes exist.
func New(_ Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"status":"error","error":"loki ingest tokens are not configured"}`))
	})
	return mux
}
