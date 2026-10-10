package distribution

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
)

// CopyDatabaseLayer writes only a constructor-authenticated exact database
// claim into caller-owned quarantine. Success requires the complete original
// bytes and SHA256. It never extracts content or assesses database freshness.
func (graph *VerifiedOfflineDatabaseGraph) CopyDatabaseLayer(ctx context.Context, subject string, source io.Reader, quarantine io.Writer) error {
	if graph == nil || graph.status.Verification != "verified" || ctx == nil || source == nil {
		return errors.New("authenticated offline database graph required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	claim, permitted := graph.layers[subject]
	if !permitted || !digestPattern.MatchString(subject) || claim.Digest != subject || claim.Size <= 0 || claim.Size > MaximumDatabaseLayerBytes {
		return errors.New("offline database file is outside authenticated claims")
	}
	if quarantine == nil {
		quarantine = io.Discard
	}
	hash := sha256.New()
	written, err := io.CopyBuffer(io.MultiWriter(quarantine, hash), io.LimitReader(databaseContextReader{context: ctx, source: source}, claim.Size+1), make([]byte, 64<<10))
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if err != nil || written != claim.Size || fmt.Sprintf("sha256:%x", hash.Sum(nil)) != subject {
		return errors.New("offline database file size or complete digest differs from authenticated claim")
	}
	return nil
}

type databaseContextReader struct {
	context context.Context
	source  io.Reader
}

func (reader databaseContextReader) Read(buffer []byte) (int, error) {
	if err := reader.context.Err(); err != nil {
		return 0, err
	}
	return reader.source.Read(buffer)
}
