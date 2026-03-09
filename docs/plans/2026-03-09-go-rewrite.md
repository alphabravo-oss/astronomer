# Astronomer Go Rewrite Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Rewrite the Astronomer multi-cluster Kubernetes management platform from Python/Django to Go, producing three binaries (server, agent, worker) that are API-compatible with the existing Next.js frontend.

**Architecture:** Chi HTTP router + pgxpool/sqlc database layer + Asynq workers + WebSocket tunnel. Server/agent communicate over a multiplexed JSON WebSocket protocol. Three-tier RBAC (Global/Cluster/Project) with JWT auth.

**Tech Stack:** Go 1.23, Chi v5, pgxpool, sqlc, Asynq, nhooyr.io/websocket, client-go, helm.sh/helm/v3, golang-jwt/jwt/v5, cobra, viper, slog, golang-migrate, go-playground/validator

---

## Phase 1: Scaffold & Foundation

### Task 1: Initialize Go module and project structure

**Files:**
- Create: `go.mod`
- Create: `cmd/server/main.go`
- Create: `cmd/agent/main.go`
- Create: `cmd/worker/main.go`
- Create: `internal/server/server.go`
- Create: `internal/server/routes.go`
- Create: `internal/config/config.go`
- Create: `pkg/version/version.go`

**Step 1: Initialize Go module**

Run:
```bash
cd /home/mj/code/astronomer-all/astronomer-go
go mod init github.com/alphabravocompany/astronomer-go
```

**Step 2: Create directory structure**

Run:
```bash
mkdir -p cmd/{server,agent,worker}
mkdir -p internal/{server/middleware,handler,tunnel,auth,rbac,worker/tasks,agent,db/{sqlc,queries,migrations},config}
mkdir -p pkg/{protocol,version}
mkdir -p api
mkdir -p deploy/{docker,k8s,nginx}
```

**Step 3: Create pkg/version/version.go**

```go
package version

var (
    Version   = "dev"
    GitCommit = "unknown"
    BuildDate = "unknown"
)
```

**Step 4: Create internal/config/config.go**

```go
package config

import (
    "fmt"
    "strings"

    "github.com/spf13/viper"
)

type Config struct {
    Env        string `mapstructure:"env"`
    Debug      bool   `mapstructure:"debug"`
    ServerAddr string `mapstructure:"server_addr"`

    // Database
    DatabaseURL string `mapstructure:"database_url"`

    // Redis
    RedisURL       string `mapstructure:"redis_url"`
    CeleryBrokerURL string `mapstructure:"celery_broker_url"`

    // Auth
    SecretKey             string `mapstructure:"secret_key"`
    SessionTimeoutMinutes int    `mapstructure:"session_timeout_minutes"`
    AgentTokenExpiryHours int    `mapstructure:"agent_token_expiry_hours"`
    EncryptionKey         string `mapstructure:"encryption_key"`

    // CORS
    CORSAllowedOrigins []string `mapstructure:"cors_allowed_origins"`

    // Agent image
    AgentImageRepository string `mapstructure:"agent_image_repository"`
    AgentImageTag        string `mapstructure:"agent_image_tag"`

    // OAuth
    GitHubClientID     string `mapstructure:"github_client_id"`
    GitHubClientSecret string `mapstructure:"github_client_secret"`
    GoogleClientID     string `mapstructure:"google_client_id"`
    GoogleClientSecret string `mapstructure:"google_client_secret"`
    OIDCIssuer         string `mapstructure:"oidc_issuer"`
    OIDCClientID       string `mapstructure:"oidc_client_id"`
    OIDCClientSecret   string `mapstructure:"oidc_client_secret"`

    // Logging
    LogLevel string `mapstructure:"log_level"`
}

func Load() (*Config, error) {
    v := viper.New()

    // Defaults matching Python settings.py
    v.SetDefault("env", "development")
    v.SetDefault("debug", false)
    v.SetDefault("server_addr", ":8000")
    v.SetDefault("database_url", "postgres://astronomer:astronomer@localhost:5432/astronomer?sslmode=disable")
    v.SetDefault("redis_url", "redis://localhost:6379/0")
    v.SetDefault("celery_broker_url", "redis://localhost:6379/1")
    v.SetDefault("secret_key", "CHANGE-ME-IN-PRODUCTION")
    v.SetDefault("session_timeout_minutes", 60)
    v.SetDefault("agent_token_expiry_hours", 24)
    v.SetDefault("encryption_key", "")
    v.SetDefault("cors_allowed_origins", []string{"http://localhost:3000"})
    v.SetDefault("agent_image_repository", "localhost:5000/astronomer/agent")
    v.SetDefault("agent_image_tag", "latest")
    v.SetDefault("log_level", "info")

    // Environment variable mapping: ASTRONOMER_* prefix
    v.SetEnvPrefix("ASTRONOMER")
    v.AutomaticEnv()
    v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

    // Also support the Django-style env vars for backwards compat
    _ = v.BindEnv("database_url", "DATABASE_URL")
    _ = v.BindEnv("redis_url", "REDIS_URL")
    _ = v.BindEnv("celery_broker_url", "CELERY_BROKER_URL")
    _ = v.BindEnv("secret_key", "DJANGO_SECRET_KEY")
    _ = v.BindEnv("debug", "DJANGO_DEBUG")
    _ = v.BindEnv("env", "DJANGO_ENV")
    _ = v.BindEnv("cors_allowed_origins", "CORS_ALLOWED_ORIGINS")
    _ = v.BindEnv("encryption_key", "ASTRONOMER_ENCRYPTION_KEY")
    _ = v.BindEnv("github_client_id", "GITHUB_CLIENT_ID")
    _ = v.BindEnv("github_client_secret", "GITHUB_CLIENT_SECRET")
    _ = v.BindEnv("google_client_id", "GOOGLE_CLIENT_ID")
    _ = v.BindEnv("google_client_secret", "GOOGLE_CLIENT_SECRET")
    _ = v.BindEnv("oidc_issuer", "OIDC_ISSUER")
    _ = v.BindEnv("oidc_client_id", "OIDC_CLIENT_ID")
    _ = v.BindEnv("oidc_client_secret", "OIDC_CLIENT_SECRET")
    _ = v.BindEnv("agent_image_repository", "AGENT_IMAGE_REPOSITORY")
    _ = v.BindEnv("agent_image_tag", "AGENT_IMAGE_TAG")
    _ = v.BindEnv("log_level", "LOG_LEVEL")
    _ = v.BindEnv("session_timeout_minutes", "SESSION_TIMEOUT_MINUTES")
    _ = v.BindEnv("agent_token_expiry_hours", "AGENT_TOKEN_EXPIRY_HOURS")

    var cfg Config
    if err := v.Unmarshal(&cfg); err != nil {
        return nil, fmt.Errorf("unmarshal config: %w", err)
    }
    return &cfg, nil
}
```

**Step 5: Create internal/server/server.go**

```go
package server

import (
    "context"
    "log/slog"
    "net/http"
    "time"

    "github.com/alphabravocompany/astronomer-go/internal/config"
)

type Server struct {
    cfg    *config.Config
    router http.Handler
    log    *slog.Logger
}

func New(cfg *config.Config, log *slog.Logger) *Server {
    s := &Server{
        cfg: cfg,
        log: log,
    }
    s.router = s.routes()
    return s
}

func (s *Server) Start(ctx context.Context) error {
    srv := &http.Server{
        Addr:         s.cfg.ServerAddr,
        Handler:      s.router,
        ReadTimeout:  15 * time.Second,
        WriteTimeout: 30 * time.Second,
        IdleTimeout:  60 * time.Second,
    }

    errCh := make(chan error, 1)
    go func() {
        s.log.Info("server starting", "addr", s.cfg.ServerAddr)
        errCh <- srv.ListenAndServe()
    }()

    select {
    case err := <-errCh:
        return err
    case <-ctx.Done():
        s.log.Info("shutting down server")
        shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
        defer cancel()
        return srv.Shutdown(shutdownCtx)
    }
}
```

**Step 6: Create internal/server/routes.go**

```go
package server

import (
    "encoding/json"
    "net/http"
    "time"

    "github.com/go-chi/chi/v5"
    chimw "github.com/go-chi/chi/v5/middleware"
    "github.com/go-chi/cors"
    "github.com/alphabravocompany/astronomer-go/pkg/version"
)

func (s *Server) routes() http.Handler {
    r := chi.NewRouter()

    // Global middleware
    r.Use(chimw.RequestID)
    r.Use(chimw.RealIP)
    r.Use(chimw.Recoverer)
    r.Use(chimw.Timeout(30 * time.Second))

    // CORS
    r.Use(cors.Handler(cors.Options{
        AllowedOrigins:   s.cfg.CORSAllowedOrigins,
        AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
        AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Request-ID", "X-CSRF-Token"},
        ExposedHeaders:   []string{"X-Request-ID"},
        AllowCredentials: true,
        MaxAge:           300,
    }))

    // Health check (public)
    r.Get("/health/", s.handleHealth)
    r.Get("/health", s.handleHealth)

    // API v1 routes
    r.Route("/api/v1", func(r chi.Router) {
        // Bootstrap (public)
        r.Get("/bootstrap/", s.handleBootstrapStatus)
        r.Post("/bootstrap/complete/", s.handleBootstrapComplete)

        // All other routes will be added in subsequent tasks
    })

    return r
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]any{
        "status":  "ok",
        "version": version.Version,
        "time":    time.Now().UTC().Format(time.RFC3339),
    })
}

func (s *Server) handleBootstrapStatus(w http.ResponseWriter, r *http.Request) {
    // Placeholder - will be implemented in Phase 3
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]any{
        "bootstrapped":  false,
        "server_url":    "",
        "platform_name": "Astronomer",
    })
}

func (s *Server) handleBootstrapComplete(w http.ResponseWriter, r *http.Request) {
    // Placeholder - will be implemented in Phase 3
    http.Error(w, "not implemented", http.StatusNotImplemented)
}
```

**Step 7: Create cmd/server/main.go**

```go
package main

import (
    "context"
    "log/slog"
    "os"
    "os/signal"
    "syscall"

    "github.com/alphabravocompany/astronomer-go/internal/config"
    "github.com/alphabravocompany/astronomer-go/internal/server"
    "github.com/alphabravocompany/astronomer-go/pkg/version"
)

func main() {
    // Load config
    cfg, err := config.Load()
    if err != nil {
        slog.Error("failed to load config", "error", err)
        os.Exit(1)
    }

    // Setup structured logging
    level := slog.LevelInfo
    switch cfg.LogLevel {
    case "debug":
        level = slog.LevelDebug
    case "warn":
        level = slog.LevelWarn
    case "error":
        level = slog.LevelError
    }
    log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
    slog.SetDefault(log)

    log.Info("starting astronomer server",
        "version", version.Version,
        "commit", version.GitCommit,
        "env", cfg.Env,
    )

    // Graceful shutdown context
    ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer cancel()

    // Create and start server
    srv := server.New(cfg, log)
    if err := srv.Start(ctx); err != nil {
        log.Error("server error", "error", err)
        os.Exit(1)
    }
}
```

**Step 8: Create cmd/agent/main.go (scaffold)**

```go
package main

import (
    "fmt"
    "os"

    "github.com/spf13/cobra"
    "github.com/alphabravocompany/astronomer-go/pkg/version"
)

func main() {
    rootCmd := &cobra.Command{
        Use:     "astronomer-agent",
        Short:   "Astronomer cluster agent",
        Version: version.Version,
    }

    connectCmd := &cobra.Command{
        Use:   "connect",
        Short: "Connect to the Astronomer management plane",
        RunE: func(cmd *cobra.Command, args []string) error {
            fmt.Println("agent connect: not yet implemented")
            return nil
        },
    }

    rootCmd.AddCommand(connectCmd)

    if err := rootCmd.Execute(); err != nil {
        os.Exit(1)
    }
}
```

**Step 9: Create cmd/worker/main.go (scaffold)**

```go
package main

import (
    "fmt"
    "os"

    "github.com/alphabravocompany/astronomer-go/pkg/version"
)

func main() {
    fmt.Printf("astronomer-worker %s: not yet implemented\n", version.Version)
    os.Exit(0)
}
```

**Step 10: Add dependencies and verify compilation**

Run:
```bash
cd /home/mj/code/astronomer-all/astronomer-go
go get github.com/go-chi/chi/v5
go get github.com/go-chi/cors
go get github.com/spf13/cobra
go get github.com/spf13/viper
go mod tidy
go build ./cmd/...
```
Expected: All three binaries compile successfully.

**Step 11: Commit**

```bash
git init
git add .
git commit -m "feat: scaffold Go project with server, agent, worker entrypoints"
```

---

### Task 2: Database connection pool and health check

**Files:**
- Create: `internal/db/db.go`
- Modify: `internal/server/server.go` — inject DB pool
- Modify: `internal/server/routes.go` — real health check with DB ping
- Create: `internal/server/server_test.go`

**Step 1: Write the failing test**

Create `internal/server/server_test.go`:
```go
package server_test

import (
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/alphabravocompany/astronomer-go/internal/config"
    "github.com/alphabravocompany/astronomer-go/internal/server"
    "log/slog"
)

func TestHealthEndpoint(t *testing.T) {
    cfg := &config.Config{
        ServerAddr:         ":0",
        CORSAllowedOrigins: []string{"http://localhost:3000"},
    }
    log := slog.Default()
    srv := server.New(cfg, log)

    req := httptest.NewRequest(http.MethodGet, "/health/", nil)
    w := httptest.NewRecorder()
    srv.ServeHTTP(w, req)

    if w.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d", w.Code)
    }

    var body map[string]any
    if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
        t.Fatalf("decode body: %v", err)
    }
    if body["status"] != "ok" {
        t.Fatalf("expected status ok, got %v", body["status"])
    }
}

func TestBootstrapStatusEndpoint(t *testing.T) {
    cfg := &config.Config{
        ServerAddr:         ":0",
        CORSAllowedOrigins: []string{"http://localhost:3000"},
    }
    log := slog.Default()
    srv := server.New(cfg, log)

    req := httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap/", nil)
    w := httptest.NewRecorder()
    srv.ServeHTTP(w, req)

    if w.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d", w.Code)
    }
}
```

**Step 2: Expose ServeHTTP on Server**

Add to `internal/server/server.go`:
```go
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    s.router.ServeHTTP(w, r)
}
```

**Step 3: Create internal/db/db.go**

```go
package db

import (
    "context"
    "fmt"
    "time"

    "github.com/jackc/pgx/v5/pgxpool"
)

func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
    cfg, err := pgxpool.ParseConfig(databaseURL)
    if err != nil {
        return nil, fmt.Errorf("parse database url: %w", err)
    }

    cfg.MaxConns = 25
    cfg.MinConns = 5
    cfg.MaxConnLifetime = 30 * time.Minute
    cfg.MaxConnIdleTime = 5 * time.Minute
    cfg.HealthCheckPeriod = 30 * time.Second

    pool, err := pgxpool.NewWithConfig(ctx, cfg)
    if err != nil {
        return nil, fmt.Errorf("create pool: %w", err)
    }

    // Verify connection
    if err := pool.Ping(ctx); err != nil {
        pool.Close()
        return nil, fmt.Errorf("ping database: %w", err)
    }

    return pool, nil
}
```

**Step 4: Add pgx dependency and run tests**

Run:
```bash
cd /home/mj/code/astronomer-all/astronomer-go
go get github.com/jackc/pgx/v5
go mod tidy
go test ./internal/server/ -v
```
Expected: Both tests PASS.

**Step 5: Commit**

```bash
git add .
git commit -m "feat: add database pool, health check, and server tests"
```

---

### Task 3: sqlc setup and initial migration (users table)

**Files:**
- Create: `sqlc.yaml`
- Create: `internal/db/migrations/001_initial.up.sql`
- Create: `internal/db/migrations/001_initial.down.sql`
- Create: `internal/db/queries/users.sql`

**Step 1: Install sqlc (if not present)**

Run:
```bash
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
```

**Step 2: Create sqlc.yaml**

```yaml
version: "2"
sql:
  - engine: "postgresql"
    queries: "internal/db/queries"
    schema: "internal/db/migrations"
    gen:
      go:
        package: "sqlc"
        out: "internal/db/sqlc"
        sql_package: "pgx/v5"
        emit_json_tags: true
        emit_prepared_queries: false
        emit_interface: true
        emit_exact_table_names: false
        emit_empty_slices: true
        overrides:
          - db_type: "uuid"
            go_type: "github.com/google/uuid.UUID"
          - db_type: "jsonb"
            go_type: "json.RawMessage"
            import: "encoding/json"
          - db_type: "inet"
            go_type: "string"
          - db_type: "timestamptz"
            go_type: "time.Time"
            import: "time"
```

**Step 3: Create initial migration (core tables matching Django models)**

Create `internal/db/migrations/001_initial.up.sql`:

```sql
-- Enable UUID extension
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ============================================================
-- Platform Configuration (singleton, pk=1)
-- ============================================================
CREATE TABLE platform_configuration (
    id              INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    server_url      VARCHAR(500) NOT NULL DEFAULT '',
    platform_name   VARCHAR(255) NOT NULL DEFAULT 'Astronomer',
    telemetry_enabled BOOLEAN NOT NULL DEFAULT false,
    bootstrapped_at TIMESTAMPTZ
);

-- ============================================================
-- Users (replaces Django auth_user)
-- ============================================================
CREATE TABLE users (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email       VARCHAR(254) NOT NULL UNIQUE,
    username    VARCHAR(150) NOT NULL UNIQUE,
    first_name  VARCHAR(150) NOT NULL DEFAULT '',
    last_name   VARCHAR(150) NOT NULL DEFAULT '',
    password    VARCHAR(128) NOT NULL DEFAULT '',
    is_active   BOOLEAN NOT NULL DEFAULT true,
    is_staff    BOOLEAN NOT NULL DEFAULT false,
    is_superuser BOOLEAN NOT NULL DEFAULT false,
    last_login  TIMESTAMPTZ,
    date_joined TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_users_email ON users (email);
CREATE INDEX idx_users_username ON users (username);
CREATE INDEX idx_users_created_at ON users (created_at);

-- ============================================================
-- SSO Configuration
-- ============================================================
CREATE TABLE sso_configurations (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider                VARCHAR(16) NOT NULL UNIQUE,
    is_enabled              BOOLEAN NOT NULL DEFAULT false,
    display_name            VARCHAR(255) NOT NULL DEFAULT '',
    config                  JSONB NOT NULL DEFAULT '{}',
    client_id               VARCHAR(255) NOT NULL DEFAULT '',
    client_secret_encrypted TEXT NOT NULL DEFAULT '',
    allowed_organizations   JSONB NOT NULL DEFAULT '[]',
    allowed_domains         JSONB NOT NULL DEFAULT '[]',
    auto_create_users       BOOLEAN NOT NULL DEFAULT true,
    default_global_role_id  UUID,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- API Tokens
-- ============================================================
CREATE TABLE api_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        VARCHAR(128) NOT NULL,
    token_hash  VARCHAR(128) NOT NULL UNIQUE,
    prefix      VARCHAR(8) NOT NULL,
    expires_at  TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    is_revoked  BOOLEAN NOT NULL DEFAULT false,
    scopes      JSONB NOT NULL DEFAULT '[]',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_api_tokens_user_revoked ON api_tokens (user_id, is_revoked);
CREATE INDEX idx_api_tokens_hash ON api_tokens (token_hash);

-- ============================================================
-- Clusters
-- ============================================================
CREATE TABLE clusters (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name              VARCHAR(128) NOT NULL UNIQUE,
    display_name      VARCHAR(255) NOT NULL,
    description       TEXT NOT NULL DEFAULT '',
    status            VARCHAR(16) NOT NULL DEFAULT 'pending',
    api_server_url    VARCHAR(512) NOT NULL DEFAULT '',
    ca_certificate    TEXT NOT NULL DEFAULT '',
    environment       VARCHAR(16) NOT NULL DEFAULT 'development',
    region            VARCHAR(64) NOT NULL DEFAULT '',
    provider          VARCHAR(16) NOT NULL DEFAULT 'other',
    labels            JSONB NOT NULL DEFAULT '{}',
    annotations       JSONB NOT NULL DEFAULT '{}',
    distribution      VARCHAR(32) NOT NULL DEFAULT '',
    agent_version     VARCHAR(32) NOT NULL DEFAULT '',
    last_heartbeat    TIMESTAMPTZ,
    kubernetes_version VARCHAR(32) NOT NULL DEFAULT '',
    node_count        INTEGER NOT NULL DEFAULT 0,
    created_by_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_clusters_name ON clusters (name);
CREATE INDEX idx_clusters_status ON clusters (status);
CREATE INDEX idx_clusters_status_env ON clusters (status, environment);
CREATE INDEX idx_clusters_provider_region ON clusters (provider, region);
CREATE INDEX idx_clusters_heartbeat ON clusters (last_heartbeat);
CREATE INDEX idx_clusters_created_at ON clusters (created_at);

-- ============================================================
-- Cluster Registration Tokens
-- ============================================================
CREATE TABLE cluster_registration_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id  UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    token       VARCHAR(128) NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    is_used     BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_reg_tokens_token_used ON cluster_registration_tokens (token, is_used);
CREATE INDEX idx_reg_tokens_expires ON cluster_registration_tokens (expires_at);

-- ============================================================
-- Cluster Health Status (one-to-one)
-- ============================================================
CREATE TABLE cluster_health_statuses (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id          UUID NOT NULL UNIQUE REFERENCES clusters(id) ON DELETE CASCADE,
    cpu_usage_percent   DOUBLE PRECISION NOT NULL DEFAULT 0,
    memory_usage_percent DOUBLE PRECISION NOT NULL DEFAULT 0,
    pod_count           INTEGER NOT NULL DEFAULT 0,
    node_count          INTEGER NOT NULL DEFAULT 0,
    conditions          JSONB NOT NULL DEFAULT '[]',
    last_check          TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- Cluster Registry Config (one-to-one, air-gapped support)
-- ============================================================
CREATE TABLE cluster_registry_configs (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id          UUID NOT NULL UNIQUE REFERENCES clusters(id) ON DELETE CASCADE,
    private_registry_url VARCHAR(500) NOT NULL DEFAULT '',
    registry_username   VARCHAR(255) NOT NULL DEFAULT '',
    registry_password   VARCHAR(255) NOT NULL DEFAULT '',
    insecure            BOOLEAN NOT NULL DEFAULT false,
    ca_bundle           TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- Projects
-- ============================================================
CREATE TABLE projects (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(128) NOT NULL,
    display_name    VARCHAR(255) NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    namespaces      JSONB NOT NULL DEFAULT '[]',
    resource_quota  JSONB NOT NULL DEFAULT '{}',
    created_by_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (name, cluster_id)
);

CREATE INDEX idx_projects_cluster_name ON projects (cluster_id, name);
CREATE INDEX idx_projects_created_at ON projects (created_at);

-- ============================================================
-- RBAC: Global Roles
-- ============================================================
CREATE TABLE global_roles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(128) NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    permissions JSONB NOT NULL DEFAULT '{}',
    rules       JSONB NOT NULL DEFAULT '[]',
    is_builtin  BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- RBAC: Global Role Bindings
-- ============================================================
CREATE TABLE global_role_bindings (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID REFERENCES users(id) ON DELETE CASCADE,
    "group"     VARCHAR(255) NOT NULL DEFAULT '',
    role_id     UUID NOT NULL REFERENCES global_roles(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, role_id),
    UNIQUE ("group", role_id)
);

CREATE INDEX idx_grb_group ON global_role_bindings ("group");

-- ============================================================
-- RBAC: Cluster Roles
-- ============================================================
CREATE TABLE cluster_roles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(128) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    permissions JSONB NOT NULL DEFAULT '{}',
    rules       JSONB NOT NULL DEFAULT '[]',
    is_builtin  BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- RBAC: Cluster Role Bindings
-- ============================================================
CREATE TABLE cluster_role_bindings (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID REFERENCES users(id) ON DELETE CASCADE,
    "group"     VARCHAR(255) NOT NULL DEFAULT '',
    role_id     UUID NOT NULL REFERENCES cluster_roles(id) ON DELETE CASCADE,
    cluster_id  UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, role_id, cluster_id),
    UNIQUE ("group", role_id, cluster_id)
);

CREATE INDEX idx_crb_cluster_user ON cluster_role_bindings (cluster_id, user_id);
CREATE INDEX idx_crb_group ON cluster_role_bindings ("group");

-- ============================================================
-- RBAC: Project Roles
-- ============================================================
CREATE TABLE project_roles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(128) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    permissions JSONB NOT NULL DEFAULT '{}',
    rules       JSONB NOT NULL DEFAULT '[]',
    is_builtin  BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- RBAC: Project Role Bindings
-- ============================================================
CREATE TABLE project_role_bindings (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID REFERENCES users(id) ON DELETE CASCADE,
    "group"     VARCHAR(255) NOT NULL DEFAULT '',
    role_id     UUID NOT NULL REFERENCES project_roles(id) ON DELETE CASCADE,
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, role_id, project_id),
    UNIQUE ("group", role_id, project_id)
);

CREATE INDEX idx_prb_project_user ON project_role_bindings (project_id, user_id);
CREATE INDEX idx_prb_group ON project_role_bindings ("group");

-- Add FK from sso_configurations to global_roles (deferred)
ALTER TABLE sso_configurations
    ADD CONSTRAINT fk_sso_default_role
    FOREIGN KEY (default_global_role_id) REFERENCES global_roles(id) ON DELETE SET NULL;

-- ============================================================
-- Alerting: Notification Channels
-- ============================================================
CREATE TABLE notification_channels (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(255) NOT NULL,
    channel_type    VARCHAR(16) NOT NULL,
    configuration   JSONB NOT NULL,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_by_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_notif_channels_type_enabled ON notification_channels (channel_type, enabled);

-- ============================================================
-- Alerting: Alert Rules
-- ============================================================
CREATE TABLE alert_rules (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                VARCHAR(255) NOT NULL,
    cluster_id          UUID REFERENCES clusters(id) ON DELETE CASCADE,
    rule_type           VARCHAR(16) NOT NULL,
    configuration       JSONB NOT NULL,
    severity            VARCHAR(16) NOT NULL DEFAULT 'warning',
    enabled             BOOLEAN NOT NULL DEFAULT true,
    cooldown_minutes    INTEGER NOT NULL DEFAULT 15,
    created_by_id       UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_alert_rules_type_enabled ON alert_rules (rule_type, enabled);
CREATE INDEX idx_alert_rules_severity_enabled ON alert_rules (severity, enabled);
CREATE INDEX idx_alert_rules_cluster_enabled ON alert_rules (cluster_id, enabled);

-- Alert Rules <-> Notification Channels (M2M)
CREATE TABLE alert_rule_channels (
    alert_rule_id           UUID NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    notification_channel_id UUID NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    PRIMARY KEY (alert_rule_id, notification_channel_id)
);

-- ============================================================
-- Alerting: Alert Events
-- ============================================================
CREATE TABLE alert_events (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_id             UUID NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    cluster_id          UUID REFERENCES clusters(id) ON DELETE SET NULL,
    status              VARCHAR(16) NOT NULL DEFAULT 'firing',
    message             TEXT NOT NULL,
    details             JSONB NOT NULL DEFAULT '{}',
    fired_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at         TIMESTAMPTZ,
    acknowledged_by_id  UUID REFERENCES users(id) ON DELETE SET NULL,
    acknowledged_at     TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_alert_events_rule_status ON alert_events (rule_id, status);
CREATE INDEX idx_alert_events_cluster_status ON alert_events (cluster_id, status);
CREATE INDEX idx_alert_events_status_fired ON alert_events (status, fired_at);
CREATE INDEX idx_alert_events_fired ON alert_events (fired_at);

-- ============================================================
-- Alerting: Alert Silences
-- ============================================================
CREATE TABLE alert_silences (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_id         UUID REFERENCES alert_rules(id) ON DELETE CASCADE,
    cluster_id      UUID REFERENCES clusters(id) ON DELETE CASCADE,
    reason          TEXT NOT NULL,
    starts_at       TIMESTAMPTZ NOT NULL,
    ends_at         TIMESTAMPTZ NOT NULL,
    created_by_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_alert_silences_window ON alert_silences (starts_at, ends_at);
CREATE INDEX idx_alert_silences_rule_cluster ON alert_silences (rule_id, cluster_id);

-- ============================================================
-- Catalog: Helm Repositories
-- ============================================================
CREATE TABLE helm_repositories (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(255) NOT NULL UNIQUE,
    url             VARCHAR(500) NOT NULL,
    repo_type       VARCHAR(20) NOT NULL DEFAULT 'helm',
    description     TEXT NOT NULL DEFAULT '',
    is_default      BOOLEAN NOT NULL DEFAULT false,
    auth_type       VARCHAR(20) NOT NULL DEFAULT 'none',
    auth_config     JSONB NOT NULL DEFAULT '{}',
    enabled         BOOLEAN NOT NULL DEFAULT true,
    last_synced_at  TIMESTAMPTZ,
    created_by_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_helm_repos_type_enabled ON helm_repositories (repo_type, enabled);

-- ============================================================
-- Catalog: Helm Charts
-- ============================================================
CREATE TABLE helm_charts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    repository_id   UUID NOT NULL REFERENCES helm_repositories(id) ON DELETE CASCADE,
    name            VARCHAR(255) NOT NULL,
    display_name    VARCHAR(255) NOT NULL DEFAULT '',
    description     TEXT NOT NULL DEFAULT '',
    icon_url        VARCHAR(500) NOT NULL DEFAULT '',
    home_url        VARCHAR(500) NOT NULL DEFAULT '',
    category        VARCHAR(100) NOT NULL DEFAULT '',
    keywords        JSONB NOT NULL DEFAULT '[]',
    maintainers     JSONB NOT NULL DEFAULT '[]',
    deprecated      BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (repository_id, name)
);

CREATE INDEX idx_helm_charts_category ON helm_charts (category);
CREATE INDEX idx_helm_charts_deprecated ON helm_charts (deprecated);

-- ============================================================
-- Catalog: Helm Chart Versions
-- ============================================================
CREATE TABLE helm_chart_versions (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chart_id            UUID NOT NULL REFERENCES helm_charts(id) ON DELETE CASCADE,
    version             VARCHAR(100) NOT NULL,
    app_version         VARCHAR(100) NOT NULL DEFAULT '',
    digest              VARCHAR(256) NOT NULL DEFAULT '',
    urls                JSONB NOT NULL DEFAULT '[]',
    values_schema       JSONB NOT NULL DEFAULT '{}',
    default_values      TEXT NOT NULL DEFAULT '',
    readme              TEXT NOT NULL DEFAULT '',
    created_at_upstream TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (chart_id, version)
);

-- ============================================================
-- Catalog: Installed Charts
-- ============================================================
CREATE TABLE installed_charts (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id          UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    chart_version_id    UUID REFERENCES helm_chart_versions(id) ON DELETE SET NULL,
    release_name        VARCHAR(255) NOT NULL,
    namespace           VARCHAR(255) NOT NULL DEFAULT 'default',
    values_override     TEXT NOT NULL DEFAULT '',
    status              VARCHAR(50) NOT NULL DEFAULT 'pending_install',
    revision            INTEGER NOT NULL DEFAULT 1,
    notes               TEXT NOT NULL DEFAULT '',
    installed_by_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    request_id          UUID,
    tool_slug           VARCHAR(50),
    preset_used         VARCHAR(20),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, release_name, namespace)
);

CREATE INDEX idx_installed_charts_cluster_status ON installed_charts (cluster_id, status);
CREATE INDEX idx_installed_charts_release ON installed_charts (release_name);
CREATE INDEX idx_installed_charts_tool_slug ON installed_charts (tool_slug);

-- ============================================================
-- Backups: Storage Config
-- ============================================================
CREATE TABLE backup_storage_configs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(255) NOT NULL,
    storage_type    VARCHAR(20) NOT NULL DEFAULT 's3',
    bucket          VARCHAR(255) NOT NULL,
    prefix          VARCHAR(255) NOT NULL DEFAULT 'astronomer-backups/',
    region          VARCHAR(50) NOT NULL DEFAULT '',
    endpoint_url    VARCHAR(500) NOT NULL DEFAULT '',
    access_key      VARCHAR(255) NOT NULL DEFAULT '',
    secret_key      VARCHAR(255) NOT NULL DEFAULT '',
    is_default      BOOLEAN NOT NULL DEFAULT false,
    created_by_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- Backups: Backups
-- ============================================================
CREATE TABLE backups (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                VARCHAR(255) NOT NULL,
    storage_id          UUID NOT NULL REFERENCES backup_storage_configs(id) ON DELETE RESTRICT,
    backup_type         VARCHAR(20) NOT NULL DEFAULT 'full',
    status              VARCHAR(20) NOT NULL DEFAULT 'pending',
    file_path           VARCHAR(500) NOT NULL DEFAULT '',
    file_size_bytes     BIGINT NOT NULL DEFAULT 0,
    database_tables     JSONB NOT NULL DEFAULT '[]',
    started_at          TIMESTAMPTZ,
    completed_at        TIMESTAMPTZ,
    error_message       TEXT NOT NULL DEFAULT '',
    created_by_id       UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- Backups: Schedules
-- ============================================================
CREATE TABLE backup_schedules (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                VARCHAR(255) NOT NULL,
    storage_id          UUID NOT NULL REFERENCES backup_storage_configs(id) ON DELETE RESTRICT,
    backup_type         VARCHAR(20) NOT NULL DEFAULT 'full',
    cron_expression     VARCHAR(100) NOT NULL DEFAULT '0 2 * * *',
    retention_count     INTEGER NOT NULL DEFAULT 30,
    enabled             BOOLEAN NOT NULL DEFAULT true,
    last_backup_id      UUID REFERENCES backups(id) ON DELETE SET NULL,
    created_by_id       UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- Backups: Restore Operations
-- ============================================================
CREATE TABLE restore_operations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    backup_id       UUID NOT NULL REFERENCES backups(id) ON DELETE CASCADE,
    status          VARCHAR(20) NOT NULL DEFAULT 'pending',
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    error_message   TEXT NOT NULL DEFAULT '',
    initiated_by_id UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- Security: Pod Security Templates
-- ============================================================
CREATE TABLE pod_security_templates (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                    VARCHAR(255) NOT NULL UNIQUE,
    description             TEXT NOT NULL DEFAULT '',
    is_default              BOOLEAN NOT NULL DEFAULT false,
    enforce_level           VARCHAR(20) NOT NULL DEFAULT 'baseline',
    enforce_version         VARCHAR(20) NOT NULL DEFAULT 'latest',
    audit_level             VARCHAR(20) NOT NULL DEFAULT 'restricted',
    audit_version           VARCHAR(20) NOT NULL DEFAULT 'latest',
    warn_level              VARCHAR(20) NOT NULL DEFAULT 'restricted',
    warn_version            VARCHAR(20) NOT NULL DEFAULT 'latest',
    exempt_usernames        JSONB NOT NULL DEFAULT '[]',
    exempt_runtime_classes  JSONB NOT NULL DEFAULT '[]',
    exempt_namespaces       JSONB NOT NULL DEFAULT '[]',
    created_by_id           UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- Security: Cluster Security Policies (one-to-one)
-- ============================================================
CREATE TABLE cluster_security_policies (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL UNIQUE REFERENCES clusters(id) ON DELETE CASCADE,
    template_id     UUID NOT NULL REFERENCES pod_security_templates(id) ON DELETE RESTRICT,
    applied_at      TIMESTAMPTZ,
    sync_status     VARCHAR(20) NOT NULL DEFAULT 'pending',
    error_message   TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- Security: Scan Results
-- ============================================================
CREATE TABLE security_scan_results (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    scan_type       VARCHAR(50) NOT NULL,
    status          VARCHAR(20) NOT NULL DEFAULT 'running',
    summary         JSONB NOT NULL DEFAULT '{}',
    results         JSONB NOT NULL DEFAULT '[]',
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ,
    initiated_by_id UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- Tools: Cluster Tools
-- ============================================================
CREATE TABLE cluster_tools (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug                VARCHAR(50) NOT NULL UNIQUE,
    name                VARCHAR(128) NOT NULL,
    description         TEXT NOT NULL DEFAULT '',
    icon                VARCHAR(64) NOT NULL DEFAULT '',
    category            VARCHAR(20) NOT NULL,
    charts              JSONB NOT NULL DEFAULT '[]',
    version_constraint  VARCHAR(64) NOT NULL DEFAULT '',
    default_namespace   VARCHAR(128) NOT NULL,
    is_builtin          BOOLEAN NOT NULL DEFAULT true,
    is_enabled          BOOLEAN NOT NULL DEFAULT true,
    helm_chart_id       UUID REFERENCES helm_charts(id) ON DELETE SET NULL,
    presets             JSONB NOT NULL DEFAULT '{}',
    service_name        VARCHAR(128) NOT NULL DEFAULT '',
    service_port        INTEGER,
    service_path        VARCHAR(128) NOT NULL DEFAULT '/',
    sub_services        JSONB NOT NULL DEFAULT '[]',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- ArgoCD: Instances
-- ============================================================
CREATE TABLE argocd_instances (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                    VARCHAR(128) NOT NULL UNIQUE,
    cluster_id              UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    api_url                 VARCHAR(512) NOT NULL,
    auth_token_encrypted    TEXT NOT NULL DEFAULT '',
    verify_ssl              BOOLEAN NOT NULL DEFAULT true,
    is_healthy              BOOLEAN NOT NULL DEFAULT false,
    last_sync               TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_argocd_instances_cluster ON argocd_instances (cluster_id);

-- ============================================================
-- ArgoCD: Applications
-- ============================================================
CREATE TABLE argocd_applications (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    argocd_instance_id      UUID NOT NULL REFERENCES argocd_instances(id) ON DELETE CASCADE,
    name                    VARCHAR(255) NOT NULL,
    project                 VARCHAR(255) NOT NULL DEFAULT 'default',
    repo_url                VARCHAR(512) NOT NULL DEFAULT '',
    path                    VARCHAR(512) NOT NULL DEFAULT '',
    target_revision         VARCHAR(128) NOT NULL DEFAULT 'HEAD',
    destination_cluster     VARCHAR(512) NOT NULL DEFAULT '',
    destination_namespace   VARCHAR(255) NOT NULL DEFAULT '',
    sync_status             VARCHAR(16) NOT NULL DEFAULT 'Unknown',
    health_status           VARCHAR(16) NOT NULL DEFAULT 'Unknown',
    last_synced             TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (argocd_instance_id, name)
);

CREATE INDEX idx_argocd_apps_sync_health ON argocd_applications (sync_status, health_status);
CREATE INDEX idx_argocd_apps_instance_project ON argocd_applications (argocd_instance_id, project);

-- ============================================================
-- Logging: Outputs
-- ============================================================
CREATE TABLE logging_outputs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(255) NOT NULL,
    output_type     VARCHAR(16) NOT NULL,
    configuration   JSONB NOT NULL,
    cluster_id      UUID REFERENCES clusters(id) ON DELETE CASCADE,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_by_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_logging_outputs_type_enabled ON logging_outputs (output_type, enabled);
CREATE INDEX idx_logging_outputs_cluster_enabled ON logging_outputs (cluster_id, enabled);

-- ============================================================
-- Logging: Pipelines
-- ============================================================
CREATE TABLE logging_pipelines (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(255) NOT NULL,
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    namespaces      JSONB NOT NULL DEFAULT '[]',
    labels          JSONB NOT NULL DEFAULT '{}',
    filters         JSONB NOT NULL DEFAULT '[]',
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_by_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_logging_pipelines_cluster_enabled ON logging_pipelines (cluster_id, enabled);

-- Pipelines <-> Outputs (M2M)
CREATE TABLE logging_pipeline_outputs (
    logging_pipeline_id UUID NOT NULL REFERENCES logging_pipelines(id) ON DELETE CASCADE,
    logging_output_id   UUID NOT NULL REFERENCES logging_outputs(id) ON DELETE CASCADE,
    PRIMARY KEY (logging_pipeline_id, logging_output_id)
);

-- ============================================================
-- Agent Connections
-- ============================================================
CREATE TABLE agent_connections (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    agent_id        VARCHAR(128) NOT NULL,
    connected_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    disconnected_at TIMESTAMPTZ,
    last_ping       TIMESTAMPTZ,
    status          VARCHAR(16) NOT NULL DEFAULT 'connected',
    channel_name    VARCHAR(255) NOT NULL DEFAULT '',
    pod_name        VARCHAR(255) NOT NULL DEFAULT '',
    node_name       VARCHAR(255) NOT NULL DEFAULT '',
    agent_version   VARCHAR(32) NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_agent_conns_cluster_status ON agent_connections (cluster_id, status);
CREATE INDEX idx_agent_conns_agent_id ON agent_connections (agent_id);

-- ============================================================
-- Audit Logs
-- ============================================================
CREATE TABLE audit_logs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID REFERENCES users(id) ON DELETE SET NULL,
    action          VARCHAR(32) NOT NULL,
    resource_type   VARCHAR(64) NOT NULL,
    resource_id     VARCHAR(255) NOT NULL DEFAULT '',
    resource_name   VARCHAR(255) NOT NULL DEFAULT '',
    detail          JSONB NOT NULL DEFAULT '{}',
    ip_address      INET,
    user_agent      TEXT NOT NULL DEFAULT '',
    request_id      VARCHAR(64) NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_audit_logs_action_created ON audit_logs (action, created_at);
CREATE INDEX idx_audit_logs_resource ON audit_logs (resource_type, resource_id);
CREATE INDEX idx_audit_logs_user_created ON audit_logs (user_id, created_at);
CREATE INDEX idx_audit_logs_request_id ON audit_logs (request_id);
CREATE INDEX idx_audit_logs_created_at ON audit_logs (created_at);

-- ============================================================
-- Seed built-in RBAC roles
-- ============================================================
INSERT INTO global_roles (name, description, rules, is_builtin) VALUES
('Administrator', 'Full platform access', '[{"resource":"*","verbs":["*"]}]', true),
('Standard User', 'Can view clusters and manage assigned resources', '[{"resource":"clusters","verbs":["read","list"]},{"resource":"projects","verbs":["read","list"]},{"resource":"workloads","verbs":["read","list"]},{"resource":"monitoring","verbs":["read","list"]}]', true),
('Read Only', 'View-only access across the platform', '[{"resource":"*","verbs":["read","list"]}]', true);

INSERT INTO cluster_roles (name, description, rules, is_builtin) VALUES
('Cluster Owner', 'Full access to a specific cluster', '[{"resource":"*","verbs":["*"]}]', true),
('Cluster Member', 'Can view cluster resources and manage workloads', '[{"resource":"clusters","verbs":["read"]},{"resource":"workloads","verbs":["read","list","create","update","delete","scale","restart"]},{"resource":"pods","verbs":["read","list","watch"]},{"resource":"monitoring","verbs":["read","list"]}]', true),
('Cluster Viewer', 'Read-only access to a cluster', '[{"resource":"*","verbs":["read","list","watch"]}]', true);

INSERT INTO project_roles (name, description, rules, is_builtin) VALUES
('Project Owner', 'Full access within a project scope', '[{"resource":"*","verbs":["*"]}]', true),
('Project Member', 'Can manage workloads within a project', '[{"resource":"workloads","verbs":["read","list","create","update","delete","scale","restart"]},{"resource":"pods","verbs":["read","list","watch"]}]', true),
('Project Viewer', 'Read-only access within a project', '[{"resource":"*","verbs":["read","list","watch"]}]', true);

-- ============================================================
-- Updated_at trigger function
-- ============================================================
CREATE OR REPLACE FUNCTION update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Apply trigger to all tables with updated_at
DO $$
DECLARE
    tbl TEXT;
BEGIN
    FOR tbl IN
        SELECT table_name FROM information_schema.columns
        WHERE column_name = 'updated_at'
        AND table_schema = 'public'
        AND table_name != 'platform_configuration'
    LOOP
        EXECUTE format(
            'CREATE TRIGGER set_updated_at BEFORE UPDATE ON %I FOR EACH ROW EXECUTE FUNCTION update_updated_at()',
            tbl
        );
    END LOOP;
END;
$$;
```

Create `internal/db/migrations/001_initial.down.sql`:

```sql
-- Drop in reverse dependency order
DROP TABLE IF EXISTS logging_pipeline_outputs CASCADE;
DROP TABLE IF EXISTS logging_pipelines CASCADE;
DROP TABLE IF EXISTS logging_outputs CASCADE;
DROP TABLE IF EXISTS argocd_applications CASCADE;
DROP TABLE IF EXISTS argocd_instances CASCADE;
DROP TABLE IF EXISTS cluster_tools CASCADE;
DROP TABLE IF EXISTS security_scan_results CASCADE;
DROP TABLE IF EXISTS cluster_security_policies CASCADE;
DROP TABLE IF EXISTS pod_security_templates CASCADE;
DROP TABLE IF EXISTS restore_operations CASCADE;
DROP TABLE IF EXISTS backup_schedules CASCADE;
DROP TABLE IF EXISTS backups CASCADE;
DROP TABLE IF EXISTS backup_storage_configs CASCADE;
DROP TABLE IF EXISTS installed_charts CASCADE;
DROP TABLE IF EXISTS helm_chart_versions CASCADE;
DROP TABLE IF EXISTS helm_charts CASCADE;
DROP TABLE IF EXISTS helm_repositories CASCADE;
DROP TABLE IF EXISTS alert_silences CASCADE;
DROP TABLE IF EXISTS alert_events CASCADE;
DROP TABLE IF EXISTS alert_rule_channels CASCADE;
DROP TABLE IF EXISTS alert_rules CASCADE;
DROP TABLE IF EXISTS notification_channels CASCADE;
DROP TABLE IF EXISTS project_role_bindings CASCADE;
DROP TABLE IF EXISTS project_roles CASCADE;
DROP TABLE IF EXISTS cluster_role_bindings CASCADE;
DROP TABLE IF EXISTS cluster_roles CASCADE;
DROP TABLE IF EXISTS global_role_bindings CASCADE;
DROP TABLE IF EXISTS global_roles CASCADE;
DROP TABLE IF EXISTS projects CASCADE;
DROP TABLE IF EXISTS cluster_registry_configs CASCADE;
DROP TABLE IF EXISTS cluster_health_statuses CASCADE;
DROP TABLE IF EXISTS cluster_registration_tokens CASCADE;
DROP TABLE IF EXISTS agent_connections CASCADE;
DROP TABLE IF EXISTS audit_logs CASCADE;
DROP TABLE IF EXISTS api_tokens CASCADE;
DROP TABLE IF EXISTS sso_configurations CASCADE;
DROP TABLE IF EXISTS clusters CASCADE;
DROP TABLE IF EXISTS users CASCADE;
DROP TABLE IF EXISTS platform_configuration CASCADE;
DROP FUNCTION IF EXISTS update_updated_at() CASCADE;
```

**Step 3: Create basic sqlc queries for users**

Create `internal/db/queries/users.sql`:

```sql
-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = $1;

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: CreateUser :one
INSERT INTO users (email, username, first_name, last_name, password, is_active, is_staff, is_superuser)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateUser :one
UPDATE users SET
    email = $2,
    username = $3,
    first_name = $4,
    last_name = $5,
    is_active = $6,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateUserPassword :exec
UPDATE users SET password = $2, updated_at = now() WHERE id = $1;

-- name: UpdateUserLastLogin :exec
UPDATE users SET last_login = now() WHERE id = $1;

-- name: CountUsers :one
SELECT count(*) FROM users;
```

**Step 4: Generate sqlc code and verify**

Run:
```bash
cd /home/mj/code/astronomer-all/astronomer-go
go get github.com/google/uuid
sqlc generate
go build ./...
```

**Step 5: Commit**

```bash
git add .
git commit -m "feat: add full database schema migration matching Django models, sqlc setup"
```

---

### Task 4: Makefile and Docker Compose

**Files:**
- Create: `Makefile`
- Create: `deploy/docker/Dockerfile.server`
- Create: `deploy/docker-compose.yml`

**Step 1: Create Makefile**

```makefile
.PHONY: build test lint run migrate sqlc clean help

VERSION ?= dev
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS := -X github.com/alphabravocompany/astronomer-go/pkg/version.Version=$(VERSION) \
           -X github.com/alphabravocompany/astronomer-go/pkg/version.GitCommit=$(GIT_COMMIT) \
           -X github.com/alphabravocompany/astronomer-go/pkg/version.BuildDate=$(BUILD_DATE)

DATABASE_URL ?= postgres://astronomer:astronomer@localhost:5432/astronomer?sslmode=disable

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[32m%-20s\033[0m %s\n", $$1, $$2}'

build: ## Build all binaries
	go build -ldflags "$(LDFLAGS)" -o bin/server ./cmd/server
	go build -ldflags "$(LDFLAGS)" -o bin/agent ./cmd/agent
	go build -ldflags "$(LDFLAGS)" -o bin/worker ./cmd/worker

test: ## Run all tests
	go test -race -count=1 ./...

lint: ## Run linter
	golangci-lint run ./...

run: ## Run the server locally
	go run -ldflags "$(LDFLAGS)" ./cmd/server

sqlc: ## Generate sqlc code
	sqlc generate

migrate-up: ## Run database migrations up
	migrate -database "$(DATABASE_URL)" -path internal/db/migrations up

migrate-down: ## Run database migrations down (1 step)
	migrate -database "$(DATABASE_URL)" -path internal/db/migrations down 1

migrate-create: ## Create a new migration (usage: make migrate-create NAME=add_foo)
	migrate create -ext sql -dir internal/db/migrations -seq $(NAME)

clean: ## Remove build artifacts
	rm -rf bin/

dev: ## Start Docker Compose dev environment
	docker compose -f deploy/docker-compose.yml up

dev-down: ## Stop Docker Compose dev environment
	docker compose -f deploy/docker-compose.yml down

dev-clean: ## Stop and remove volumes
	docker compose -f deploy/docker-compose.yml down -v
```

**Step 2: Create deploy/docker/Dockerfile.server**

```dockerfile
# Build stage
FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG GIT_COMMIT=unknown
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-X github.com/alphabravocompany/astronomer-go/pkg/version.Version=${VERSION} \
              -X github.com/alphabravocompany/astronomer-go/pkg/version.GitCommit=${GIT_COMMIT}" \
    -o /server ./cmd/server

# Runtime stage
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /server /usr/local/bin/server

EXPOSE 8000
USER nobody:nobody
ENTRYPOINT ["server"]
```

**Step 3: Create deploy/docker-compose.yml**

```yaml
x-go-env: &go-env
  DATABASE_URL: postgres://astronomer:astronomer@postgres:5432/astronomer?sslmode=disable
  REDIS_URL: redis://redis:6379/0
  CELERY_BROKER_URL: redis://redis:6379/1
  DJANGO_SECRET_KEY: insecure-dev-secret-key-change-in-production
  DJANGO_ENV: development
  DJANGO_DEBUG: "true"
  CORS_ALLOWED_ORIGINS: "http://localhost:3000,http://localhost"
  LOG_LEVEL: debug

services:
  postgres:
    image: postgres:16-alpine
    container_name: astronomer-go-postgres
    environment:
      POSTGRES_DB: astronomer
      POSTGRES_USER: astronomer
      POSTGRES_PASSWORD: astronomer
    volumes:
      - postgres_data:/var/lib/postgresql/data
    ports:
      - "5433:5432"
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U astronomer -d astronomer"]
      interval: 10s
      timeout: 5s
      retries: 5
    networks:
      - astronomer-go

  redis:
    image: redis:7-alpine
    container_name: astronomer-go-redis
    command: redis-server --appendonly yes --maxmemory 256mb --maxmemory-policy allkeys-lru
    volumes:
      - redis_data:/data
    ports:
      - "6380:6379"
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 10s
      timeout: 5s
      retries: 5
    networks:
      - astronomer-go

  server:
    build:
      context: ../
      dockerfile: deploy/docker/Dockerfile.server
    container_name: astronomer-go-server
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
    ports:
      - "8001:8000"
    environment:
      <<: *go-env
    healthcheck:
      test: ["CMD-SHELL", "wget --no-verbose --tries=1 --spider http://127.0.0.1:8000/health/ || exit 1"]
      interval: 30s
      timeout: 10s
      retries: 3
    networks:
      - astronomer-go

volumes:
  postgres_data:
  redis_data:

networks:
  astronomer-go:
    driver: bridge
```

**Step 4: Verify Makefile works**

Run:
```bash
cd /home/mj/code/astronomer-all/astronomer-go
make build
make test
```
Expected: Binaries in `bin/`, tests pass.

**Step 5: Commit**

```bash
git add .
git commit -m "feat: add Makefile, Dockerfiles, and Docker Compose for dev environment"
```

---

### Task 5: JSON response helpers matching DRF format

**Files:**
- Create: `internal/handler/response.go`
- Create: `internal/handler/response_test.go`

The Python backend uses a custom DRF renderer (`APIResponseRenderer`) that wraps responses. The frontend expects:
- Success: `{"data": <payload>}` or `{"data": <list>, "count": N, "next": "...", "previous": "..."}`
- Error: `{"error": {"code": "...", "message": "...", "details": {...}}}`

**Step 1: Write the failing test**

Create `internal/handler/response_test.go`:
```go
package handler_test

import (
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/alphabravocompany/astronomer-go/internal/handler"
)

func TestRespondJSON(t *testing.T) {
    w := httptest.NewRecorder()
    handler.RespondJSON(w, http.StatusOK, map[string]string{"name": "test"})

    if w.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d", w.Code)
    }

    var body map[string]any
    json.NewDecoder(w.Body).Decode(&body)

    data, ok := body["data"].(map[string]any)
    if !ok {
        t.Fatalf("expected data wrapper, got %v", body)
    }
    if data["name"] != "test" {
        t.Fatalf("expected name=test, got %v", data["name"])
    }
}

func TestRespondError(t *testing.T) {
    w := httptest.NewRecorder()
    handler.RespondError(w, http.StatusBadRequest, "validation_error", "invalid input")

    if w.Code != http.StatusBadRequest {
        t.Fatalf("expected 400, got %d", w.Code)
    }

    var body map[string]any
    json.NewDecoder(w.Body).Decode(&body)

    errObj, ok := body["error"].(map[string]any)
    if !ok {
        t.Fatalf("expected error wrapper, got %v", body)
    }
    if errObj["code"] != "validation_error" {
        t.Fatalf("expected code=validation_error, got %v", errObj["code"])
    }
}

func TestRespondPaginated(t *testing.T) {
    w := httptest.NewRecorder()
    items := []map[string]string{{"id": "1"}, {"id": "2"}}
    handler.RespondPaginated(w, http.StatusOK, items, 100, 25, 0)

    var body map[string]any
    json.NewDecoder(w.Body).Decode(&body)

    if body["count"] != float64(100) {
        t.Fatalf("expected count=100, got %v", body["count"])
    }
    data, ok := body["data"].([]any)
    if !ok || len(data) != 2 {
        t.Fatalf("expected 2 items in data, got %v", body["data"])
    }
}
```

**Step 2: Implement response helpers**

Create `internal/handler/response.go`:
```go
package handler

import (
    "encoding/json"
    "fmt"
    "net/http"
)

// RespondJSON wraps the payload in {"data": payload} matching DRF format.
func RespondJSON(w http.ResponseWriter, status int, payload any) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(map[string]any{
        "data": payload,
    })
}

// RespondError returns {"error": {"code": ..., "message": ...}} matching DRF format.
func RespondError(w http.ResponseWriter, status int, code, message string) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(map[string]any{
        "error": map[string]any{
            "code":    code,
            "message": message,
        },
    })
}

// RespondPaginated returns {"data": items, "count": N, "next": ..., "previous": ...}
// matching DRF pagination format.
func RespondPaginated(w http.ResponseWriter, status int, items any, total int64, limit, offset int) {
    var next, previous *string

    if int64(offset+limit) < total {
        s := fmt.Sprintf("?limit=%d&offset=%d", limit, offset+limit)
        next = &s
    }
    if offset > 0 {
        prevOffset := offset - limit
        if prevOffset < 0 {
            prevOffset = 0
        }
        s := fmt.Sprintf("?limit=%d&offset=%d", limit, prevOffset)
        previous = &s
    }

    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(map[string]any{
        "data":     items,
        "count":    total,
        "next":     next,
        "previous": previous,
    })
}
```

**Step 3: Run tests**

Run:
```bash
go test ./internal/handler/ -v
```
Expected: All 3 tests PASS.

**Step 4: Commit**

```bash
git add .
git commit -m "feat: add JSON response helpers matching DRF API format"
```

---

### Task 6: Request ID and audit logging middleware

**Files:**
- Create: `internal/server/middleware/requestid.go`
- Create: `internal/server/middleware/audit.go`
- Create: `internal/server/middleware/requestid_test.go`

**Step 1: Write the failing test**

Create `internal/server/middleware/requestid_test.go`:
```go
package middleware_test

import (
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

func TestRequestIDMiddleware(t *testing.T) {
    handler := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Should have request ID in context
        id := middleware.GetRequestID(r.Context())
        if id == "" {
            t.Fatal("expected request ID in context")
        }
        w.WriteHeader(http.StatusOK)
    }))

    // Without X-Request-ID header - should generate one
    req := httptest.NewRequest(http.MethodGet, "/", nil)
    w := httptest.NewRecorder()
    handler.ServeHTTP(w, req)

    if w.Header().Get("X-Request-ID") == "" {
        t.Fatal("expected X-Request-ID response header")
    }

    // With X-Request-ID header - should reuse it
    req2 := httptest.NewRequest(http.MethodGet, "/", nil)
    req2.Header.Set("X-Request-ID", "test-123")
    w2 := httptest.NewRecorder()
    handler.ServeHTTP(w2, req2)

    if w2.Header().Get("X-Request-ID") != "test-123" {
        t.Fatalf("expected X-Request-ID=test-123, got %s", w2.Header().Get("X-Request-ID"))
    }
}
```

**Step 2: Implement request ID middleware**

Create `internal/server/middleware/requestid.go`:
```go
package middleware

import (
    "context"
    "net/http"

    "github.com/google/uuid"
)

type contextKey string

const requestIDKey contextKey = "request_id"

func RequestID(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        id := r.Header.Get("X-Request-ID")
        if id == "" {
            id = uuid.New().String()
        }
        ctx := context.WithValue(r.Context(), requestIDKey, id)
        w.Header().Set("X-Request-ID", id)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}

func GetRequestID(ctx context.Context) string {
    if id, ok := ctx.Value(requestIDKey).(string); ok {
        return id
    }
    return ""
}
```

**Step 3: Create audit middleware (stub for now, full DB write in Phase 3)**

Create `internal/server/middleware/audit.go`:
```go
package middleware

import (
    "log/slog"
    "net/http"
    "regexp"
    "strings"
    "time"
)

// PathResourceMap maps URL segments to human-readable resource types.
// Matches Python middleware._PATH_RESOURCE_MAP
var PathResourceMap = map[string]string{
    "clusters": "cluster", "workloads": "workload", "pods": "pod",
    "nodes": "node", "namespaces": "namespace", "projects": "project",
    "users": "user", "global-roles": "global role", "cluster-roles": "cluster role",
    "project-roles": "project role", "bindings": "role binding",
    "argocd": "ArgoCD", "alerting": "alert", "rules": "alert rule",
    "channels": "notification channel", "silences": "alert silence",
    "logging": "logging", "outputs": "log output", "pipelines": "log pipeline",
    "backups": "backup", "schedules": "backup schedule", "storage": "backup storage",
    "security": "security", "templates": "security template",
    "policies": "security policy", "scans": "security scan",
    "catalog": "catalog", "tools": "tool", "repositories": "Helm repository",
    "charts": "Helm chart", "installed": "Helm release",
    "sso": "SSO provider", "tokens": "API token", "settings": "settings",
}

var mutatingMethods = map[string]bool{
    "POST": true, "PUT": true, "PATCH": true, "DELETE": true,
}

var skipPaths = map[string]bool{
    "/api/v1/auth/login":         true,
    "/api/v1/auth/refresh":       true,
    "/api/v1/bootstrap/complete": true,
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func AuditLog(log *slog.Logger) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            start := time.Now()
            ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}
            next.ServeHTTP(ww, r)
            duration := time.Since(start)

            if !mutatingMethods[r.Method] || !strings.HasPrefix(r.URL.Path, "/api/") {
                return
            }

            path := strings.TrimRight(r.URL.Path, "/")
            if skipPaths[path] {
                return
            }

            if ww.status >= 400 {
                return
            }

            resourceType, resourceID, _ := parsePath(r.URL.Path)
            requestID := GetRequestID(r.Context())

            log.Info("audit",
                "method", r.Method,
                "path", r.URL.Path,
                "resource_type", resourceType,
                "resource_id", resourceID,
                "status", ww.status,
                "duration_ms", duration.Milliseconds(),
                "request_id", requestID,
            )
        })
    }
}

type statusWriter struct {
    http.ResponseWriter
    status int
}

func (w *statusWriter) WriteHeader(status int) {
    w.status = status
    w.ResponseWriter.WriteHeader(status)
}

func parsePath(path string) (resourceType, resourceID, subAction string) {
    stripped := regexp.MustCompile(`^/api/v[0-9]+/`).ReplaceAllString(strings.TrimRight(path, "/"), "")
    segments := strings.Split(stripped, "/")

    resourceType = "api"
    for i := 0; i < len(segments); i++ {
        seg := segments[i]
        if rt, ok := PathResourceMap[seg]; ok {
            resourceType = rt
            if i+1 < len(segments) && uuidPattern.MatchString(segments[i+1]) {
                resourceID = segments[i+1]
                i++
            }
        } else if uuidPattern.MatchString(seg) {
            resourceID = seg
        }
    }
    return
}
```

**Step 4: Run tests**

Run:
```bash
go test ./internal/server/middleware/ -v
```
Expected: PASS.

**Step 5: Commit**

```bash
git add .
git commit -m "feat: add request ID and audit logging middleware"
```

---

## Phase 2: Database Queries (sqlc)

### Task 7: Write sqlc queries for all models

**Files:**
- Create: `internal/db/queries/clusters.sql`
- Create: `internal/db/queries/projects.sql`
- Create: `internal/db/queries/rbac.sql`
- Create: `internal/db/queries/alerting.sql`
- Create: `internal/db/queries/catalog.sql`
- Create: `internal/db/queries/backups.sql`
- Create: `internal/db/queries/security.sql`
- Create: `internal/db/queries/tools.sql`
- Create: `internal/db/queries/argocd.sql`
- Create: `internal/db/queries/logging.sql`
- Create: `internal/db/queries/agents.sql`
- Create: `internal/db/queries/audit.sql`
- Create: `internal/db/queries/auth.sql`
- Create: `internal/db/queries/platform.sql`

Each query file should have standard CRUD operations: Get, List, Create, Update, Delete, plus domain-specific queries (e.g., GetClustersByStatus, ListAlertEventsByCluster, etc.).

**Pattern for each file:**
```sql
-- name: Get<Model>ByID :one
SELECT * FROM <table> WHERE id = $1;

-- name: List<Model>s :many
SELECT * FROM <table> ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: Create<Model> :one
INSERT INTO <table> (...) VALUES (...) RETURNING *;

-- name: Update<Model> :one
UPDATE <table> SET ... WHERE id = $1 RETURNING *;

-- name: Delete<Model> :exec
DELETE FROM <table> WHERE id = $1;

-- name: Count<Model>s :one
SELECT count(*) FROM <table>;
```

After writing all query files:
```bash
sqlc generate
go build ./...
```

**Step: Commit**
```bash
git add .
git commit -m "feat: add sqlc queries for all database models"
```

---

## Phase 3: Auth & Middleware

### Task 8: JWT token generation and validation

**Files:**
- Create: `internal/auth/jwt.go`
- Create: `internal/auth/jwt_test.go`

Implement JWT using `golang-jwt/jwt/v5` with HS256 signing.
- Access token: configurable lifetime (default 60 min)
- Refresh token: 7 day lifetime
- Claims: `user_id` (UUID), `exp`, `iat`, `jti`, `token_type` (access/refresh)
- Refresh token rotation with blacklist tracking

### Task 9: Login endpoint

**Files:**
- Create: `internal/handler/auth.go`
- Create: `internal/handler/auth_test.go`

`POST /api/v1/auth/login/` — accepts `{username, password}` or `{email, password}`
- bcrypt password verification
- Returns `{token, refresh, user}` with full profile + role bindings
- Matches Python `AstronomerTokenObtainPairSerializer` response format

### Task 10: Auth middleware

**Files:**
- Create: `internal/server/middleware/auth.go`
- Create: `internal/server/middleware/auth_test.go`

Check order:
1. `Authorization: Bearer astro_*` → API token (SHA-256 hash match in DB)
2. `Authorization: Bearer <jwt>` → JWT validation
3. No auth → 401

Inject user into request context.

### Task 11: API token CRUD

**Files:**
- Modify: `internal/handler/auth.go` — add token endpoints

`POST /api/v1/auth/tokens/` — generate `astro_` prefixed token, return plaintext once
`GET /api/v1/auth/tokens/` — list tokens (prefix, expiry, last_used)
`DELETE /api/v1/auth/tokens/{id}/` — soft revoke

### Task 12: Fernet-compatible encryption

**Files:**
- Create: `internal/auth/crypto.go`
- Create: `internal/auth/crypto_test.go`

Use `github.com/fernet/fernet-go` for SSO client secret encryption.
Must be compatible with Python `cryptography.fernet` for migration.

### Task 13: OAuth2 SSO flows (GitHub, Google, OIDC)

**Files:**
- Create: `internal/auth/oauth.go`
- Create: `internal/auth/oauth_test.go`

`GET /api/v1/auth/sso/{provider}/` → redirect to provider
`GET /api/v1/auth/sso/{provider}/callback` → exchange code, create/find user, return JWT

### Task 14: Bootstrap endpoints

**Files:**
- Modify: `internal/server/routes.go` — connect to DB
- Modify: `internal/handler/bootstrap.go`

Wire `GET /api/v1/bootstrap/` and `POST /api/v1/bootstrap/complete/` to real DB.

---

## Phase 4: RBAC Engine

### Task 15: RBAC permission engine

**Files:**
- Create: `internal/rbac/engine.go`
- Create: `internal/rbac/engine_test.go`
- Create: `internal/rbac/types.go`
- Create: `internal/rbac/cache.go`

Three-tier check: Global → Cluster → Project
- 11 resources × 11 verbs
- Wildcard matching (`*`)
- Redis or in-memory cache for hot paths

### Task 16: RBAC middleware

**Files:**
- Create: `internal/server/middleware/rbac.go`
- Create: `internal/server/middleware/rbac_test.go`

Per-route permission check injected via Chi middleware.

### Task 17: RBAC API endpoints

**Files:**
- Create: `internal/handler/rbac.go`

CRUD for roles and bindings at all three tiers.
`GET /api/v1/rbac/my-roles/` — current user's role summary.

---

## Phase 5: REST API Endpoints

### Tasks 18-30: One per domain (implement in dependency order)

Each task creates `internal/handler/<domain>.go` with all endpoints matching the existing Python views.

**Task 18:** Projects — CRUD
**Task 19:** Tools — list tools, track installations
**Task 20:** Audit — query/filter audit logs
**Task 21:** Clusters — CRUD, health, nodes, namespaces, events, register
**Task 22:** Monitoring — Prometheus query proxy
**Task 23:** Logging — outputs and pipelines CRUD
**Task 24:** Workloads — list, scale, restart, pod operations (proxied through tunnel)
**Task 25:** Catalog — Helm repos, charts, installations CRUD
**Task 26:** Backups — storage configs, schedules, retention CRUD
**Task 27:** Security — templates, policies, scans CRUD
**Task 28:** ArgoCD — instances, applications, sync
**Task 29:** Alerting — rules, events, channels, silences CRUD
**Task 30:** Resources — generic K8s resource proxy, settings, activity feed, users

Each endpoint needs:
- Request validation (go-playground/validator struct tags)
- Response serialization matching existing JSON (camelCase via struct tags)
- Pagination (`limit`/`offset`, DRF format)
- RBAC middleware annotation
- Wire into `routes.go`

---

## Phase 6: WebSocket Tunnel Server

### Task 31: Tunnel hub and WebSocket upgrade

**Files:**
- Create: `internal/tunnel/server.go`
- Create: `internal/tunnel/protocol.go`
- Create: `pkg/protocol/types.go`

Hub manages connected agents. WebSocket at `/api/v1/ws/agent/tunnel/{cluster_id}/`.

### Task 32: Wire protocol and message dispatch

**Files:**
- Create: `internal/tunnel/handler.go`

32 message types from Python `protocol.py`. JSON wire format.

### Task 33: Stream multiplexing

**Files:**
- Create: `internal/tunnel/stream.go`

256 concurrent streams per agent. Goroutine-per-stream.

### Task 34: K8s API proxy, exec relay, log streaming

Route K8s requests through tunnel to agent and back.

### Task 35: Pod exec and log WebSocket consumers

**Files:**
- Modify: `internal/server/routes.go` — add exec/logs WebSocket routes

`/api/v1/ws/exec/{cluster_id}/{namespace}/{pod}/{container}/`
`/api/v1/ws/logs/{cluster_id}/{namespace}/{pod}/{container}/`

---

## Phase 7: Agent Rewrite

### Task 36: Agent tunnel client with reconnection

**Files:**
- Create: `internal/agent/agent.go`
- Create: `internal/agent/tunnel.go`
- Create: `internal/agent/config.go`

Cobra CLI `connect` command. WebSocket client with exponential backoff.

### Task 37: K8s API proxy via client-go

**Files:**
- Create: `internal/agent/k8sproxy.go`

Forward HTTP requests from tunnel to K8s API server.

### Task 38: Helm operations (native Go)

**Files:**
- Create: `internal/agent/helm.go`

Install, upgrade, uninstall, rollback using `helm.sh/helm/v3` — no subprocess.

### Task 39: Pod exec via SPDY

**Files:**
- Create: `internal/agent/exec.go`

`k8s.io/client-go/tools/remotecommand` for interactive shell.

### Task 40: Pod log streaming

**Files:**
- Create: `internal/agent/logs.go`

Stream pod logs via client-go.

### Task 41: Health reporter

**Files:**
- Create: `internal/agent/health.go`

Periodic heartbeat with cluster metrics (node count, CPU, memory, K8s version, distribution).

### Task 42: RBAC syncer

**Files:**
- Create: `internal/agent/rbac.go`

Server-side apply of RBAC resources. Garbage collection of removed bindings.

---

## Phase 8: Background Workers

### Task 43: Asynq worker setup

**Files:**
- Create: `internal/worker/worker.go`
- Create: `internal/worker/scheduler.go`

### Tasks 44-51: Port individual tasks

**Task 44:** Health check task
**Task 45:** Alert evaluation task
**Task 46:** Helm catalog sync task
**Task 47:** Metrics aggregation task
**Task 48:** Backup execution task
**Task 49:** Security scan task
**Task 50:** Notification dispatch task
**Task 51:** Agent manifest generation task

---

## Phase 9: Integration & Testing

### Task 52: API contract tests against frontend types
### Task 53: End-to-end test harness
### Task 54: K8s manifests and Helm chart
### Task 55: Performance benchmarks
### Task 56: Documentation and README

---

## Key Reference Files (Python → Go Mapping)

| Python File | Go Equivalent | Notes |
|------------|--------------|-------|
| `backend/astronomer/settings/base.py` | `internal/config/config.go` | All env vars |
| `backend/astronomer/urls.py` | `internal/server/routes.go` | All routes |
| `backend/astronomer/routing.py` | WebSocket routes in `routes.go` | 3 WS endpoints |
| `backend/apps/core/models.py` | `internal/db/migrations/001_initial.up.sql` | Base model + audit |
| `backend/apps/core/middleware.py` | `internal/server/middleware/` | RequestID + Audit |
| `backend/apps/authentication/models.py` | Migration SQL | SSO + API tokens |
| `backend/apps/clusters/models.py` | Migration SQL | 4 models |
| `backend/apps/rbac/models.py` | Migration SQL + `internal/rbac/` | 6 models |
| `backend/apps/alerting/models.py` | Migration SQL | 4 models |
| `backend/apps/catalog/models.py` | Migration SQL | 4 models |
| `backend/apps/backups/models.py` | Migration SQL | 4 models |
| `backend/apps/security/models.py` | Migration SQL | 3 models |
| `backend/apps/tools/models.py` | Migration SQL | 1 model |
| `backend/apps/argocd/models.py` | Migration SQL | 2 models |
| `backend/apps/logging_config/models.py` | Migration SQL | 2 models |
| `backend/apps/agents/models.py` | Migration SQL | 1 model |
| `backend/apps/agents/consumers.py` | `internal/tunnel/` | 515 LOC → Go |
| `agent/astronomer_agent/` | `internal/agent/` | Full agent rewrite |
| `frontend/src/lib/api.ts` | N/A (contract to match) | API compatibility target |
| `frontend/src/types/index.ts` | N/A (contract to match) | 106 TypeScript types |
