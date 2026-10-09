package main

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	ociregistry "oras.land/oras-go/v2/registry"
)

// mirrorSchedule is derived only after canonical offline authentication. It is
// preparation metadata, not authority: execution recomputes it from the signed
// graph and requires the separately signed operator MirrorPlan.
type mirrorSchedule struct {
	SchemaVersion          int                     `json:"schema_version"`
	DistributionRepository string                  `json:"distribution_repository"`
	Plan                   distribution.MirrorPlan `json:"plan"`
	Copies                 []mirrorCopy            `json:"copies"`
	Assets                 []mirrorAsset           `json:"assets"`
}
type mirrorCopy struct {
	Kind            string `json:"kind"`
	Source          string `json:"source"`
	Target          string `json:"target"`
	SignatureDigest string `json:"signature_digest,omitempty"`
}
type mirrorAsset struct {
	ID           string   `json:"id"`
	MemberPath   string   `json:"member_path"`
	MemberDigest string   `json:"member_digest"`
	Images       []string `json:"images"`
}

func planOfflineMirror(ctx context.Context, payload []byte, options distribution.OfflineOptions, targetBase, releaseManifestDigest string) (mirrorSchedule, offlineTransferIndex, map[string][]byte, error) {
	var empty mirrorSchedule
	index, blobs, err := readOfflineTransfer(ctx, payload, options)
	if err != nil {
		return empty, offlineTransferIndex{}, nil, err
	}
	target, err := ociregistry.ParseReference(targetBase)
	if err != nil || target.Reference != "" || target.Registry != strings.ToLower(target.Registry) || !strings.ContainsAny(target.Registry, ".:") || targetBase != target.Registry+"/"+target.Repository || len(targetBase) > 1024 {
		return empty, offlineTransferIndex{}, nil, errors.New("canonical target registry repository required")
	}
	if _, _, err := contentSubject("registry.invalid/release@" + releaseManifestDigest); err != nil {
		return empty, offlineTransferIndex{}, nil, errors.New("pinned release manifest digest required")
	}
	var root ocispec.Manifest
	if closedJSON(blobs[options.Digest], &root) != nil || len(root.Layers) != 1 {
		return empty, offlineTransferIndex{}, nil, errors.New("verified parent descriptor unavailable")
	}
	members, bom, err := distribution.ReadClosedArchive(blobs[root.Layers[0].Digest.String()], options.Release, options.CatalogDigest)
	if err != nil {
		return empty, offlineTransferIndex{}, nil, err
	}
	plan := distribution.MirrorPlan{SchemaVersion: 1, Release: options.Release, DistributionDigest: options.Digest, CatalogDigest: options.CatalogDigest, ReleaseManifestDigest: releaseManifestDigest, Rewrites: []distribution.Rewrite{}}
	schedule := mirrorSchedule{SchemaVersion: 1, DistributionRepository: mirrorRepository(targetBase, options.Repository), Plan: plan, Copies: []mirrorCopy{}, Assets: []mirrorAsset{}}
	rewrites := map[string]distribution.Rewrite{}
	for _, subject := range index.Subjects {
		destination := mirrorRepository(targetBase, subject.Repository)
		schedule.Copies = append(schedule.Copies, mirrorCopy{Kind: "signed_artifact", Source: subject.Repository + "@" + subject.Digest, Target: destination + "@" + subject.Digest, SignatureDigest: subject.SignatureDigest})
	}
	// Source.URL is the chart repository base; downstream appends chart name.
	// The same namespace function preserves that exact child relationship.
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
			return empty, offlineTransferIndex{}, nil, errors.New("verified prerequisite member unavailable")
		}
		projected := mirrorAsset{ID: asset.ID, MemberPath: asset.MemberPath, MemberDigest: identity, Images: []string{}}
		for _, image := range asset.Images {
			repository, subject, err := contentSubject(image)
			if err != nil {
				return empty, offlineTransferIndex{}, nil, err
			}
			projected.Images = append(projected.Images, mirrorRepository(targetBase, repository)+"@"+subject)
		}
		sort.Strings(projected.Images)
		schedule.Assets = append(schedule.Assets, projected)
		if asset.PublishedSubject != "" {
			repository, subject, err := contentSubject(asset.PublishedSubject)
			if err != nil {
				return empty, offlineTransferIndex{}, nil, err
			}
			rewrites[asset.PublishedSubject] = distribution.Rewrite{Kind: "artifact", Source: asset.PublishedSubject, Target: mirrorRepository(targetBase, repository) + "@" + subject}
		}
	}
	var inventory distribution.Inventory
	found := false
	for _, profile := range bom.Profiles {
		if profile.Name == options.Profile {
			if closedJSON(members[profile.InventoryPath], &inventory) != nil || inventory.SchemaVersion != 1 {
				return empty, offlineTransferIndex{}, nil, errors.New("verified image inventory unavailable")
			}
			found = true
		}
	}
	if !found {
		return empty, offlineTransferIndex{}, nil, errors.New("verified mirror profile unavailable")
	}
	for _, image := range inventory.Images {
		repository, subject, err := contentSubject(image.Reference)
		if err != nil {
			return empty, offlineTransferIndex{}, nil, err
		}
		destination := mirrorRepository(targetBase, repository) + "@" + subject
		rewrites[image.Reference] = distribution.Rewrite{Kind: "image", Source: image.Reference, Target: destination}
		schedule.Copies = append(schedule.Copies, mirrorCopy{Kind: "image", Source: image.Reference, Target: destination})
	}
	for _, copy := range schedule.Copies {
		if _, _, err := contentSubject(copy.Target); err != nil {
			return empty, offlineTransferIndex{}, nil, err
		}
	}
	for _, rewrite := range rewrites {
		if len(rewrite.Source) > 2048 || len(rewrite.Target) > 2048 {
			return empty, offlineTransferIndex{}, nil, errors.New("mirror rewrite exceeds identity bounds")
		}
		schedule.Plan.Rewrites = append(schedule.Plan.Rewrites, rewrite)
	}
	if len(schedule.Plan.Rewrites) > 4096 || len(schedule.Copies) > 4096 {
		return empty, offlineTransferIndex{}, nil, errors.New("mirror inventory exceeds plan bounds")
	}
	sort.Slice(schedule.Plan.Rewrites, func(i, j int) bool { return schedule.Plan.Rewrites[i].Source < schedule.Plan.Rewrites[j].Source })
	sort.Slice(schedule.Copies, func(i, j int) bool { return schedule.Copies[i].Source < schedule.Copies[j].Source })
	sort.Slice(schedule.Assets, func(i, j int) bool { return schedule.Assets[i].ID < schedule.Assets[j].ID })
	return schedule, index, blobs, nil
}

func mirrorRepository(base, source string) string {
	host, repository, _ := strings.Cut(source, "/")
	// Different upstream hosts can use the same path; preserve both identities
	// without credential-bearing or mutable location substitutions.
	return base + "/r-" + strings.TrimPrefix(sha([]byte(host)), "sha256:") + "/" + repository
}
func contentSubject(reference string) (string, string, error) {
	parsed, err := ociregistry.ParseReference(reference)
	if err != nil || parsed.Reference == "" || len(parsed.Reference) != 71 || !strings.HasPrefix(parsed.Reference, "sha256:") || len(reference) > 2048 || reference != parsed.Registry+"/"+parsed.Repository+"@"+parsed.Reference {
		return "", "", errors.New("canonical immutable mirror subject required")
	}
	for _, c := range parsed.Reference[7:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return "", "", errors.New("mirror subject digest invalid")
		}
	}
	return parsed.Registry + "/" + parsed.Repository, parsed.Reference, nil
}
