package main

import "time"

type caseManifest struct {
	SchemaVersion     string             `json:"schema_version"`
	SourceDocument    string             `json:"source_document"`
	Cases             []caseDefinition   `json:"cases"`
	RuntimeRegistries []registryContract `json:"runtime_registries"`
}

type caseDefinition struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Title    string `json:"title"`
	Section  string `json:"section"`
	Required bool   `json:"required"`
}

type registryContract struct {
	ID                        string   `json:"id"`
	Path                      string   `json:"path"`
	ItemsPath                 string   `json:"items_path"`
	IdentityField             string   `json:"identity_field,omitempty"`
	ExpectedIdentities        []string `json:"expected_identities"`
	AllowAdditional           bool     `json:"allow_additional,omitempty"`
	ObjectKeys                bool     `json:"object_keys,omitempty"`
	AbsentWhenFeatureDisabled string   `json:"absent_when_feature_disabled,omitempty"`
}

type qualificationConfig struct {
	SchemaVersion     string            `json:"schema_version"`
	BaseURL           string            `json:"base_url"`
	TokenFile         string            `json:"token_file"`
	ExpectedCommit    string            `json:"expected_commit"`
	AllowLoopbackHTTP bool              `json:"allow_loopback_http,omitempty"`
	TargetIDs         map[string]string `json:"target_ids"`
}

type report struct {
	SchemaVersion string           `json:"schema_version"`
	RunID         string           `json:"run_id"`
	GeneratedAt   time.Time        `json:"generated_at"`
	Mode          string           `json:"mode"`
	Candidate     candidate        `json:"candidate"`
	Target        target           `json:"target"`
	Inventory     []registryResult `json:"inventory"`
	Cases         []caseResult     `json:"cases"`
	Summary       resultSummary    `json:"summary"`
	Cleanup       cleanupResult    `json:"cleanup"`
}

type candidate struct {
	Commit          string `json:"commit"`
	StagedDiffSHA   string `json:"staged_diff_sha256"`
	UnstagedDiffSHA string `json:"unstaged_diff_sha256"`
	UntrackedSHA    string `json:"untracked_sha256"`
	Dirty           bool   `json:"dirty"`
}

type target struct {
	BaseOrigin string            `json:"base_origin"`
	TargetIDs  map[string]string `json:"target_ids"`
}

type registryResult struct {
	ID                   string          `json:"id"`
	Path                 string          `json:"path"`
	Pages                int             `json:"pages"`
	HTTPStatuses         []int           `json:"http_statuses"`
	ObservedIdentities   []string        `json:"observed_identities"`
	ExpectedIdentities   []string        `json:"expected_identities"`
	MissingIdentities    []string        `json:"missing_identities"`
	AdditionalIdentities []string        `json:"additional_identities"`
	ObservedValues       map[string]bool `json:"observed_values,omitempty"`
	Status               string          `json:"status"`
	Reason               string          `json:"reason"`
}

type caseResult struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Reason   string `json:"reason"`
	Contract string `json:"not_supported_contract,omitempty"`
}

type resultSummary struct {
	Total        int            `json:"total"`
	ByState      map[string]int `json:"by_state"`
	InventoryOK  int            `json:"inventory_pass"`
	InventoryBad int            `json:"inventory_fail"`
}

type cleanupResult struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
}
