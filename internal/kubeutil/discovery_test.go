package kubeutil

import "testing"

func TestDiscoveryClientForConfigRejectsNil(t *testing.T) {
	if _, err := DiscoveryClientForConfig(nil); err == nil {
		t.Fatal("expected nil config error")
	}
}

func TestRESTMapperForConfigRejectsNil(t *testing.T) {
	if _, err := RESTMapperForConfig(nil); err == nil {
		t.Fatal("expected nil config error")
	}
}

func TestRESTMapperForDiscoveryRejectsNil(t *testing.T) {
	if _, err := RESTMapperForDiscovery(nil); err == nil {
		t.Fatal("expected nil discovery client error")
	}
}
