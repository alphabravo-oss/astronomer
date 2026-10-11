//go:build unix && integration

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	ociregistry "oras.land/oras-go/v2/registry"
)

// This emits an operator endorsement only after the complete canonical graph
// has passed actual certificate verification. It does not copy any registry
// object, confer destination policy, or assert that a scan used these bytes.
func localQAWriteMirror(t *testing.T, ctx context.Context, output, targetBase string, options distribution.OfflineOptions, signed []distribution.StoredSubject, blobs map[string][]byte, manifestDigest string) string {
	t.Helper()
	target, err := ociregistry.ParseReference(targetBase)
	if err != nil || target.Reference != "" || target.Registry != strings.ToLower(target.Registry) || !strings.ContainsAny(target.Registry, ".:") || targetBase != target.Registry+"/"+target.Repository || len(targetBase) > 1024 {
		t.Fatal("local QA operator mirror target invalid")
	}
	var root ocispec.Manifest
	if closedJSON(blobs[options.Digest], &root) != nil || len(root.Layers) != 1 {
		t.Fatal("local QA verified parent unavailable")
	}
	members, bom, err := distribution.ReadClosedArchive(blobs[root.Layers[0].Digest.String()], options.Release, options.CatalogDigest)
	if err != nil {
		t.Fatal("local QA original closed parent unavailable")
	}
	plan := distribution.MirrorPlan{SchemaVersion: 1, Release: options.Release, DistributionDigest: options.Digest, CatalogDigest: options.CatalogDigest, ReleaseManifestDigest: manifestDigest, Rewrites: []distribution.Rewrite{}}
	schedule := mirrorSchedule{SchemaVersion: 1, DistributionRepository: mirrorRepository(targetBase, options.Repository), Copies: []mirrorCopy{}, Assets: []mirrorAsset{}}
	rewrites := map[string]distribution.Rewrite{}
	for _, subject := range signed {
		schedule.Copies = append(schedule.Copies, mirrorCopy{Kind: "signed_artifact", Source: subject.Repository + "@" + subject.Digest, Target: mirrorRepository(targetBase, subject.Repository) + "@" + subject.Digest, SignatureDigest: subject.SignatureDigest})
	}
	for _, chart := range bom.Charts {
		source := "oci://" + chart.Repository
		rewrites[source] = distribution.Rewrite{Kind: "chart_repository", Source: source, Target: "oci://" + mirrorRepository(targetBase, chart.Repository)}
	}
	for _, asset := range bom.Assets {
		var identity string
		for _, member := range bom.Members {
			if member.Path == asset.MemberPath {
				identity = member.Digest
			}
		}
		if identity == "" {
			t.Fatal("local QA mirror prerequisite missing")
		}
		projected := mirrorAsset{ID: asset.ID, MemberPath: asset.MemberPath, MemberDigest: identity, Images: []string{}}
		for _, image := range asset.Images {
			repo, subject, e := contentSubject(image)
			if e != nil {
				t.Fatal("local QA asset image identity invalid")
			}
			projected.Images = append(projected.Images, mirrorRepository(targetBase, repo)+"@"+subject)
		}
		sort.Strings(projected.Images)
		schedule.Assets = append(schedule.Assets, projected)
		if asset.PublishedSubject != "" {
			repo, subject, e := contentSubject(asset.PublishedSubject)
			if e != nil {
				t.Fatal("local QA original DB subject invalid")
			}
			rewrites[asset.PublishedSubject] = distribution.Rewrite{Kind: "artifact", Source: asset.PublishedSubject, Target: mirrorRepository(targetBase, repo) + "@" + subject}
		}
	}
	var inventory distribution.Inventory
	found := false
	for _, profile := range bom.Profiles {
		if profile.Name == options.Profile {
			if found || closedJSON(members[profile.InventoryPath], &inventory) != nil || inventory.SchemaVersion != 1 {
				t.Fatal("local QA measured profile inventory invalid")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("local QA measured profile unavailable")
	}
	for _, image := range inventory.Images {
		repo, subject, e := contentSubject(image.Reference)
		if e != nil {
			t.Fatal("local QA profile image identity invalid")
		}
		destination := mirrorRepository(targetBase, repo) + "@" + subject
		rewrites[image.Reference] = distribution.Rewrite{Kind: "image", Source: image.Reference, Target: destination}
		schedule.Copies = append(schedule.Copies, mirrorCopy{Kind: "image", Source: image.Reference, Target: destination})
	}
	for _, rewrite := range rewrites {
		plan.Rewrites = append(plan.Rewrites, rewrite)
	}
	sort.Slice(plan.Rewrites, func(i, j int) bool { return plan.Rewrites[i].Source < plan.Rewrites[j].Source })
	sort.Slice(schedule.Copies, func(i, j int) bool { return schedule.Copies[i].Source < schedule.Copies[j].Source })
	sort.Slice(schedule.Assets, func(i, j int) bool { return schedule.Assets[i].ID < schedule.Assets[j].ID })
	if len(plan.Rewrites) == 0 || len(plan.Rewrites) > 4096 || len(schedule.Copies) > 4096 {
		t.Fatal("local QA mirror exceeds closed bounds")
	}
	payload, err := json.Marshal(plan)
	if err != nil || len(payload) > 1<<20 {
		t.Fatal("local QA mirror payload invalid")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("local QA operator signing key unavailable")
	}
	hash := sha256.Sum256(payload)
	signature, err := ecdsa.SignASN1(rand.Reader, key, hash[:])
	if err != nil {
		t.Fatal("local QA mirror endorsement unavailable")
	}
	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal("local QA operator public key unavailable")
	}
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public})
	directory := filepath.Join(output, "trust")
	if err = os.WriteFile(filepath.Join(directory, "mirror.pub"), publicPEM, 0600); err != nil {
		t.Fatal("local QA operator public mount unavailable")
	}
	signatureText := []byte(base64.StdEncoding.EncodeToString(signature))
	verified, err := distribution.VerifyMirrorPlan(ctx, payload, signatureText, "mirror", directory, options.Release, options.Digest, options.CatalogDigest, manifestDigest)
	if err != nil || verified.Identity() == "" {
		t.Fatal("local QA actual operator endorsement verification failed")
	}
	changed := append([]byte(nil), payload...)
	changed[0] = '!'
	if _, err = distribution.VerifyMirrorPlan(ctx, changed, signatureText, "mirror", directory, options.Release, options.Digest, options.CatalogDigest, manifestDigest); err == nil {
		t.Fatal("local QA changed endorsement accepted")
	}
	if _, err = distribution.VerifyMirrorPlan(ctx, payload, nil, "mirror", directory, options.Release, options.Digest, options.CatalogDigest, manifestDigest); err == nil {
		t.Fatal("local QA missing endorsement accepted")
	}
	schedule.Plan = plan
	if err = writeJSON(filepath.Join(output, "mirror-plan.json"), plan); err != nil {
		t.Fatal("local QA mirror plan output unavailable")
	}
	if err = os.WriteFile(filepath.Join(output, "mirror-signature.json"), signatureText, 0600); err != nil {
		t.Fatal("local QA mirror signature output unavailable")
	}
	if err = writeJSON(filepath.Join(output, "mirror-schedule.json"), schedule); err != nil {
		t.Fatal("local QA mirror copy DATA unavailable")
	}
	return verified.Identity()
}
