package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/hibiken/asynq"
)

// CatalogSyncPayload contains parameters for catalog sync.
type CatalogSyncPayload struct {
	RepositoryURL string `json:"repository_url,omitempty"` // empty = sync all repos
}

// NewCatalogSyncTask creates a new catalog sync task.
func NewCatalogSyncTask(payload CatalogSyncPayload) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal catalog sync payload: %w", err)
	}
	return asynq.NewTask("catalog:sync", data), nil
}

// HandleCatalogSync syncs Helm repositories and updates chart listings.
func HandleCatalogSync(ctx context.Context, t *asynq.Task) error {
	var p CatalogSyncPayload
	if len(t.Payload()) > 0 {
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("unmarshal catalog sync payload: %w", err)
		}
	}

	if p.RepositoryURL != "" {
		slog.InfoContext(ctx, "syncing catalog repository", "url", p.RepositoryURL)
	} else {
		slog.InfoContext(ctx, "syncing all catalog repositories")
	}

	// TODO: Fetch Helm repo indices, parse chart metadata, upsert into catalog tables.

	slog.InfoContext(ctx, "catalog sync complete")
	return nil
}
