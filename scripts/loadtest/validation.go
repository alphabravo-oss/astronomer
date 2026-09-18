package main

import (
	"fmt"
	"os"
	"strings"
)

func validateConfig(cfg *config) error {
	if cfg.clusters < 1 {
		return fmt.Errorf("clusters must be >= 1, got %d", cfg.clusters)
	}
	if cfg.rps < 0 {
		return fmt.Errorf("rps must be >= 0, got %d", cfg.rps)
	}
	if !cfg.certification || cfg.mandatoryAudit.RatePerSecond <= 0 {
		return nil
	}
	if strings.TrimSpace(cfg.auditObserverPath) == "" {
		return fmt.Errorf("LOADTEST_AUDIT_OBSERVER_DATABASE_URL_FILE is required for certification")
	}
	info, err := os.Stat(cfg.auditObserverPath)
	if err != nil {
		return fmt.Errorf("audit observer DSN must be readable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("audit observer DSN must be a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("audit observer DSN file permissions must not grant group or other access")
	}
	return nil
}
