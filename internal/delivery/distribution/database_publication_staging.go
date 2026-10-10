package distribution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// PublicationWorkingDirectory creates one new private producer directory only
// after checking the configured cache origin/owner and conservative two-layer
// capacity plus reserve. It grants no network, publication or signing authority.
// Complete originals/partial failure artifacts are retained for operator-owned
// cleanup; no caller-selected or recursive cleanup path is used.
func (authority *DatabaseAuthority) PublicationWorkingDirectory(ctx context.Context, proof *VerifiedDatabaseAuthorityBase, cache string) (string, error) {
	if ctx == nil || !filepath.IsAbs(cache) || filepath.Clean(cache) != cache {
		return "", errors.New("database publication cache path invalid")
	}
	if err := authority.Revalidate(ctx, proof); err != nil {
		return "", err
	}
	root, before, err := openSupplierTransferDirectory(cache, MaximumDatabaseLayerBytes)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	if err := supplierRefreshCapacity(root, 2*MaximumDatabaseLayerBytes); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	directory, err := os.MkdirTemp(cache, ".database-publication-")
	if err != nil {
		return "", errors.New("database publication private staging unavailable")
	}
	current, rootError := os.Lstat(cache)
	child, childError := os.Lstat(directory)
	if rootError != nil || childError != nil || !os.SameFile(current, before) || !child.IsDir() || child.Mode().Perm() != 0700 {
		return "", errors.New("database publication staging origin changed")
	}
	opened, _, err := openSupplierTransferDirectory(directory, MaximumDatabaseLayerBytes)
	if err != nil {
		return "", err
	}
	_ = opened.Close()
	if err := authority.Revalidate(ctx, proof); err != nil {
		return "", err
	}
	return directory, nil
}
