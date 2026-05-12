package handler

// Test-only helpers for cluster_registries_test.go. Kept in a separate
// _test.go-suffix-less file so we can use the package's internal types
// (handler.K8sRequester returns *protocol.K8sResponsePayload, which we
// have to wrap for the test). Build-tag-gated to test so it doesn't
// land in production binaries.

import (
	"context"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// fakeTestRequester implements handler.K8sRequester. The Test/* handler
// uses K8sRequester through the package interface, which (unlike the
// ProjectK8sRequester used by the worker tests) returns
// *protocol.K8sResponsePayload directly.
type fakeTestRequester struct {
	status     int
	body       string
	gotPath    string
	gotHeaders map[string]string
	gotMethod  string
	err        error
}

func (f *fakeTestRequester) Do(_ context.Context, _, method, path string, _ []byte, headers map[string]string) (*protocol.K8sResponsePayload, error) {
	f.gotPath = path
	f.gotMethod = method
	f.gotHeaders = headers
	if f.err != nil {
		return nil, f.err
	}
	return &protocol.K8sResponsePayload{
		StatusCode: f.status,
		Body:       f.body,
	}, nil
}
