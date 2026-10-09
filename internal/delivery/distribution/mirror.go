package distribution

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

type MirrorPlan struct {
	SchemaVersion         int       `json:"schema_version"`
	Release               string    `json:"release"`
	DistributionDigest    string    `json:"distribution_digest"`
	CatalogDigest         string    `json:"catalog_digest"`
	ReleaseManifestDigest string    `json:"release_manifest_digest"`
	Rewrites              []Rewrite `json:"rewrites"`
	identity              string
}
type Rewrite struct {
	Kind   string `json:"kind"`
	Source string `json:"source"`
	Target string `json:"target"`
}

// VerifyMirrorPlan authenticates operator endorsement before interpreting any
// rewrite. The endorsement binds the original signed release unit and never
// changes vendor trust policy or content digests.
func VerifyMirrorPlan(ctx context.Context, payload, signature []byte, keyRef, trustDirectory, release, distributionDigest, catalogDigest, manifestDigest string) (*MirrorPlan, error) {
	if len(payload) == 0 || len(payload) > 1<<20 || len(signature) == 0 || len(signature) > 1<<20 || !digestPattern.MatchString(manifestDigest) {
		return nil, errors.New("mirror endorsement inputs are invalid")
	}
	verifier, err := resolver.NewExecVerifier(trustDirectory)
	if err != nil {
		return nil, err
	}
	policy := model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: keyRef}
	if err := policy.Validate(); err != nil {
		return nil, errors.New("mirror endorsement policy is invalid")
	}
	_, err = verifier.Verify(ctx, resolver.VerificationInput{Source: model.Source{Type: model.SourceHelmHTTP, Trust: policy}, Artifact: payload, Signature: signature})
	if err != nil {
		return nil, errors.New("mirror endorsement signature is invalid")
	}
	var plan MirrorPlan
	if decodeClosed(payload, &plan) != nil || plan.SchemaVersion != 1 || plan.Release != release || plan.DistributionDigest != distributionDigest || plan.CatalogDigest != catalogDigest || plan.ReleaseManifestDigest != manifestDigest || len(plan.Rewrites) == 0 || len(plan.Rewrites) > 4096 {
		return nil, errors.New("mirror endorsement is not bound to signed release")
	}
	seen := map[string]bool{}
	for _, rewrite := range plan.Rewrites {
		if seen[rewrite.Source] || rewrite.Source == rewrite.Target || len(rewrite.Source) > 2048 || len(rewrite.Target) > 2048 {
			return nil, errors.New("mirror rewrite identity is invalid")
		}
		seen[rewrite.Source] = true
		switch rewrite.Kind {
		case "chart_repository":
			if _, err := builtinbundles.NormalizeOCISourceURL(rewrite.Source); err != nil {
				return nil, errors.New("mirror chart source is invalid")
			}
			if _, err := builtinbundles.NormalizeOCISourceURL(rewrite.Target); err != nil {
				return nil, errors.New("mirror chart target is invalid")
			}
		case "image":
			sourceRepo, sourceDigest, ok := strings.Cut(rewrite.Source, "@")
			targetRepo, targetDigest, targetOK := strings.Cut(rewrite.Target, "@")
			if !ok || !targetOK || !digestPattern.MatchString(sourceDigest) || sourceDigest != targetDigest || sourceRepo == "" || targetRepo == "" {
				return nil, errors.New("mirror image changed content identity")
			}
			for _, repository := range []string{sourceRepo, targetRepo} {
				parsed, err := url.Parse("https://" + repository)
				if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
					return nil, errors.New("mirror image repository is invalid")
				}
			}
		default:
			return nil, errors.New("mirror rewrite kind is unsupported")
		}
	}
	plan.identity = contentDigest(payload)
	return &plan, nil
}

func projectMirrors(ctx context.Context, catalog builtinbundles.Catalog, plan *MirrorPlan, network httpclient.DestinationPolicy) (builtinbundles.Catalog, error) {
	if plan == nil {
		if network.Restricted {
			return builtinbundles.Catalog{}, errors.New("disconnected distribution has no authenticated mirror endorsement")
		}
		return catalog, nil
	}
	charts := map[string]string{}
	images := map[string]string{}
	for _, rewrite := range plan.Rewrites {
		if rewrite.Kind == "chart_repository" {
			charts[rewrite.Source] = rewrite.Target
			if err := network.ValidateURL(ctx, "https://"+strings.TrimPrefix(rewrite.Target, "oci://")); err != nil {
				return builtinbundles.Catalog{}, errors.New("mirror chart destination is outside operator policy")
			}
		}
		if rewrite.Kind == "image" {
			images[rewrite.Source] = rewrite.Target
			repository, _, _ := strings.Cut(rewrite.Target, "@")
			if err := network.ValidateURL(ctx, "https://"+repository); err != nil {
				return builtinbundles.Catalog{}, errors.New("mirror image destination is outside operator policy")
			}
		}
	}
	for index := range catalog.Components {
		component := &catalog.Components[index]
		if component.Values == nil {
			return builtinbundles.Catalog{}, blocked("values", "distribution values profile must be a closed object")
		}
		target, found := charts[component.Source.URL]
		if !found {
			return builtinbundles.Catalog{}, errors.New("mirror endorsement omits chart repository")
		}
		component.Source.URL = target
		for i, image := range component.Images {
			target, found := images[image]
			if !found {
				return builtinbundles.Catalog{}, errors.New("mirror endorsement omits workload image")
			}
			component.Images[i] = target
		}
		valueBytes, _ := json.Marshal(component.Values)
		var values any
		_ = json.Unmarshal(valueBytes, &values)
		rewritten, err := rewriteValues(values, images)
		if err != nil {
			return builtinbundles.Catalog{}, err
		}
		projected, ok := rewritten.(map[string]any)
		if !ok {
			return builtinbundles.Catalog{}, blocked("values", "distribution values profile must be a closed object")
		}
		component.Values = projected
	}
	catalog.DistributionIdentity += ";mirror:" + plan.identity
	if err := catalog.Validate(); err != nil {
		return builtinbundles.Catalog{}, err
	}
	return catalog, nil
}

func rewriteValues(value any, images map[string]string) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		// Helm image maps commonly split repository and digest/tag. Reconstruct
		// the exact signed subject, never perform broad substring replacements.
		repository, repoOK := typed["repository"].(string)
		digest, digestOK := typed["digest"].(string)
		if !digestOK {
			digest, digestOK = typed["sha"].(string)
		}
		if !digestOK {
			if tag, ok := typed["tag"].(string); ok {
				_, candidate, found := strings.Cut(tag, "@")
				if found {
					digest = candidate
					digestOK = true
				}
			}
		}
		registry, registryOK := typed["registry"].(string)
		if digestOK && digestPattern.MatchString(digest) && !repoOK {
			return nil, errors.New("values image map lacks its qualified repository")
		}
		if repoOK && digestOK && digestPattern.MatchString(digest) {
			source := repository + "@" + digest
			if registryOK && registry != "" {
				source = registry + "/" + source
			}
			target, found := images[source]
			if !found {
				return nil, errors.New("values image map is outside authenticated inventory")
			}
			targetRepo, _, _ := strings.Cut(target, "@")
			if registryOK {
				host, rest, ok := strings.Cut(targetRepo, "/")
				if !ok {
					return nil, errors.New("values image target is invalid")
				}
				typed["registry"] = host
				typed["repository"] = rest
			} else {
				typed["repository"] = targetRepo
			}
		}
		for key, child := range typed {
			rewritten, err := rewriteValues(child, images)
			if err != nil {
				return nil, err
			}
			typed[key] = rewritten
		}
		return typed, nil
	case []any:
		for index, child := range typed {
			rewritten, err := rewriteValues(child, images)
			if err != nil {
				return nil, err
			}
			typed[index] = rewritten
		}
		return typed, nil
	case string:
		if target, found := images[typed]; found {
			return target, nil
		}
		if strings.Contains(typed, "/") && strings.Contains(typed, "@sha256:") {
			return nil, errors.New("values image reference is outside authenticated inventory")
		}
		return typed, nil
	default:
		return value, nil
	}
}
