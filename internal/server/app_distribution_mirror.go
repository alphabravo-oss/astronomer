package server

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

func readDistributionInput(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("distribution metadata file is unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, errors.New("distribution metadata file is invalid")
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) == 0 || len(data) > 1<<20 {
		return nil, errors.New("distribution metadata file exceeds bounds")
	}
	return data, nil
}
func distributionMirrorPlan(cfg *config.Config) (*distribution.MirrorPlan, error) {
	if cfg.DistributionMirrorPlanPath == "" {
		if cfg.DistributionMirrorSignaturePath != "" || cfg.DistributionMirrorKeyRef != "" {
			return nil, errors.New("mirror endorsement configuration is incomplete")
		}
		return nil, nil
	}
	data, err := readDistributionInput(cfg.DistributionMirrorPlanPath)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	signature, err := readDistributionInput(cfg.DistributionMirrorSignaturePath)
	if err != nil {
		return nil, err
	}
	defer clear(signature)
	manifest, err := readDistributionInput(cfg.ReleaseManifestPath)
	if err != nil {
		return nil, err
	}
	defer clear(manifest)
	return distribution.VerifyMirrorPlan(context.Background(), data, signature, cfg.DistributionMirrorKeyRef, cfg.DeliverySourceTrustDirectory, version.Version, cfg.DeliveryBundleDigest, cfg.DeliveryBundleCatalogDigest, fmt.Sprintf("sha256:%x", sha256.Sum256(manifest)))
}
