package handler

import "testing"

// bslItem builds an unstructured BSL as returned by listVeleroBSLs.
func bslItem(name, provider, bucket string, isDefault bool) map[string]any {
	return map[string]any{
		"metadata": map[string]any{"name": name},
		"spec": map[string]any{
			"provider": provider,
			"default":  isDefault,
			"objectStorage": map[string]any{
				"bucket": bucket,
			},
		},
		"status": map[string]any{"phase": "Available"},
	}
}

func TestResolveBSLStore(t *testing.T) {
	bsls := []map[string]any{
		bslItem("aws-primary", "aws", "prod-backups", false),
		bslItem("aws-default", "aws", "default-backups", true),
	}

	// Named lookup resolves the exact BSL.
	if s, ok := resolveBSLStore(bsls, "aws-primary"); !ok || s.Bucket != "prod-backups" {
		t.Fatalf("named lookup: got (%+v, %v)", s, ok)
	}
	// Empty name resolves the default-flagged BSL.
	if s, ok := resolveBSLStore(bsls, ""); !ok || s.Bucket != "default-backups" {
		t.Fatalf("default lookup: got (%+v, %v)", s, ok)
	}
	// Unknown name resolves nothing (so the caller falls back, not blocks).
	if s, ok := resolveBSLStore(bsls, "does-not-exist"); ok {
		t.Fatalf("unknown name should not resolve, got %+v", s)
	}
	// Sole BSL is treated as the default when no default flag is set.
	single := []map[string]any{bslItem("only", "gcp", "solo-bucket", false)}
	if s, ok := resolveBSLStore(single, ""); !ok || s.Bucket != "solo-bucket" {
		t.Fatalf("sole-bsl default: got (%+v, %v)", s, ok)
	}
}

func TestTargetHasMatchingStore(t *testing.T) {
	src := VeleroBSLSummary{Provider: "aws", Bucket: "prod-backups"}

	// Target with a BSL pointing at the same bucket+provider matches.
	sameStore := []map[string]any{
		bslItem("target-bsl", "aws", "prod-backups", true),
	}
	if !targetHasMatchingStore(sameStore, src) {
		t.Error("expected match when bucket+provider agree")
	}

	// This is the bug being fixed: the target has a BSL, but it points at a
	// DIFFERENT bucket. The old code (len(bsls) > 0) passed; the fix must
	// reject it so the restore 409s instead of failing later in the poller.
	otherStore := []map[string]any{
		bslItem("target-bsl", "aws", "different-bucket", true),
	}
	if targetHasMatchingStore(otherStore, src) {
		t.Error("expected NO match when the target BSL points at a different bucket")
	}

	// Same bucket but a different provider is not the same store.
	crossProvider := []map[string]any{
		bslItem("target-bsl", "gcp", "prod-backups", true),
	}
	if targetHasMatchingStore(crossProvider, src) {
		t.Error("expected NO match when providers differ")
	}
}
