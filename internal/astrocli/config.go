// Astro CLI on-disk config.
//
// Stored at $XDG_CONFIG_HOME/astronomer/config.yaml (defaults to
// ~/.config/astronomer/config.yaml). Three values today: server URL,
// session JWT, refresh JWT. Persisted as YAML rather than netrc-style
// because we expect to grow per-server profiles ("staging" vs "prod")
// and YAML reads natural for multi-key state.
//
// The file is chmod 0600 — it carries a bearer token that the API
// accepts unconditionally.

package astrocli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ConfigFileName is the persisted file name under the per-user config
// dir. Kept as a constant so tests can override the dir but not the
// shape; operators don't need to know the file name to use the CLI.
const ConfigFileName = "config.yaml"

// Config is what the CLI persists between invocations.
type Config struct {
	// ServerURL is the Astronomer management URL the CLI talks to
	// (e.g. https://astronomer.example.com:8080). Set on first login.
	ServerURL string `yaml:"server_url"`

	// AccessToken is the short-lived JWT (~1h) the API accepts. Empty
	// until first login.
	AccessToken string `yaml:"access_token,omitempty"`

	// RefreshToken is the long-lived JWT (~7d) used to mint a fresh
	// access token when the current one expires. Empty until first
	// login. Stored alongside the access token because the API's
	// /auth/refresh endpoint takes a refresh JWT.
	RefreshToken string `yaml:"refresh_token,omitempty"`

	// Username is the last-logged-in user, surfaced by `astro whoami`
	// without an extra API round-trip when the token is still fresh.
	Username string `yaml:"username,omitempty"`
}

// ConfigPath returns the full path to the persisted config file.
// Honors $ASTRO_CONFIG_HOME (test override) before $XDG_CONFIG_HOME
// before $HOME/.config.
func ConfigPath() (string, error) {
	if v := strings.TrimSpace(os.Getenv("ASTRO_CONFIG_HOME")); v != "" {
		return filepath.Join(v, ConfigFileName), nil
	}
	if v := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); v != "" {
		return filepath.Join(v, "astronomer", ConfigFileName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "astronomer", ConfigFileName), nil
}

// LoadConfig reads the persisted config. Missing-file is NOT an error
// — returns a zero-value Config so the caller can treat "no config" and
// "fresh user" identically. Caller is expected to refuse most commands
// when ServerURL is empty.
func LoadConfig() (*Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(body, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &c, nil
}

// SaveConfig writes the config back to disk with 0600 perms. Creates
// the parent directory on the fly because a fresh-host install won't
// have ~/.config/astronomer/ yet.
func SaveConfig(c *Config) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	body, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
