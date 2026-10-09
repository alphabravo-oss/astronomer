package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

type mirrorFlags struct{ plan, copy, target, manifest, output, endorsement, operatorKey, operatorTrust *string }

func registerMirrorFlags() mirrorFlags {
	return mirrorFlags{
		plan:          flag.String("plan-offline-mirror", "", "authenticate a closed offline transfer and prepare a complete v2 mirror plan"),
		copy:          flag.String("mirror-offline-transfer", "", "copy original signed offline subjects and all inventory images; verify mirrored runtime readback"),
		target:        flag.String("mirror-target", "", "exact operator target registry/repository base, without tag or credentials"),
		manifest:      flag.String("release-manifest-digest", "", "independently pinned release manifest SHA256 for operator endorsement"),
		output:        flag.String("mirror-output-directory", "", "new private mirror preparation directory"),
		endorsement:   flag.String("mirror-endorsement", "", "exact separately signed operator plan; execution recomputes it"),
		operatorKey:   flag.String("mirror-key-ref", "", "mounted operator endorsement public key reference"),
		operatorTrust: flag.String("mirror-trust-directory", "", "absolute mounted operator public trust directory"),
	}
}
func (flags mirrorFlags) selected() bool { return *flags.plan != "" || *flags.copy != "" }
func (flags mirrorFlags) run(repository, digest, catalogDigest, release, trustDirectory, provider, keyRef, identity, issuer, signature, networkFile, caFile string) error {
	if (*flags.plan == "") == (*flags.copy == "") || repository == "" || digest == "" || catalogDigest == "" || release == "" || trustDirectory == "" || *flags.target == "" || *flags.manifest == "" {
		return errors.New("one mirror action and independent release/trust/location pins required")
	}
	options := distribution.OfflineOptions{Repository: repository, Digest: digest, CatalogDigest: catalogDigest, Release: release, Profile: "builtins", TrustDirectory: trustDirectory, Policy: model.TrustPolicy{Provider: model.SignatureProvider(provider), KeyRef: keyRef, Identity: identity, Issuer: issuer}}
	archive := *flags.plan
	if archive == "" {
		archive = *flags.copy
	}
	payload, err := boundedFile(archive, 2*distribution.MaximumExpandedBytes)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if *flags.plan != "" {
		if *flags.output == "" || *flags.endorsement != "" || signature != "" || *flags.operatorKey != "" || *flags.operatorTrust != "" || networkFile != "" || caFile != "" {
			return errors.New("mirror preparation requires new output and no network/endorsement inputs")
		}
		schedule, _, _, err := planOfflineMirror(ctx, payload, options, *flags.target, *flags.manifest)
		if err != nil {
			return err
		}
		if err := os.Mkdir(*flags.output, 0700); err != nil {
			return errors.New("mirror output directory must be new")
		}
		success := false
		defer func() {
			if !success {
				_ = os.RemoveAll(*flags.output)
			}
		}()
		if err := writeJSON(filepath.Join(*flags.output, "mirror-plan.json"), schedule.Plan); err != nil {
			return err
		}
		if err := writeJSON(filepath.Join(*flags.output, "mirror-schedule.json"), schedule); err != nil {
			return err
		}
		success = true
		return nil
	}
	if *flags.output != "" || *flags.endorsement == "" || signature == "" || *flags.operatorKey == "" || *flags.operatorTrust == "" || networkFile == "" {
		return errors.New("mirror execution requires signed operator plan and explicit destination policy")
	}
	endorsement, err := boundedFile(*flags.endorsement, 1<<20)
	if err != nil {
		return err
	}
	detached, err := boundedFile(signature, 1<<20)
	if err != nil {
		return err
	}
	networkData, err := boundedFile(networkFile, 64<<10)
	if err != nil {
		return err
	}
	network, err := httpclient.ParseDestinationPolicy(string(networkData))
	if err != nil {
		return err
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
	client, err := httpclient.NewDestinationClient(network, httpclient.DestinationOptions{TLSConfig: tls, MaxResponseBytes: distribution.MaximumArchiveBytes})
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	// Authentication wraps the same guarded client in copyOfflineMirror. No
	// external shell tool or registry-provided URL can expand network authority.
	_, err = copyOfflineMirror(ctx, payload, mirrorExecutionOptions{Vendor: options, TargetBase: *flags.target, ReleaseManifestDigest: *flags.manifest, Endorsement: endorsement, Signature: detached, OperatorKeyRef: *flags.operatorKey, OperatorTrustDirectory: *flags.operatorTrust, Client: client, Network: network})
	return err
}
