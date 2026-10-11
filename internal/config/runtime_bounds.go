package config

import (
	"fmt"
	"math"
)

func validateRuntimeBounds(cfg *Config) error {
	if cfg.LoginFailureThreshold > math.MaxInt32 {
		return fmt.Errorf("login_failure_threshold exceeds the supported database range")
	}
	if cfg.WorkerConcurrency > math.MaxInt32-8 {
		return fmt.Errorf("worker_concurrency exceeds the supported database pool range")
	}
	return normalizeHelmAndDeliveryTrust(cfg)
}
