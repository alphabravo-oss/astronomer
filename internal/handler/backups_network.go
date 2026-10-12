package handler

import (
	"crypto/tls"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// SetS3Network injects operator authority independently of stored endpoint URLs.
// A storage configuration cannot grant itself private network access.
func (h *BackupHandler) SetS3Network(policy httpclient.DestinationPolicy, trust *tls.Config) error {
	client, err := httpclient.NewDestinationClient(policy, httpclient.DestinationOptions{TLSConfig: trust, Timeout: 15 * time.Second, MaxResponseBytes: 4096})
	if err != nil {
		return err
	}
	policy = policy.Clone()
	h.s3Policy = &policy
	h.httpClient = client
	return nil
}
