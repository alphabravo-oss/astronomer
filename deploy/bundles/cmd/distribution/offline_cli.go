package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
)

func offlineTransferFromFlags(exportLayout, output, archive, importDirectory, repository, digest, catalogDigest, release, trustDirectory, provider, keyRef, identity, issuer, networkFile, caFile string) error {
	if (exportLayout == "") == (archive == "") || repository == "" || digest == "" || catalogDigest == "" || release == "" || trustDirectory == "" {
		return errors.New("one transfer action and caller-pinned identity/trust required")
	}
	options := distribution.OfflineOptions{Repository: repository, Digest: digest, CatalogDigest: catalogDigest, Release: release, Profile: "builtins", TrustDirectory: trustDirectory, Policy: model.TrustPolicy{Provider: model.SignatureProvider(provider), KeyRef: keyRef, Identity: identity, Issuer: issuer}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if archive != "" {
		if networkFile != "" || caFile != "" || output != "" {
			return errors.New("offline verification does not accept network configuration")
		}
		if importDirectory != "" {
			return importOfflineTransfer(ctx, archive, importDirectory, options)
		}
		payload, err := boundedFile(archive, 2*distribution.MaximumExpandedBytes)
		if err != nil {
			return err
		}
		_, _, err = readOfflineTransfer(ctx, payload, options)
		return err
	}
	if output == "" || importDirectory != "" {
		return errors.New("export requires a new archive output")
	}
	network := httpclient.DestinationPolicy{}
	if networkFile != "" {
		data, err := boundedFile(networkFile, 64<<10)
		if err != nil {
			return err
		}
		network, err = httpclient.ParseDestinationPolicy(string(data))
		if err != nil {
			return err
		}
	}
	ca, cleanup, err := snapshotRegistryCA(caFile)
	if err != nil {
		return err
	}
	defer cleanup()
	tls, err := httpclient.TLSForCA(ca)
	if err != nil {
		return err
	}
	client, err := newRegistryToolDestinationClient(network, tls)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	store, err := credentials.NewStoreFromDocker(credentials.StoreOptions{})
	if err != nil {
		return errors.New("protected registry credentials unavailable")
	}
	authenticated := &http.Client{Transport: registryAuthenticationTransport{client: &auth.Client{Client: client, Credential: credentials.Credential(store), Cache: auth.NewCache()}}, Timeout: client.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	payload, err := captureOfflineTransfer(ctx, exportLayout, options, authenticated, network)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("export destination must be new")
	}
	_, writeErr := file.Write(payload)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(output)
		return errors.New("offline export write failed")
	}
	return nil
}
