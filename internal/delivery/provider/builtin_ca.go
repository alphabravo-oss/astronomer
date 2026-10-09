package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
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
	if len(ca) == 0 || len(ca) > protocol.MaxDeliveryCredentialValue {
		return false
	}
	count := 0
	for remaining := bytes.TrimSpace(ca); len(remaining) != 0; {
		if !bytes.HasPrefix(remaining, []byte("-----BEGIN CERTIFICATE-----")) {
			return false
		}
		block, rest := pem.Decode(remaining)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return false
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !certificate.IsCA {
			return false
		}
		count++
		if count > 64 {
			return false
		}
		remaining = bytes.TrimSpace(rest)
	}
	return count != 0
}

func builtinCAEligible(row sqlc.ListClusterDeliveryAssignmentsRow) bool {
	return row.Scope == string(model.ScopePlatform) && row.SourceType == string(model.SourceHelmOCI) &&
		row.AuthMode == string(model.AuthNone) && row.CredentialEncrypted == "" && row.CaBundleEncrypted == ""
}

func (p *Provider) applyBuiltinCAMetadata(ctx context.Context, row sqlc.ListClusterDeliveryAssignmentsRow, assignment *protocol.DeliveryAssignmentV2) (string, error) {
	if len(p.builtinCA) == 0 || !builtinCAEligible(row) {
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
