package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/builtin"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type publicTrustBinding struct{ CA, SigningKey bool }

// NewBuiltinQAConsumer composes the constructor-only keyed QA consumer lane.
// Its returned loader authenticates with the same captured public material
// delivered by its provider. Production server configuration remains OIDC-only.
func NewBuiltinQAConsumer(queries Querier, decryptor Decryptor, snapshot *resolver.PublicKeySnapshot, ca []byte, options distribution.Options) (*Provider, *distribution.Loader, error) {
	if snapshot == nil || options.Development || options.Profile != "builtins" || options.Policy.Provider != model.SignatureCosignKey || options.Policy.AllowUnsigned || options.Policy.KeyRef != snapshot.Reference() || options.Policy.Validate() != nil || (options.TrustDirectory != "" && options.TrustDirectory != snapshot.Directory()) {
		return nil, nil, errors.New("QA consumer requires exact captured key policy")
	}
	options.TrustDirectory = snapshot.Directory()
	options.Network = options.Network.Clone()
	loader, err := distribution.New(options)
	if err != nil {
		return nil, nil, err
	}
	authorize := func(ctx context.Context, cluster, project, source uuid.UUID, assignment protocol.DeliveryAssignmentV2) (bool, error) {
		return builtin.AuthorizeCatalogSource(ctx, loader, options.Network, cluster, project, source, assignment)
	}
	p, err := newWithBuiltinPublicKey(queries, decryptor, snapshot, ca, authorize)
	if err != nil {
		return nil, nil, err
	}
	return p, loader, nil
}

func newWithBuiltinPublicKey(queries Querier, decryptor Decryptor, snapshot *resolver.PublicKeySnapshot, ca []byte, authorize BuiltinCAAuthorizer) (*Provider, error) {
	if snapshot == nil || authorize == nil {
		return nil, errors.New("captured public signing trust and authorizer required")
	}
	material := snapshot.Material()
	defer clear(material)
	if len(material) == 0 || len(material) > protocol.MaxDeliveryCredentialValue {
		return nil, errors.New("captured public signing trust unavailable")
	}
	p := New(queries, decryptor)
	if len(ca) != 0 {
		var err error
		p, err = NewWithBuiltinCA(queries, decryptor, ca, authorize)
		if err != nil {
			return nil, err
		}
	}
	p.authorizeBuiltinCA = authorize
	p.builtinSigningKey = bytes.Clone(material)
	p.builtinSigningKeyRef = snapshot.Reference()
	digest := sha256.Sum256(material)
	p.builtinSigningIdentity = "sha256:" + hex.EncodeToString(digest[:])
	return p, nil
}

func (p *Provider) builtinSigningAuthorization(ctx context.Context, row sqlc.ListClusterDeliveryAssignmentsRow, assignment protocol.DeliveryAssignmentV2) (string, error) {
	if len(p.builtinSigningKey) == 0 || !builtinCAEligible(row) {
		return "", nil
	}
	if !p.builtinSigningPolicyMatches(row) {
		return "", nil
	}
	approved, err := p.authorizeBuiltinCA(ctx, row.ClusterID, row.ProjectID, row.SourceID, assignment)
	if err != nil {
		return "", errors.New("builtin public signing trust authorization failed")
	}
	if !approved {
		return "", nil
	}
	return p.builtinSigningIdentity, nil
}

func (p *Provider) builtinSigningPolicyMatches(row sqlc.ListClusterDeliveryAssignmentsRow) bool {
	var trust model.TrustPolicy
	return decodeStrict(row.TrustPolicy, &trust) == nil && trust.Provider == model.SignatureCosignKey && trust.KeyRef == p.builtinSigningKeyRef && !trust.AllowUnsigned
}
