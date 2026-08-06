package charlie

import "testing"

func TestFixedDiscoveryQualificationUsesProductionCatalogCompiler(t *testing.T) {
	mixed, err := qualifyFixedDiscovery(DiscoveryQualificationMixed)
	if err != nil {
		t.Fatal(err)
	}
	readName, writeName := ReadCapabilityCatalog()[0].Name, WriteCapabilityCatalog()[0].Name
	if !mixed.CandidateEnabled || mixed.AcceptedCount != 2 || mixed.RejectedCount != 1 || !mixed.MalformedRejected || !mixed.CatalogBound || mixed.DisclosureDigest == "" ||
		len(mixed.AcceptedNames) != 2 || mixed.AcceptedNames[0] != readName || mixed.AcceptedNames[1] != writeName {
		t.Fatalf("mixed catalog qualification is incomplete: %+v", mixed)
	}
	malformed, err := qualifyFixedDiscovery(DiscoveryQualificationMalformed)
	if err != nil {
		t.Fatal(err)
	}
	if malformed.CandidateEnabled || malformed.AcceptedCount != 0 || malformed.RejectedCount != 1 || !malformed.MalformedRejected || malformed.CatalogBound || malformed.DisclosureDigest != "" || len(malformed.AcceptedNames) != 0 {
		t.Fatalf("malformed catalog was not disabled: %+v", malformed)
	}
}

func TestFixedDiscoveryQualificationRejectsCallerSelectedCases(t *testing.T) {
	for _, scenario := range []string{"", "custom", "mixed_catalog?capability=attacker"} {
		if _, err := qualifyFixedDiscovery(scenario); err == nil {
			t.Fatalf("arbitrary discovery qualification scenario %q accepted", scenario)
		}
	}
}
