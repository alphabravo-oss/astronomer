package main

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

func verifyReport(manifest caseManifest, evidence report) error {
	if evidence.SchemaVersion != reportSchema || evidence.Mode != "qualification" || evidence.RunID == "" || evidence.GeneratedAt.IsZero() {
		return errors.New("report is not comprehensive v1 qualification evidence")
	}
	if !validCommitHash(evidence.Candidate.Commit) || evidence.Candidate.Dirty || !validSHA256(evidence.Candidate.StagedDiffSHA) || !validSHA256(evidence.Candidate.UnstagedDiffSHA) || !validSHA256(evidence.Candidate.UntrackedSHA) {
		return errors.New("qualification candidate must be an immutable clean commit with source digests")
	}
	if evidence.Cleanup.State != "PASS" {
		return errors.New("qualification cleanup did not pass")
	}
	want := map[string]bool{}
	for _, definition := range manifest.Cases {
		if definition.Required {
			want[definition.ID] = true
		}
	}
	seen := map[string]bool{}
	for _, result := range evidence.Cases {
		if seen[result.ID] {
			return fmt.Errorf("duplicate case result %s", result.ID)
		}
		seen[result.ID] = true
		if !want[result.ID] {
			return fmt.Errorf("unknown case result %s", result.ID)
		}
		switch result.State {
		case "PASS":
		case "NOT_SUPPORTED":
			if strings.TrimSpace(result.Contract) == "" {
				return fmt.Errorf("case %s lacks an explicit not-supported contract", result.ID)
			}
		default:
			return fmt.Errorf("case %s is %s, not PASS", result.ID, result.State)
		}
	}
	for id := range want {
		if !seen[id] {
			return fmt.Errorf("required case %s is missing", id)
		}
	}
	wantRegistries := map[string]registryContract{}
	for _, contract := range manifest.RuntimeRegistries {
		wantRegistries[contract.ID] = contract
	}
	featureValues := map[string]bool{}
	for _, result := range evidence.Inventory {
		if result.ID == "feature-flags" {
			featureValues = result.ObservedValues
			break
		}
	}
	seenRegistries := map[string]bool{}
	for _, result := range evidence.Inventory {
		contract, exists := wantRegistries[result.ID]
		if !exists || seenRegistries[result.ID] {
			return fmt.Errorf("unknown or duplicate registry result %s", result.ID)
		}
		seenRegistries[result.ID] = true
		if err := verifyRegistryResult(contract, result, featureValues); err != nil {
			return fmt.Errorf("registry %s: %w", result.ID, err)
		}
		if result.Status != "PASS" {
			return fmt.Errorf("registry %s did not pass reconciliation", result.ID)
		}
	}
	if len(evidence.Inventory) != len(manifest.RuntimeRegistries) {
		return errors.New("runtime registry result count does not match the manifest")
	}
	expectedSummary := summarize(evidence)
	if !equalSummary(expectedSummary, evidence.Summary) {
		return errors.New("report summary does not match its case and inventory results")
	}
	return nil
}

func verifyRegistryResult(contract registryContract, result registryResult, featureValues map[string]bool) error {
	expected := sortedUnique(contract.ExpectedIdentities)
	if result.Path != contract.Path || !slicesEqual(result.ExpectedIdentities, expected) {
		return errors.New("reported contract differs from the frozen manifest")
	}
	if result.Pages < 1 || len(result.HTTPStatuses) != result.Pages {
		return errors.New("HTTP page evidence is incomplete")
	}
	disabledRoute := false
	for _, status := range result.HTTPStatuses {
		if status == http.StatusOK {
			continue
		}
		enabled, known := featureValues[contract.AbsentWhenFeatureDisabled]
		if status == http.StatusNotFound && result.Pages == 1 && contract.AbsentWhenFeatureDisabled != "" && known && !enabled {
			disabledRoute = true
			continue
		}
		return fmt.Errorf("unexpected HTTP status %d", status)
	}
	if disabledRoute && len(result.HTTPStatuses) != 1 {
		return errors.New("disabled route evidence included additional pages")
	}
	observed := sortedUnique(result.ObservedIdentities)
	if len(observed) != len(result.ObservedIdentities) || !slicesEqual(observed, result.ObservedIdentities) {
		return errors.New("observed identities are not unique and canonical")
	}
	if contract.ObjectKeys {
		valueKeys := make([]string, 0, len(result.ObservedValues))
		for key := range result.ObservedValues {
			valueKeys = append(valueKeys, key)
		}
		sort.Strings(valueKeys)
		if !slicesEqual(valueKeys, observed) {
			return errors.New("observed object values do not match observed identities")
		}
	} else if len(result.ObservedValues) != 0 {
		return errors.New("non-object registry reported object values")
	}
	missing := difference(expected, observed)
	additional := []string{}
	if !contract.AllowAdditional {
		additional = difference(observed, expected)
	}
	if !slicesEqual(result.MissingIdentities, missing) || !slicesEqual(result.AdditionalIdentities, additional) {
		return errors.New("reported reconciliation does not match observed identities")
	}
	if len(missing) != 0 || len(additional) != 0 {
		return errors.New("runtime offerings differ from the frozen manifest")
	}
	return nil
}

func validCommitHash(value string) bool {
	return (len(value) == 40 || len(value) == 64) && isLowerHex(value)
}

func validSHA256(value string) bool {
	return len(value) == 64 && isLowerHex(value)
}

func isLowerHex(value string) bool {
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func slicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func summarize(evidence report) resultSummary {
	summary := resultSummary{Total: len(evidence.Cases), ByState: map[string]int{}}
	for _, result := range evidence.Cases {
		summary.ByState[result.State]++
	}
	for _, result := range evidence.Inventory {
		if result.Status == "PASS" {
			summary.InventoryOK++
		} else {
			summary.InventoryBad++
		}
	}
	return summary
}

func equalSummary(left, right resultSummary) bool {
	if left.Total != right.Total || left.InventoryOK != right.InventoryOK || left.InventoryBad != right.InventoryBad || len(left.ByState) != len(right.ByState) {
		return false
	}
	for state, count := range left.ByState {
		if right.ByState[state] != count {
			return false
		}
	}
	return true
}
