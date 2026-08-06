package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charliequalification"
)

const envPrefix = "ASTRONOMER_CHARLIE_QUALIFICATION_"

func main() {
	if err := run(); err != nil {
		// The hook can read operator tokens and exercise live effects. Keep its
		// process log content-free even for malformed configuration and remote
		// failures; detailed errors stay on the authenticated loopback response.
		slog.Error("Charlie qualification hook stopped", slog.String("failure_code", "charlie.qualification_hook_failed"))
		os.Exit(1)
	}
}

func run() error {
	if err := charliequalification.ValidateAcknowledgement(os.Getenv(envPrefix + "EFFECTS_ACK")); err != nil {
		return err
	}
	address := valueOrDefault(os.Getenv(envPrefix+"LISTEN"), "127.0.0.1:9443")
	certFile := strings.TrimSpace(os.Getenv(envPrefix + "TLS_CERT_FILE"))
	keyFile := strings.TrimSpace(os.Getenv(envPrefix + "TLS_KEY_FILE"))
	if certFile == "" || keyFile == "" {
		return errors.New("TLS_CERT_FILE and TLS_KEY_FILE are required")
	}
	if err := privateFile(keyFile); err != nil {
		return fmt.Errorf("TLS key: %w", err)
	}
	hookToken, err := secret("HOOK_TOKEN")
	if err != nil {
		return err
	}
	adminToken, err := secret("ADMIN_TOKEN")
	if err != nil {
		return err
	}
	deniedToken, err := optionalSecret("DENIED_TOKEN")
	if err != nil {
		return err
	}
	metricSources, err := metricSourcesFromFile(os.Getenv(envPrefix + "METRICS_SOURCES_FILE"))
	if err != nil {
		return err
	}

	counterMetrics := map[string]string{}
	if err := optionalJSONFile(os.Getenv(envPrefix+"COUNTER_METRICS_FILE"), &counterMetrics); err != nil {
		return fmt.Errorf("counter metrics: %w", err)
	}
	var scaler charliequalification.AgentScaler
	kubeconfig := strings.TrimSpace(os.Getenv(envPrefix + "KUBECONFIG_FILE"))
	if kubeconfig != "" {
		if err := privateFile(kubeconfig); err != nil {
			return fmt.Errorf("kubeconfig: %w", err)
		}
		scaler, err = charliequalification.NewKubectlScaler(
			os.Getenv(envPrefix+"KUBECTL"), kubeconfig,
			valueOrDefault(os.Getenv(envPrefix+"AGENT_NAMESPACE"), "astronomer-charlie"),
			valueOrDefault(os.Getenv(envPrefix+"AGENT_STATEFULSET"), "charlie-agent"),
		)
		if err != nil {
			return err
		}
	}
	client, err := operatorHTTPClient(os.Getenv(envPrefix + "CA_FILE"))
	if err != nil {
		return err
	}
	driver, err := charliequalification.NewLiveDriver(charliequalification.LiveConfig{
		AstronomerURL:  strings.TrimSpace(os.Getenv(envPrefix + "ASTRONOMER_URL")),
		AdminToken:     adminToken,
		DeniedToken:    deniedToken,
		MetricSources:  metricSources,
		CounterMetrics: counterMetrics,
		AllowHTTP:      os.Getenv(envPrefix+"ALLOW_HTTP_LOOPBACK") == "1",
		AgentScaler:    scaler,
		HTTPClient:     client,
	})
	if err != nil {
		return err
	}
	hook, err := charliequalification.NewHook(hookToken, driver)
	if err != nil {
		return err
	}
	server, err := charliequalification.NewHTTPServer(address, hook.Handler())
	if err != nil {
		return err
	}
	server.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS13}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServeTLS(certFile, keyFile) }()
	slog.Info("Charlie qualification hook started",
		slog.String("event", "charlie.qualification_hook_started"),
		slog.String("transport", "tls"))
	select {
	case err := <-result:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}

func operatorHTTPClient(caFile string) (*http.Client, error) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if caFile = strings.TrimSpace(caFile); caFile != "" {
		contents, readErr := os.ReadFile(caFile)
		if readErr != nil || len(contents) > 1<<20 || !pool.AppendCertsFromPEM(contents) {
			return nil, errors.New("CA_FILE must contain a bounded PEM certificate bundle")
		}
	}
	return &http.Client{
		Timeout:       30 * time.Second,
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

func secret(name string) (string, error) {
	value, err := optionalSecret(name)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", fmt.Errorf("%s%s or %s%s_FILE is required", envPrefix, name, envPrefix, name)
	}
	return value, nil
}

func optionalSecret(name string) (string, error) {
	file := strings.TrimSpace(os.Getenv(envPrefix + name + "_FILE"))
	inline := strings.TrimSpace(os.Getenv(envPrefix + name))
	if file != "" && inline != "" {
		return "", fmt.Errorf("set only one of %s%s and %s%s_FILE", envPrefix, name, envPrefix, name)
	}
	if file == "" {
		return inline, nil
	}
	if err := privateFile(file); err != nil {
		return "", fmt.Errorf("%s file: %w", name, err)
	}
	contents, err := os.ReadFile(file)
	if err != nil || len(contents) > 16<<10 {
		return "", fmt.Errorf("cannot read bounded %s file", name)
	}
	return strings.TrimSpace(string(contents)), nil
}

func privateFile(path string) error {
	info, err := os.Stat(strings.TrimSpace(path))
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("must be a regular file not accessible by group or other")
	}
	return nil
}

func optionalJSONFile(path string, destination any) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	contents, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil || len(contents) > 64<<10 {
		return errors.New("JSON file exceeds the 64 KiB limit")
	}
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("JSON file contains trailing data")
	}
	return nil
}

type metricSourceFile struct {
	Sources []struct {
		URL             string `json:"url"`
		BearerTokenFile string `json:"bearer_token_file,omitempty"`
	} `json:"sources"`
}

func metricSourcesFromFile(path string) ([]charliequalification.MetricSource, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	if err := privateFile(path); err != nil {
		return nil, fmt.Errorf("metrics sources file: %w", err)
	}
	var configured metricSourceFile
	if err := optionalJSONFile(path, &configured); err != nil {
		return nil, fmt.Errorf("metrics sources: %w", err)
	}
	if len(configured.Sources) == 0 || len(configured.Sources) > 8 {
		return nil, errors.New("metrics sources must contain one to eight endpoints")
	}
	result := make([]charliequalification.MetricSource, 0, len(configured.Sources))
	for _, source := range configured.Sources {
		token := ""
		if source.BearerTokenFile != "" {
			if err := privateFile(source.BearerTokenFile); err != nil {
				return nil, errors.New("metrics bearer file is not private")
			}
			contents, err := os.ReadFile(source.BearerTokenFile)
			if err != nil || len(contents) > 16<<10 {
				return nil, errors.New("metrics bearer file is unavailable")
			}
			token = strings.TrimSpace(string(contents))
			if len(token) < 16 || len(token) > 16<<10 {
				return nil, errors.New("metrics bearer is invalid")
			}
		}
		result = append(result, charliequalification.MetricSource{URL: source.URL, Token: token})
	}
	return result, nil
}

func valueOrDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}
