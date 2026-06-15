package kubeutil

import "testing"

func TestServerSideApplyPath(t *testing.T) {
	path := ServerSideApplyPath("/api/v1/namespaces/default/secrets/name", ApplyOptions{
		FieldManager: "astronomer",
		Force:        true,
	})
	if path != "/api/v1/namespaces/default/secrets/name?fieldManager=astronomer&force=true" {
		t.Fatalf("unexpected path: %s", path)
	}
}

func TestServerSideApplyPathAppendsQuery(t *testing.T) {
	path := ServerSideApplyPath("/api/v1/namespaces/default/secrets/name?dryRun=All", ApplyOptions{
		FieldManager: "astronomer",
	})
	if path != "/api/v1/namespaces/default/secrets/name?dryRun=All&fieldManager=astronomer" {
		t.Fatalf("unexpected path: %s", path)
	}
}

func TestApplyPatchHeaders(t *testing.T) {
	headers := ApplyPatchHeaders()
	if headers["Content-Type"] != ApplyPatchContentType {
		t.Fatalf("unexpected content type: %#v", headers)
	}
	if headers["Accept"] != "application/json" {
		t.Fatalf("unexpected accept header: %#v", headers)
	}
}
