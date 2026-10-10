package distribution

// DatabaseCacheContent is DATA derived from the fully authenticated compressed
// layer and closed archive. It describes the expanded database file, not a scan
// observation or permission. A scanner job must measure its own cache before
// and after using it and bind the actual job/report/current assignment separately.
// It deliberately contains no timestamp manufactured from download/completion.
type DatabaseCacheContent struct {
	Role, File, Digest string
	Size               int64
}

type databaseInspectedContent struct {
	metadata []byte
	database DatabaseCacheContent
}

func (verified *VerifiedDatabaseUpdatePayload) CacheContent() (DatabaseCacheContent, bool) {
	if verified == nil {
		return DatabaseCacheContent{}, false
	}
	_, snapshot, valid := verified.Snapshot()
	content := verified.content
	expectedFile := "trivy.db"
	if snapshot.Role == "trivy-java-database" {
		expectedFile = "trivy-java.db"
	}
	if !valid || content.Role != snapshot.Role || content.File != expectedFile || !digestPattern.MatchString(content.Digest) || content.Size <= 0 || content.Size > MaximumDatabaseExpandedBytes {
		return DatabaseCacheContent{}, false
	}
	return content, true
}
