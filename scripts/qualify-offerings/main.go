// qualify-offerings records GET-only inventory and estate preflight evidence.
// Lifecycle mutation executors are intentionally not part of this port.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	casesSchema  = "astronomer-offering-cases-v1"
	configSchema = "astronomer-offering-qualification-config-v1"
	reportSchema = "astronomer-offering-qualification-v1"
	maxPages     = 100
	maxBodyBytes = 8 << 20
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "qualify-offerings:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: qualify-offerings inventory|preflight [flags]")
	}
	switch args[0] {
	case "inventory":
		return runInventory(ctx, args[1:])
	case "preflight":
		return runPreflight(ctx, args[1:])
	default:
		return fmt.Errorf("unsupported mode %q (expected inventory or preflight)", args[0])
	}
}

func runInventory(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("inventory", flag.ContinueOnError)
	configPath := flags.String("config", "", "qualification config JSON")
	casesPath := flags.String("cases", "scripts/testdata/offering-qualification/cases.json", "case manifest JSON")
	outputPath := flags.String("output", "", "report JSON path in an existing directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *configPath == "" || *outputPath == "" {
		return errors.New("--config and --output are required; positional arguments are not accepted")
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
	client := inventoryHTTPClient()
	results := make([]registryResult, 0, len(manifest.RuntimeRegistries))
	featureFlags := map[string]bool{}
	failed := false
	for _, contract := range manifest.RuntimeRegistries {
		result := inventoryRegistry(ctx, client, base, token, contract, config.TargetIDs, featureFlags)
		results = append(results, result)
		if contract.ID == "feature-flags" && result.Status == "PASS" {
			featureFlags = cloneBoolMap(result.ObservedValues)
		}
		failed = failed || result.Status != "PASS"
	}
	cases := make([]caseResult, 0, len(manifest.Cases))
	for _, definition := range manifest.Cases {
		cases = append(cases, caseResult{ID: definition.ID, State: "NOT_RUN", Reason: "inventory mode does not execute functional lifecycle cases"})
	}
	result := report{
		SchemaVersion: reportSchema,
		RunID:         fmt.Sprintf("inventory-%d", time.Now().UTC().UnixNano()),
		GeneratedAt:   time.Now().UTC(),
		Mode:          "inventory",
		Candidate:     identity,
		Target:        target{BaseOrigin: base.Scheme + "://" + base.Host, TargetIDs: cloneMap(config.TargetIDs)},
		Inventory:     results,
		Cases:         cases,
		Cleanup:       cleanupResult{State: "NOT_APPLICABLE", Reason: "inventory mode is GET-only"},
	}
	result.Summary = summarize(result)
	if err := writeJSONAtomic(*outputPath, result); err != nil {
		return err
	}
	if failed {
		return fmt.Errorf("runtime inventory does not match the frozen manifest; inspect %s", *outputPath)
	}
	fmt.Printf("qualify-offerings: inventory passed; registries=%d cases=%d evidence=%s\n", len(results), len(cases), *outputPath)
	return nil
}

func loadConfig(path string) (qualificationConfig, error) {
	var config qualificationConfig
	if err := readStrictJSON(path, &config); err != nil {
		return config, fmt.Errorf("read config: %w", err)
	}
	if config.SchemaVersion != configSchema || config.BaseURL == "" || config.TokenFile == "" || !validCommit(config.ExpectedCommit) {
		return config, errors.New("config requires the v1 schema, base_url, token_file, and expected_commit")
	}
	if config.QualificationID != "" && (len(config.QualificationID) > 63 || !dnsLabel.MatchString(config.QualificationID)) {
		return config, errors.New("qualification_id must be a DNS label")
	}
	if config.EvidenceSHA256 != "" {
		decoded, err := hex.DecodeString(config.EvidenceSHA256)
		if err != nil || len(decoded) != 32 || config.EvidenceSHA256 != strings.ToLower(config.EvidenceSHA256) {
			return config, errors.New("estate evidence SHA256 must be 64 lowercase hex characters")
		}
	}
	if config.TargetIDs == nil {
		config.TargetIDs = map[string]string{}
	}
	return config, nil
}

func loadCases(path string) (caseManifest, error) {
	var manifest caseManifest
	if err := readStrictJSON(path, &manifest); err != nil {
		return manifest, fmt.Errorf("read cases: %w", err)
	}
	if manifest.SchemaVersion != casesSchema || manifest.SourceDocument == "" || len(manifest.Cases) == 0 || len(manifest.RuntimeRegistries) == 0 {
		return manifest, errors.New("case manifest is incomplete or uses an unsupported schema")
	}
	seen := map[string]bool{}
	for _, definition := range manifest.Cases {
		if definition.ID == "" || definition.Category == "" || definition.Title == "" || definition.Section == "" || seen[definition.ID] {
			return manifest, fmt.Errorf("case manifest has an invalid or duplicate case %q", definition.ID)
		}
		seen[definition.ID] = true
	}
	for _, contract := range manifest.RuntimeRegistries {
		if contract.ID == "" || !strings.HasPrefix(contract.Path, "/api/v1/") || (!contract.ObjectKeys && contract.IdentityField == "") {
			return manifest, fmt.Errorf("registry contract %q is invalid", contract.ID)
		}
	}
	return manifest, nil
}

func validateBaseURL(raw string, allowLoopback bool) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("base_url must be an absolute origin without credentials, query, or fragment")
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	host := parsed.Hostname()
	isLoopback := host == "127.0.0.1" || host == "localhost" || host == "::1"
	if parsed.Scheme != "https" && (!allowLoopback || parsed.Scheme != "http" || !isLoopback) {
		return nil, errors.New("base_url must use https (explicit loopback HTTP is test-only)")
	}
	return parsed, nil
}

func inventoryHTTPClient() *http.Client {
	return &http.Client{
		Transport: getOnlyTransport{base: http.DefaultTransport},
		Timeout:   30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func readToken(path string) (string, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() {
		return "", errors.New("token must be a regular non-symlink file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("token file is unavailable")
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !os.SameFile(before, info) || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("token file identity or private permissions are invalid")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil || len(raw) > 8192 {
		return "", errors.New("token exceeds read limit")
	}
	token := strings.TrimSpace(string(raw))
	if token == "" || strings.ContainsAny(token, "\r\n\t ") {
		return "", errors.New("token must contain one non-empty credential")
	}
	return token, nil
}

func inventoryRegistry(ctx context.Context, client *http.Client, base *url.URL, token string, contract registryContract, targets map[string]string, featureFlags map[string]bool) registryResult {
	result := registryResult{
		ID: contract.ID, Path: contract.Path, ExpectedIdentities: sortedUnique(contract.ExpectedIdentities), Status: "FAIL",
		HTTPStatuses: []int{}, ObservedIdentities: []string{}, MissingIdentities: []string{}, AdditionalIdentities: []string{},
	}
	path, err := expandTargets(contract.Path, targets)
	if err != nil {
		result.Reason = err.Error()
		return result
	}
	next, err := relativeAPIURL(base, path)
	if err != nil {
		result.Reason = err.Error()
		return result
	}
	identities := []string{}
	seenURLs := map[string]bool{}
	for page := 0; next != nil && page < maxPages; page++ {
		if seenURLs[next.String()] {
			result.Reason = "pagination repeated a URL"
			return result
		}
		seenURLs[next.String()] = true
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, next.String(), nil)
		if err != nil {
			result.Reason = err.Error()
			return result
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			result.Reason = "request failed: " + err.Error()
			return result
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
		closeErr := resp.Body.Close()
		result.Pages++
		result.HTTPStatuses = append(result.HTTPStatuses, resp.StatusCode)
		if readErr != nil || closeErr != nil || len(body) > maxBodyBytes {
			result.Reason = "response body could not be read within the size limit"
			return result
		}
		if resp.StatusCode != http.StatusOK {
			if enabled, known := featureFlags[contract.AbsentWhenFeatureDisabled]; resp.StatusCode == http.StatusNotFound && contract.AbsentWhenFeatureDisabled != "" && known && !enabled {
				result.Status = "PASS"
				result.Reason = fmt.Sprintf("route is unavailable because %s is explicitly disabled", contract.AbsentWhenFeatureDisabled)
				return result
			}
			result.Reason = fmt.Sprintf("GET returned HTTP %d", resp.StatusCode)
			return result
		}
		var decoded any
		if err := decodeStrictJSON(body, &decoded); err != nil {
			result.Reason = "response was ambiguous or invalid JSON"
			return result
		}
		if object, ok := decoded.(map[string]any); ok {
			if success, exists := object["ok"]; exists && success != true || object["error"] != nil {
				result.Reason = "response reported failure"
				return result
			}
		}
		resolved, err := valueAtPath(decoded, contract.ItemsPath)
		if err != nil {
			result.Reason = err.Error()
			return result
		}
		if contract.ObjectKeys {
			object, ok := resolved.(map[string]any)
			if !ok {
				result.Reason = fmt.Sprintf("items_path %q did not resolve to an object", contract.ItemsPath)
				return result
			}
			if result.ObservedValues == nil {
				result.ObservedValues = map[string]bool{}
			}
			for key, raw := range object {
				value, ok := raw.(bool)
				if !ok {
					result.Reason = fmt.Sprintf("registry object key %q was not boolean", key)
					return result
				}
				identities = append(identities, key)
				result.ObservedValues[key] = value
			}
			next, err = nextPageURL(base, next, decoded)
			if err != nil {
				result.Reason = err.Error()
				return result
			}
			continue
		}
		items, ok := resolved.([]any)
		if !ok {
			result.Reason = fmt.Sprintf("items_path %q did not resolve to an array", contract.ItemsPath)
			return result
		}
		for _, item := range items {
			object, ok := item.(map[string]any)
			if !ok {
				result.Reason = "registry item was not an object"
				return result
			}
			identity, ok := object[contract.IdentityField].(string)
			if !ok || strings.TrimSpace(identity) == "" {
				result.Reason = fmt.Sprintf("registry item lacked string identity field %q", contract.IdentityField)
				return result
			}
			identities = append(identities, identity)
		}
		next, err = nextPageURL(base, next, decoded)
		if err != nil {
			result.Reason = err.Error()
			return result
		}
	}
	if next != nil {
		result.Reason = fmt.Sprintf("pagination exceeded %d pages", maxPages)
		return result
	}
	result.ObservedIdentities = sortedUnique(identities)
	if len(result.ObservedIdentities) != len(identities) {
		result.Reason = "registry returned duplicate identities"
		return result
	}
	result.MissingIdentities = difference(result.ExpectedIdentities, result.ObservedIdentities)
	if !contract.AllowAdditional {
		result.AdditionalIdentities = difference(result.ObservedIdentities, result.ExpectedIdentities)
	}
	if len(result.MissingIdentities) != 0 || len(result.AdditionalIdentities) != 0 {
		result.Reason = "runtime offerings differ from the frozen manifest"
		return result
	}
	result.Status = "PASS"
	result.Reason = "all pages matched the frozen manifest"
	return result
}

func expandTargets(path string, targets map[string]string) (string, error) {
	for {
		start := strings.Index(path, "{")
		if start < 0 {
			return path, nil
		}
		end := strings.Index(path[start:], "}")
		if end < 0 {
			return "", errors.New("registry path has an unterminated target placeholder")
		}
		end += start
		key := path[start+1 : end]
		value := targets[key]
		if value == "" || strings.Contains(value, "/") {
			return "", fmt.Errorf("target_ids.%s is required and must be one path segment", key)
		}
		path = path[:start] + url.PathEscape(value) + path[end+1:]
	}
}

func relativeAPIURL(base *url.URL, path string) (*url.URL, error) {
	relative, err := url.Parse(path)
	if err != nil || relative.IsAbs() || relative.Host != "" || relative.User != nil || relative.Fragment != "" || !strings.HasPrefix(relative.Path, "/api/v1/") {
		return nil, errors.New("inventory paths must be relative /api/v1/ URLs")
	}
	resolved := base.ResolveReference(relative)
	if resolved.Scheme != base.Scheme || resolved.Host != base.Host || !strings.HasPrefix(resolved.Path, "/api/v1/") {
		return nil, errors.New("inventory URL escaped the configured API origin")
	}
	return resolved, nil
}

func valueAtPath(value any, path string) (any, error) {
	current := value
	if path != "" {
		for _, part := range strings.Split(path, ".") {
			object, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("items_path %q could not be traversed", path)
			}
			current, ok = object[part]
			if !ok {
				return nil, fmt.Errorf("items_path %q was missing", path)
			}
		}
	}
	return current, nil
}

func nextPageURL(base, current *url.URL, decoded any) (*url.URL, error) {
	object, ok := decoded.(map[string]any)
	if !ok {
		return nil, nil
	}
	if raw, ok := object["next"].(string); ok && raw != "" {
		return guardedNextURL(base, current, raw)
	}
	pagination, ok := object["pagination"].(map[string]any)
	if !ok {
		return nil, nil
	}
	hasMore, valid := pagination["has_more"].(bool)
	if !valid {
		return nil, errors.New("pagination omitted boolean has_more")
	}
	if !hasMore {
		return nil, nil
	}
	query := current.Query()
	if cursor, ok := pagination["next_cursor"].(string); ok && cursor != "" {
		query.Set("cursor", cursor)
		query.Del("offset")
	} else if offset, ok := jsonInteger(pagination["next_offset"]); ok {
		query.Set("offset", fmt.Sprintf("%d", offset))
	} else {
		return nil, errors.New("pagination said has_more without a continuation")
	}
	next := *current
	next.RawQuery = query.Encode()
	return &next, nil
}

func guardedNextURL(base, current *url.URL, raw string) (*url.URL, error) {
	next, err := current.Parse(raw)
	if err != nil || next.User != nil || next.Fragment != "" || next.Path != current.Path || next.Scheme != base.Scheme || next.Host != base.Host || !strings.HasPrefix(next.Path, "/api/v1/") {
		return nil, errors.New("pagination continuation escaped the configured API origin")
	}
	return next, nil
}

func jsonInteger(value any) (int64, bool) {
	switch value := value.(type) {
	case json.Number:
		integer, err := value.Int64()
		return integer, err == nil && integer >= 0
	case float64:
		return int64(value), value >= 0 && value == float64(int64(value))
	default:
		return 0, false
	}
}

func repositoryIdentity() (candidate, error) {
	commit, err := gitOutput("rev-parse", "HEAD")
	if err != nil {
		return candidate{}, fmt.Errorf("read repository commit: %w", err)
	}
	staged, err := gitBytes("diff", "--cached", "--binary")
	if err != nil {
		return candidate{}, fmt.Errorf("hash staged diff: %w", err)
	}
	unstaged, err := gitBytes("diff", "--binary")
	if err != nil {
		return candidate{}, fmt.Errorf("hash unstaged diff: %w", err)
	}
	untracked, err := untrackedDigest()
	if err != nil {
		return candidate{}, err
	}
	return candidate{
		Commit:          strings.TrimSpace(commit),
		StagedDiffSHA:   digest(staged),
		UnstagedDiffSHA: digest(unstaged),
		UntrackedSHA:    untracked,
		Dirty:           len(staged) != 0 || len(unstaged) != 0 || untracked != digest(nil),
	}, nil
}

func untrackedDigest() (string, error) {
	raw, err := gitBytes("ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", fmt.Errorf("list untracked files: %w", err)
	}
	paths := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	if len(paths) == 1 && paths[0] == "" {
		return digest(nil), nil
	}
	root, err := gitOutput("rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	root = strings.TrimSpace(root)
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		info, err := os.Lstat(filepath.Join(root, path))
		if err != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("untracked path %q must be a readable regular file", path)
		}
		body, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return "", fmt.Errorf("read untracked path %q: %w", path, err)
		}
		_, _ = fmt.Fprintf(hash, "%d:%s:%d:", len(path), path, len(body))
		hash.Write(body)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func gitOutput(args ...string) (string, error) {
	raw, err := gitBytes(args...)
	return string(raw), err
}

func gitBytes(args ...string) ([]byte, error) {
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return nil, err
	}
	command := exec.Command("git", args...)
	command.Dir = strings.TrimSpace(string(root))
	return command.Output()
}

func readStrictJSON(path string, destination any) error {
	raw, err := readBoundedFile(path, maxBodyBytes)
	if err != nil {
		return err
	}
	return decodeStrictJSON(raw, destination)
}

func writeJSONAtomic(path string, value any) error {
	directory := filepath.Dir(path)
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		return errors.New("output parent must be an existing directory")
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	temporary, err := os.CreateTemp(directory, ".offering-qualification-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	// Publish without overwriting any prior evidence or following a destination symlink.
	return os.Link(temporaryName, path)
}

func sortedUnique(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func difference(left, right []string) []string {
	other := map[string]bool{}
	for _, value := range right {
		other[value] = true
	}
	result := []string{}
	for _, value := range left {
		if !other[value] {
			result = append(result, value)
		}
	}
	return result
}

func cloneMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneBoolMap(source map[string]bool) map[string]bool {
	result := make(map[string]bool, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
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
