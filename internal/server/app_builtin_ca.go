package server

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/builtin"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/provider"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
)

func builtinCAAuthorizer(loader builtin.CatalogLoader, policy httpclient.DestinationPolicy) provider.BuiltinCAAuthorizer {
	policy = policy.Clone()
	return func(ctx context.Context, cluster, project, source uuid.UUID, assignment protocol.DeliveryAssignmentV2) (bool, error) {
		catalog, err := loader.Load(ctx)
		if err != nil {
			return false, errors.New("verified builtin catalog unavailable")
		}
		if !builtin.AuthorizesCatalogAssignment(catalog, cluster, project, source, assignment) {
			return false, nil
		}
		endpoint := "https://" + strings.TrimPrefix(assignment.Source.URL, "oci://")
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := policy.ValidateURL(checkCtx, endpoint); err != nil {
			return false, errors.New("builtin registry destination is not approved")
		}
		return true, nil
	}
}

func (c *productionComposition) configureBuiltinCA(cfg *config.Config, loader builtin.CatalogLoader, ca []byte) error {
	if len(ca) == 0 {
		return nil
	}
	policy, err := httpclient.ParseDestinationPolicy(cfg.DistributionDestinationPolicy)
	if err != nil {
		return err
	}
	state, err := provider.NewWithBuiltinCA(c.queries, c.encryptor, ca, builtinCAAuthorizer(loader, policy))
	if err != nil {
		return err
	}
	c.hub.SetDeliveryStateProvider(state)
	return nil
}

func readBuiltinCA(filename string) ([]byte, error) {
	if filename == "" {
		return nil, nil
	}
	info, err := os.Stat(filename)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, errors.New("builtin registry CA must be a bounded regular file")
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, errors.New("builtin registry CA is unavailable")
	}
	defer func() { _ = file.Close() }()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > 1<<20 {
		return nil, errors.New("builtin registry CA must be a bounded regular file")
	}
	ca, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(ca) == 0 || len(ca) > 1<<20 {
		return nil, errors.New("builtin registry CA exceeds its read boundary")
	}
	return ca, nil
}
