// Package handler — Vault resolver hook helpers (migration 067).
//
// This file centralises the small "resolve a values blob at install
// time" plumbing the three install paths (catalog.go,
// cluster_templates.go, tools.go) need. Each handler stores a
// *avault.Resolver pointer via SetVaultResolver; the helper here
// invokes it best-effort and returns a typed error the install
// handlers convert to a 400.
//
// Design rule: the resolved blob is ONLY held in the local function
// stack of the install handler. We never write the resolved blob to
// the helm_installations.values_override / tool_installation / etc.
// columns — what the DB sees is always the operator's original
// ${vault://...}-bearing blob, so on every upgrade the latest Vault
// value is fetched fresh.

package handler

import (
	"context"
	"errors"

	"github.com/google/uuid"

	avault "github.com/alphabravocompany/astronomer-go/internal/vault"
)

// vaultResolveBlob is the small wrapper every install path calls right
// before enqueuing the install task. It returns the substituted blob
// when there are vault refs; when there are none it returns the input
// unchanged so the caller can use the result unconditionally.
//
// resolver may be nil — in that case the function returns the blob
// unchanged ONLY IF the blob has no vault references. If the operator
// wrote a vault ref but the resolver isn't wired we MUST fail (we'd
// silently install the literal "${vault://...}" string into the
// cluster otherwise, which is exactly the bug this whole subsystem
// exists to prevent).
func vaultResolveBlob(ctx context.Context, resolver *avault.Resolver, projectID uuid.UUID, blob string) (string, error) {
	refs := avault.Parse(blob)
	if len(refs) == 0 {
		return blob, nil
	}
	if resolver == nil {
		return "", errors.New("values contain ${vault://...} references but the Vault resolver is not configured on this server")
	}
	return resolver.Resolve(ctx, projectID, blob)
}
