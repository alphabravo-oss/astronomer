package main

import (
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
)

type estateImageInventory struct {
	Schema string            `json:"schema_version"`
	Images map[string]string `json:"images"`
}
type estateImageReview struct {
	Schema               string   `json:"schema_version"`
	Baseline             string   `json:"baseline_inventory"`
	Candidate            string   `json:"candidate_inventory"`
	Allowed              []string `json:"allowed_changed_components"`
	BaselineAttribution  string   `json:"baseline_consumer_attribution"`
	CandidateAttribution string   `json:"candidate_consumer_attribution"`
}
type estateComparisonProvenance struct {
	ReviewSHA256            string   `json:"review_sha256,omitempty"`
	BaselineImagesSHA256    string   `json:"baseline_images_sha256"`
	CandidateImagesSHA256   string   `json:"candidate_images_sha256"`
	ImageEvidence           string   `json:"image_evidence"`
	ChangedComponentsSHA256 []string `json:"changed_component_sha256"`
	Attribution             string   `json:"consumer_attribution"`
}

func compareEstateImages(path string, a, b estateReport) (estateComparisonProvenance, error) {
	p := estateComparisonProvenance{BaselineImagesSHA256: a.Environment.ImagesSHA256, CandidateImagesSHA256: b.Environment.ImagesSHA256, ImageEvidence: "unverified_inventory_absent", Attribution: "unknown", ChangedComponentsSHA256: []string{}}
	if path == "" {
		return p, nil
	}
	raw, digest, err := readEstateBounded(path, 64<<10)
	if err != nil {
		return p, err
	}
	var review estateImageReview
	if decodeEstateStrict(raw, &review) != nil {
		return p, errors.New("invalid_image_review")
	}
	if review.Schema != "astronomer-estate-image-review-v1" || review.Baseline == "" || review.Candidate == "" || len(review.Allowed) > 64 {
		return p, errors.New("invalid_image_review")
	}
	attributions := []string{"unknown", "legacy_callbacks_other", "tagged_callbacks_v1"}
	if !slices.Contains(attributions, review.BaselineAttribution) || !slices.Contains(attributions, review.CandidateAttribution) {
		return p, errors.New("invalid_attribution_declaration")
	}
	p.ReviewSHA256 = digest
	if review.BaselineAttribution != "unknown" && review.CandidateAttribution != "unknown" {
		p.Attribution = "declared_mismatch"
		if review.BaselineAttribution == review.CandidateAttribution {
			p.Attribution = "declared_matching"
		}
	}
	read := func(name, expected string) (estateImageInventory, error) {
		var inventory estateImageInventory
		if !filepath.IsAbs(name) {
			name = filepath.Join(filepath.Dir(path), name)
		}
		raw, digest, err := readEstateBounded(name, 64<<10)
		if err != nil {
			return inventory, err
		}
		if digest != expected {
			return inventory, errors.New("image_inventory_hash_mismatch")
		}
		if decodeEstateStrict(raw, &inventory) != nil || inventory.Schema != "astronomer-image-inventory-v1" || len(inventory.Images) == 0 || len(inventory.Images) > 64 {
			return inventory, errors.New("invalid_image_inventory")
		}
		for name, image := range inventory.Images {
			if !estateName.MatchString(name) || !strings.HasPrefix(image, "sha256:") || !estateDigest.MatchString(strings.TrimPrefix(image, "sha256:")) {
				return inventory, errors.New("invalid_image_inventory")
			}
		}
		return inventory, nil
	}
	left, err := read(review.Baseline, a.Environment.ImagesSHA256)
	if err != nil {
		return p, err
	}
	right, err := read(review.Candidate, b.Environment.ImagesSHA256)
	if err != nil {
		return p, err
	}
	allowed := map[string]bool{}
	for _, name := range review.Allowed {
		if !estateName.MatchString(name) || allowed[name] || left.Images[name] == "" || right.Images[name] == "" {
			return p, errors.New("invalid_image_change_allowlist")
		}
		allowed[name] = true
	}
	leftKeys, rightKeys := []string{}, []string{}
	for name := range left.Images {
		leftKeys = append(leftKeys, name)
	}
	for name := range right.Images {
		rightKeys = append(rightKeys, name)
	}
	slices.Sort(leftKeys)
	slices.Sort(rightKeys)
	p.ImageEvidence = "hash_matched_user_reviewed_changes"
	if !reflect.DeepEqual(leftKeys, rightKeys) {
		p.ImageEvidence = "component_set_mismatch"
		return p, nil
	}
	for _, name := range leftKeys {
		if left.Images[name] != right.Images[name] {
			p.ChangedComponentsSHA256 = append(p.ChangedComponentsSHA256, estateComparisonHash(name))
			if !allowed[name] {
				p.ImageEvidence = "unreviewed_image_change"
			}
		}
	}
	return p, nil
}
