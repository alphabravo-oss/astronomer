package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// BuiltinCAAuthorizer binds a candidate to the same verified catalog used by
// the provisioner and to independent operator destination authority.
type BuiltinCAAuthorizer func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, protocol.DeliveryAssignmentV2) (bool, error)

// NewWithBuiltinCA captures public TLS trust. It does not configure registry
// authentication or change any user source's independently stored trust.
func NewWithBuiltinCA(queries Querier, decryptor Decryptor, ca []byte, authorize BuiltinCAAuthorizer) (*Provider, error) {
	if authorize == nil || !validBuiltinCA(ca) {
		return nil, errors.New("bounded builtin CA and authorizer are required")
	}
	p := New(queries, decryptor)
	p.builtinCA = bytes.Clone(ca)
	digest := sha256.Sum256(ca)
	p.builtinCAIdentity = "sha256:" + hex.EncodeToString(digest[:])
	p.authorizeBuiltinCA = authorize
	return p, nil
}

func validBuiltinCA(ca []byte) bool {
	if len(ca) > protocol.MaxDeliveryCredentialValue {
		return false
	}
	_, err := httpclient.ParseCertificateAuthorities(ca)
	return err == nil
}

func builtinCAEligible(row sqlc.ListClusterDeliveryAssignmentsRow) bool {
	return row.Scope == string(model.ScopePlatform) && row.SourceType == string(model.SourceHelmOCI) &&
		row.AuthMode == string(model.AuthNone) && row.CredentialKeyID == "" && row.CaBundleKeyID == ""
}

func (p *Provider) applyBuiltinCAMetadata(ctx context.Context, row sqlc.ListClusterDeliveryAssignmentsRow, assignment *protocol.DeliveryAssignmentV2) (string, error) {
	if len(p.builtinCA) == 0 || !builtinCAEligible(row) {
		return "", nil
	}
	if len(p.builtinSigningKey) != 0 && !p.builtinSigningPolicyMatches(row) {
		return "", nil
	}
	approved, err := p.authorizeBuiltinCA(ctx, row.ClusterID, row.ProjectID, row.SourceID, *assignment)
	if err != nil {
		return "", errors.New("builtin source TLS trust authorization failed")
	}
	if !approved {
		return "", nil
	}
	assignment.Source.CredentialSecret = protocol.ObjectNames(row.ProjectID.String(), row.ID.String()).AuthSecret
	return p.builtinCAIdentity, nil
}
