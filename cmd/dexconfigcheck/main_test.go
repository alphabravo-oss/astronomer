package main

import "testing"

func TestValidateDexRuntimeContract(t *testing.T) {
	valid := config{Issuer: "https://dex.example.com"}
	valid.Storage.Type = "kubernetes"
	valid.Storage.Config.InCluster = true
	valid.Web.HTTP = "0.0.0.0:5556"
	valid.StaticClients = append(valid.StaticClients, struct {
		ID           string   `yaml:"id"`
		RedirectURIs []string `yaml:"redirectURIs"`
		Secret       string   `yaml:"secret"`
		Public       bool     `yaml:"public"`
		TrustedPeers []string `yaml:"trustedPeers"`
	}{ID: "astronomer", RedirectURIs: []string{"https://platform.example/api/v1/auth/callback/dex"}, Secret: "synthetic"})
	if err := validate(valid); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*config){
		"credential issuer":     func(c *config) { c.Issuer = "https://user:pass@dex.example.com" },
		"wrong storage":         func(c *config) { c.Storage.Type = "memory" },
		"missing client secret": func(c *config) { c.StaticClients[0].Secret = "" },
		"insecure redirect":     func(c *config) { c.StaticClients[0].RedirectURIs = []string{"http://platform.example/callback"} },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			candidate.StaticClients = append([]struct {
				ID           string   `yaml:"id"`
				RedirectURIs []string `yaml:"redirectURIs"`
				Secret       string   `yaml:"secret"`
				Public       bool     `yaml:"public"`
				TrustedPeers []string `yaml:"trustedPeers"`
			}{}, valid.StaticClients...)
			mutate(&candidate)
			if err := validate(candidate); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}
