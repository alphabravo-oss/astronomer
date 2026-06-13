package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type scaleProfile struct {
	Name              string         `yaml:"name"`
	Clusters          int            `yaml:"clusters"`
	RPS               int            `yaml:"rps"`
	Duration          string         `yaml:"duration"`
	Agents            scaleAgents    `yaml:"agents"`
	Resources         scaleResources `yaml:"resources"`
	EventsPerSecond   int            `yaml:"eventsPerSecond"`
	Day2FailureDrills []string       `yaml:"day2FailureDrills"`
}

type scaleAgents struct {
	Mode           string                `yaml:"mode"`
	ReconnectStorm reconnectStormProfile `yaml:"reconnectStorm"`
}

type reconnectStormProfile struct {
	Enabled      bool   `yaml:"enabled"`
	At           string `yaml:"at"`
	BatchPercent int    `yaml:"batchPercent"`
	Jitter       string `yaml:"jitter"`
}

type reconnectStormConfig struct {
	Enabled        bool
	AtDuration     time.Duration
	BatchPercent   int
	JitterDuration time.Duration
}

type scaleResources struct {
	ProfileName           string
	PodsPerCluster        int `yaml:"podsPerCluster"`
	DeploymentsPerCluster int `yaml:"deploymentsPerCluster"`
	ServicesPerCluster    int `yaml:"servicesPerCluster"`
	EventsPerSecond       int
}

func loadScaleProfile(path string) (*scaleProfile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var profile scaleProfile
	if err := yaml.Unmarshal(raw, &profile); err != nil {
		return nil, err
	}
	if profile.Name == "" {
		return nil, fmt.Errorf("profile name is required")
	}
	if profile.Clusters <= 0 {
		return nil, fmt.Errorf("clusters must be > 0")
	}
	if profile.RPS < 0 {
		return nil, fmt.Errorf("rps must be >= 0")
	}
	if strings.TrimSpace(profile.Duration) == "" {
		return nil, fmt.Errorf("duration is required")
	}
	if _, err := time.ParseDuration(profile.Duration); err != nil {
		return nil, fmt.Errorf("duration: %w", err)
	}
	if profile.Agents.Mode == "" {
		profile.Agents.Mode = "simulated"
	}
	if profile.Agents.Mode != "simulated" {
		return nil, fmt.Errorf("agents.mode must be simulated")
	}
	if profile.Resources.PodsPerCluster < 0 ||
		profile.Resources.DeploymentsPerCluster < 0 ||
		profile.Resources.ServicesPerCluster < 0 {
		return nil, fmt.Errorf("resource counts must be >= 0")
	}
	if profile.Agents.ReconnectStorm.Enabled {
		if profile.Agents.ReconnectStorm.BatchPercent < 0 || profile.Agents.ReconnectStorm.BatchPercent > 100 {
			return nil, fmt.Errorf("agents.reconnectStorm.batchPercent must be 0..100")
		}
		if profile.Agents.ReconnectStorm.At != "" {
			if _, err := time.ParseDuration(profile.Agents.ReconnectStorm.At); err != nil {
				return nil, fmt.Errorf("agents.reconnectStorm.at: %w", err)
			}
		}
		if profile.Agents.ReconnectStorm.Jitter != "" {
			if _, err := time.ParseDuration(profile.Agents.ReconnectStorm.Jitter); err != nil {
				return nil, fmt.Errorf("agents.reconnectStorm.jitter: %w", err)
			}
		}
	}
	return &profile, nil
}

func (p *scaleProfile) apply(cfg *config) error {
	if p == nil {
		return nil
	}
	duration, err := time.ParseDuration(p.Duration)
	if err != nil {
		return err
	}
	cfg.profileName = p.Name
	cfg.clusters = p.Clusters
	cfg.rps = p.RPS
	cfg.duration = duration
	cfg.resources = p.Resources
	cfg.resources.ProfileName = p.Name
	cfg.resources.EventsPerSecond = p.EventsPerSecond
	cfg.day2FailureDrill = append([]string{}, p.Day2FailureDrills...)
	if p.Agents.ReconnectStorm.Enabled {
		cfg.reconnectStorm.Enabled = true
		cfg.reconnectStorm.BatchPercent = p.Agents.ReconnectStorm.BatchPercent
		if cfg.reconnectStorm.BatchPercent == 0 {
			cfg.reconnectStorm.BatchPercent = 100
		}
		if p.Agents.ReconnectStorm.At != "" {
			cfg.reconnectStorm.AtDuration, err = time.ParseDuration(p.Agents.ReconnectStorm.At)
			if err != nil {
				return err
			}
		}
		if p.Agents.ReconnectStorm.Jitter != "" {
			cfg.reconnectStorm.JitterDuration, err = time.ParseDuration(p.Agents.ReconnectStorm.Jitter)
			if err != nil {
				return err
			}
		}
		if cfg.reconnectStorm.AtDuration == 0 {
			cfg.reconnectStorm.AtDuration = duration / 3
		}
		if cfg.reconnectStorm.JitterDuration == 0 {
			cfg.reconnectStorm.JitterDuration = 15 * time.Second
		}
	}
	return nil
}
