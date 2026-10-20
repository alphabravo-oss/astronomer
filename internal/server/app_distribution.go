package server

import (
	"crypto/tls"
	"crypto/x509"
	"errors"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

func newDistributionLoader(cfg *config.Config, ca []byte) (*distribution.Loader, error) {
	policy, err := httpclient.ParseDestinationPolicy(cfg.DistributionDestinationPolicy)
	if err != nil {
		return nil, err
	}
	development := !config.IsProduction(cfg) && cfg.DeliveryBundleRepository == "" && cfg.ReleaseManifestPath == ""
	if development {
		return distribution.New(distribution.Options{Development: true, Network: policy, LocalRegistry: cfg.RegistryLocal})
	}
	trust := &tls.Config{MinVersion: tls.VersionTLS12}
	if len(ca) != 0 {
		roots, poolErr := x509.SystemCertPool()
		if poolErr != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(ca) {
			return nil, errors.New("builtin registry CA contains no certificates")
		}
		trust.RootCAs = roots
	}
	client, err := httpclient.NewDestinationClient(policy, httpclient.DestinationOptions{TLSConfig: trust, MaxResponseBytes: distribution.MaximumArchiveBytes})
	if err != nil {
		return nil, err
	}
	mirrorPlan, err := distributionMirrorPlan(cfg)
	if err != nil {
		return nil, err
	}
	return distribution.New(distribution.Options{MirrorPlan: mirrorPlan, Repository: cfg.DeliveryBundleRepository, Digest: cfg.DeliveryBundleDigest, CatalogDigest: cfg.DeliveryBundleCatalogDigest, Release: version.Version, Profile: "builtins", Policy: model.TrustPolicy{Provider: model.SignatureCosignKeyless, Identity: cfg.DeliveryBundleCertificateIdentity, Issuer: cfg.DeliveryBundleOIDCIssuer}, TrustDirectory: cfg.DeliverySourceTrustDirectory, Client: client, Network: policy, LocalRegistry: cfg.RegistryLocal})
}
