package main

import (
	"testing"
	"time"
)

func TestRealEstateManifestRejectsShortcuts(t *testing.T) {
	m := estateTestManifest()
	if err := m.validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*estateManifest){
		func(m *estateManifest) { m.Members = m.Members[:1] },
		func(m *estateManifest) { m.Tier = 5 },
		func(m *estateManifest) { m.Members[1].ClusterID = m.Members[0].ClusterID },
		func(m *estateManifest) { m.Members[0].Assignments = nil },
		func(m *estateManifest) { m.Members[0].Metrics.URL = "https://user:password@metrics.test/metrics" },
	} {
		candidate := estateTestManifest()
		mutate(&candidate)
		if candidate.validate() == nil {
			t.Fatal("invalid manifest accepted")
		}
	}
}
func TestRealEstateWindows(t *testing.T) {
	for _, cfg := range []*config{
		{realEstate: "manifest", duration: 29 * time.Minute, warmup: 5 * time.Minute, rps: 1},
		{realEstate: "manifest", duration: 30 * time.Minute, warmup: 4 * time.Minute, rps: 1},
		{realEstate: "manifest", duration: 30 * time.Minute, warmup: 5 * time.Minute, rps: 1, certification: true},
	} {
		if validateEstateConfig(cfg) == nil {
			t.Fatal("invalid shortcut accepted")
		}
	}
}

func TestRealEstateAcceptsLoopbackPortForward(t *testing.T) {
	for _, endpoint := range []string{"http://127.0.0.1:8081/metrics", "http://[::1]:8081/metrics", "https://agent.test/metrics"} {
		if err := validateEstateURL(endpoint, true); err != nil {
			t.Fatal(err)
		}
	}
	for _, endpoint := range []string{"http://localhost:8081/metrics", "http://10.0.0.1/metrics", "https://a/metrics?token=x", "https://u:p@a/metrics"} {
		if validateEstateURL(endpoint, true) == nil {
			t.Fatal("unsafe target accepted")
		}
	}
}
