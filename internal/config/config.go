package config

import (
	"strings"

	"github.com/spf13/viper"
)

// Config holds all configuration for the application.
type Config struct {
	DatabaseURL    string `mapstructure:"database_url"`
	RedisURL       string `mapstructure:"redis_url"`
	CeleryBrokerURL string `mapstructure:"celery_broker_url"`

	SecretKey string `mapstructure:"secret_key"`
	Env       string `mapstructure:"env"`
	Debug     bool   `mapstructure:"debug"`

	CORSAllowedOrigins    string `mapstructure:"cors_allowed_origins"`
	SessionTimeoutMinutes int    `mapstructure:"session_timeout_minutes"`
	AgentTokenExpiryHours int    `mapstructure:"agent_token_expiry_hours"`

	EncryptionKey string `mapstructure:"astronomer_encryption_key"`

	GithubClientID     string `mapstructure:"github_client_id"`
	GithubClientSecret string `mapstructure:"github_client_secret"`

	GoogleClientID     string `mapstructure:"google_client_id"`
	GoogleClientSecret string `mapstructure:"google_client_secret"`

	OIDCIssuer       string `mapstructure:"oidc_issuer"`
	OIDCClientID     string `mapstructure:"oidc_client_id"`
	OIDCClientSecret string `mapstructure:"oidc_client_secret"`

	AgentImageRepository string `mapstructure:"agent_image_repository"`
	AgentImageTag        string `mapstructure:"agent_image_tag"`

	LogLevel string `mapstructure:"log_level"`
}

// CORSOrigins returns the allowed origins as a slice.
func (c *Config) CORSOrigins() []string {
	return strings.Split(c.CORSAllowedOrigins, ",")
}

// Load reads configuration from environment variables with sensible defaults.
func Load() (*Config, error) {
	v := viper.New()
	v.AutomaticEnv()

	// Map DJANGO_* env vars to our config keys.
	v.BindEnv("secret_key", "DJANGO_SECRET_KEY")
	v.BindEnv("env", "DJANGO_ENV")
	v.BindEnv("debug", "DJANGO_DEBUG")

	v.SetDefault("database_url", "postgres://astronomer:astronomer@localhost:5432/astronomer?sslmode=disable")
	v.SetDefault("redis_url", "redis://localhost:6379/0")
	v.SetDefault("celery_broker_url", "redis://localhost:6379/1")
	v.SetDefault("env", "development")
	v.SetDefault("debug", false)
	v.SetDefault("cors_allowed_origins", "http://localhost:3000")
	v.SetDefault("session_timeout_minutes", 60)
	v.SetDefault("agent_token_expiry_hours", 24)
	v.SetDefault("log_level", "info")

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
