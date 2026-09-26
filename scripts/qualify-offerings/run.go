package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

const defaultCaseTimeout = 20 * time.Minute

// caseExecutor is intentionally compiled into the runner. A qualification job
// cannot turn the API token into a general-purpose HTTP client by supplying a
// method, URL, or request body in a fixture file.
type caseExecutor interface {
	Run(context.Context, executionContext, caseDefinition, caseResult, checkpointFunc) caseResult
}

type executionContext struct {
	Base            *url.URL
	Token           string
	RestrictedToken string
	Config          qualificationConfig
	RunID           string
}

type checkpointFunc func(caseResult) error

type blockedExecutor struct{ reason string }

func (executor blockedExecutor) Run(_ context.Context, _ executionContext, definition caseDefinition, result caseResult, checkpoint checkpointFunc) caseResult {
	now := time.Now().UTC()
	result.State = "BLOCKED"
	result.Reason = executor.reason
	result.CompletedAt = &now
	result.Dimensions = append(result.Dimensions, dimensionResult{
		Name: "fixture", State: "BLOCKED", Reason: executor.reason, ObservedAt: now,
	})
	_ = checkpoint(result)
	return result
}

func runQualification(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	configPath := flags.String("config", "", "qualification config JSON")
	casesPath := flags.String("cases", "scripts/testdata/offering-qualification/cases.json", "case manifest JSON")
	inventoryPath := flags.String("inventory", "", "passing inventory report from the same candidate")
	outputPath := flags.String("output", "", "checkpointed qualification report JSON")
	resume := flags.Bool("resume", false, "resume the report at --output")
	caseIDs := flags.String("case", "", "optional comma-separated case IDs")
	timeout := flags.Duration("case-timeout", defaultCaseTimeout, "per-case execution timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *configPath == "" || *inventoryPath == "" || *outputPath == "" {
		return errors.New("--config, --inventory, and --output are required; positional arguments are not accepted")
	}
	if *timeout <= 0 || *timeout > 2*time.Hour {
		return errors.New("--case-timeout must be greater than zero and no more than 2h")
	}
	config, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	manifest, err := loadCases(*casesPath)
	if err != nil {
		return err
	}
	identity, err := repositoryIdentity()
	if err != nil {
		return err
	}
	if identity.Dirty {
		return errors.New("qualification run requires a clean repository candidate")
	}
	if config.ExpectedCommit != identity.Commit {
		return fmt.Errorf("frozen candidate mismatch: config=%s repository=%s", config.ExpectedCommit, identity.Commit)
	}
	base, err := validateBaseURL(config.BaseURL, config.AllowLoopbackHTTP)
	if err != nil {
		return err
	}
	token, err := readToken(config.TokenFile)
	if err != nil {
		return err
	}
	restrictedToken := ""
	if config.RestrictedTokenFile != "" {
		restrictedToken, err = readToken(config.RestrictedTokenFile)
		if err != nil {
			return fmt.Errorf("read restricted qualification token: %w", err)
		}
	}
	inventory, err := loadPassingInventory(*inventoryPath, manifest, identity, base)
	if err != nil {
		return err
	}
	selected, err := selectCases(manifest.Cases, *caseIDs)
	if err != nil {
		return err
	}

	result, err := initializeQualificationReport(*outputPath, *resume, identity, base, config, inventory, manifest.Cases)
	if err != nil {
		return err
	}
	index := make(map[string]int, len(result.Cases))
	for position := range result.Cases {
		index[result.Cases[position].ID] = position
	}
	checkpoint := func(updated caseResult) error {
		position, ok := index[updated.ID]
		if !ok {
			return fmt.Errorf("checkpoint referenced unknown case %s", updated.ID)
		}
		result.Cases[position] = updated
		result.GeneratedAt = time.Now().UTC()
		result.Summary = summarize(result)
		return writeJSONAtomic(*outputPath, result)
	}

	execution := executionContext{Base: base, Token: token, RestrictedToken: restrictedToken, Config: config, RunID: result.RunID}
	for _, definition := range selected {
		current := result.Cases[index[definition.ID]]
		if shouldSkipResumedCase(definition, current) {
			continue
		}
		now := time.Now().UTC()
		current.State = "RUNNING"
		current.Reason = "case executor started"
		current.Attempts++
		current.StartedAt = &now
		current.CompletedAt = nil
		if err := checkpoint(current); err != nil {
			return err
		}
		caseCtx, cancel := context.WithTimeout(ctx, *timeout)
		updated := executorFor(definition).Run(caseCtx, execution, definition, current, checkpoint)
		cancel()
		if updated.State == "RUNNING" || updated.State == "NOT_RUN" {
			updated.State = "FAIL"
			updated.Reason = "executor returned without a terminal result"
		}
		if caseCtx.Err() == context.DeadlineExceeded {
			updated.State = "FAIL"
			updated.Reason = "case exceeded its configured timeout"
		}
		completed := time.Now().UTC()
		updated.CompletedAt = &completed
		if err := checkpoint(updated); err != nil {
			return err
		}
	}
	result.Cleanup = qualificationCleanup(result)
	result.GeneratedAt = time.Now().UTC()
	result.Summary = summarize(result)
	if err := writeJSONAtomic(*outputPath, result); err != nil {
		return err
	}
	fmt.Printf("qualify-offerings: run checkpointed; cases=%d states=%v evidence=%s\n", len(selected), result.Summary.ByState, *outputPath)
	return nil
}

func qualificationCleanup(result report) cleanupResult {
	for _, item := range result.Cases {
		for _, dimension := range item.Dimensions {
			if dimension.Name == "uninstall_cleanup" && dimension.State == "FAIL" {
				return cleanupResult{State: "FAIL", Reason: "one or more executors could not prove removal of run-owned resources"}
			}
		}
	}
	return cleanupResult{State: "PASS", Reason: "no executor reported a remaining run-owned resource after reverse-order cleanup"}
}

func shouldSkipResumedCase(definition caseDefinition, result caseResult) bool {
	if result.State == "PASS" || result.State == "NOT_SUPPORTED" {
		return true
	}
	if result.State == "BLOCKED" {
		_, external := externalFixtureReason(definition.ID)
		return external
	}
	return false
}

func loadPassingInventory(path string, manifest caseManifest, identity candidate, base *url.URL) ([]registryResult, error) {
	var evidence report
	if err := readStrictJSON(path, &evidence); err != nil {
		return nil, fmt.Errorf("read inventory: %w", err)
	}
	if evidence.SchemaVersion != reportSchema || evidence.Mode != "inventory" || evidence.Candidate != identity {
		return nil, errors.New("inventory was not produced by this exact clean candidate")
	}
	if evidence.Target.BaseOrigin != base.Scheme+"://"+base.Host {
		return nil, errors.New("inventory target origin differs from the run target")
	}
	if len(evidence.Inventory) != len(manifest.RuntimeRegistries) {
		return nil, errors.New("inventory registry count differs from the case manifest")
	}
	features := map[string]bool{}
	for _, item := range evidence.Inventory {
		if item.ID == "feature-flags" {
			features = item.ObservedValues
		}
	}
	contracts := map[string]registryContract{}
	for _, contract := range manifest.RuntimeRegistries {
		contracts[contract.ID] = contract
	}
	for _, item := range evidence.Inventory {
		contract, ok := contracts[item.ID]
		if !ok || item.Status != "PASS" {
			return nil, fmt.Errorf("inventory registry %s did not pass", item.ID)
		}
		if err := verifyRegistryResult(contract, item, features); err != nil {
			return nil, fmt.Errorf("inventory registry %s: %w", item.ID, err)
		}
	}
	return evidence.Inventory, nil
}

func initializeQualificationReport(path string, resume bool, identity candidate, base *url.URL, config qualificationConfig, inventory []registryResult, definitions []caseDefinition) (report, error) {
	if resume {
		var existing report
		if err := readStrictJSON(path, &existing); err != nil {
			return report{}, fmt.Errorf("resume report: %w", err)
		}
		if existing.SchemaVersion != reportSchema || existing.Mode != "qualification" || existing.Candidate != identity || existing.Target.BaseOrigin != base.Scheme+"://"+base.Host {
			return report{}, errors.New("resume report does not belong to this candidate and target")
		}
		if len(existing.Cases) != len(definitions) {
			return report{}, errors.New("resume report case count differs from the manifest")
		}
		return existing, nil
	}
	value := report{
		SchemaVersion: reportSchema,
		RunID:         fmt.Sprintf("qualification-%d", time.Now().UTC().UnixNano()),
		GeneratedAt:   time.Now().UTC(), Mode: "qualification", Candidate: identity,
		Target:    target{BaseOrigin: base.Scheme + "://" + base.Host, TargetIDs: cloneMap(config.TargetIDs)},
		Inventory: inventory,
		Cleanup:   cleanupResult{State: "BLOCKED", Reason: "qualification execution has not completed cleanup"},
	}
	for _, definition := range definitions {
		value.Cases = append(value.Cases, caseResult{ID: definition.ID, State: "NOT_RUN", Reason: "awaiting its compiled case executor"})
	}
	value.Summary = summarize(value)
	if err := writeJSONAtomic(path, value); err != nil {
		return report{}, err
	}
	return value, nil
}

func selectCases(definitions []caseDefinition, raw string) ([]caseDefinition, error) {
	if strings.TrimSpace(raw) == "" {
		return definitions, nil
	}
	wanted := map[string]bool{}
	for _, id := range strings.Split(raw, ",") {
		id = strings.TrimSpace(id)
		if id == "" || wanted[id] {
			return nil, fmt.Errorf("invalid or duplicate --case value %q", id)
		}
		wanted[id] = true
	}
	selected := make([]caseDefinition, 0, len(wanted))
	for _, definition := range definitions {
		if wanted[definition.ID] {
			selected = append(selected, definition)
			delete(wanted, definition.ID)
		}
	}
	if len(wanted) != 0 {
		unknown := make([]string, 0, len(wanted))
		for id := range wanted {
			unknown = append(unknown, id)
		}
		sort.Strings(unknown)
		return nil, fmt.Errorf("unknown case IDs: %s", strings.Join(unknown, ", "))
	}
	return selected, nil
}

func executorFor(definition caseDefinition) caseExecutor {
	if reason, external := externalFixtureReason(definition.ID); external {
		return blockedExecutor{reason: reason}
	}
	if slug, ok := toolCases[definition.ID]; ok {
		return toolLifecycleExecutor{slug: slug}
	}
	return blockedExecutor{reason: "no compiled functional executor exists for this locally testable case yet"}
}

func externalFixtureReason(id string) (string, bool) {
	prefixes := []string{"CLOUD-", "SECRET-", "AI-"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(id, prefix) {
			return "requires the external provider or companion fixture named by the case contract", true
		}
	}
	exact := map[string]string{
		"SOURCE-01": "requires an independent authenticated registry receiver", "SOURCE-03": "requires an independent OCI registry receiver",
		"SOURCE-04": "requires an independent Git receiver", "SOURCE-07": "requires a disconnected mirror fixture",
		"DR-05": "requires AWS S3 credentials and storage", "DR-06": "requires an independent S3-compatible receiver",
		"DR-07": "requires GCS credentials and storage", "DR-08": "requires Azure Blob credentials and storage", "DR-09": "requires a CSI snapshot-capable fixture",
		"CIS-02": "requires an RKE cluster", "CIS-03": "requires an RKE2 cluster", "CIS-05": "requires an EKS cluster", "CIS-06": "requires an AKS cluster", "CIS-07": "requires a GKE cluster",
		"MESH-02": "requires a declared Linkerd fixture", "MESH-03": "requires a declared Kuma fixture", "MESH-04": "requires a declared Cilium fixture",
		"LOG-01": "requires an independent Elasticsearch receiver", "LOG-02": "requires an independent OpenSearch receiver", "LOG-04": "requires a Splunk receiver",
		"LOG-05": "requires AWS CloudWatch credentials", "LOG-06": "requires a Datadog receiver", "LOG-07": "requires object storage",
		"SEND-01": "requires a Slack receiver", "SEND-02": "requires an email receiver", "SEND-03": "requires a PagerDuty receiver", "SEND-05": "requires a Microsoft Teams receiver",
		"ID-02": "requires an Okta identity provider", "ID-03": "requires a Microsoft Entra identity provider", "ID-04": "requires a GitHub identity provider",
		"ID-05": "requires a GitLab identity provider", "ID-06": "requires a Bitbucket identity provider", "ID-07": "requires a Google Workspace identity provider",
		"ID-08": "requires a SAML identity provider", "ID-09": "requires an LDAP directory", "ID-11": "requires a GitHub identity provider",
		"ID-12": "requires a Google Workspace identity provider", "ID-13": "requires a Microsoft Entra identity provider", "ID-14": "requires a GitLab identity provider", "ID-15": "requires an Okta identity provider",
	}
	reason, ok := exact[id]
	return reason, ok
}
