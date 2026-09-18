// Package configuration resolves reusable delivery templates and scoped
// overrides into one deterministic, digest-bound configuration document.
package configuration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

const MaxDocumentBytes = 256 << 10

type Scope string

const (
	ScopeOrganization Scope = "organization"
	ScopeProject      Scope = "project"
	ScopeEnvironment  Scope = "environment"
	ScopeGroup        Scope = "group"
	ScopeCluster      Scope = "cluster"
	ScopeRollout      Scope = "rollout"
)

var scopeRank = map[Scope]int{
	ScopeOrganization: 0, ScopeProject: 1, ScopeEnvironment: 2,
	ScopeGroup: 3, ScopeCluster: 4, ScopeRollout: 5,
}

type Layer struct {
	ID         uuid.UUID       `json:"id"`
	Name       string          `json:"name"`
	Scope      Scope           `json:"scope"`
	ScopeID    uuid.UUID       `json:"scope_id,omitempty"`
	Precedence int             `json:"precedence"`
	Values     json.RawMessage `json:"values"`
	Patches    []string        `json:"patches,omitempty"`
}

type Conflict struct {
	Path       string      `json:"path"`
	Scope      Scope       `json:"scope"`
	Precedence int         `json:"precedence"`
	LayerIDs   []uuid.UUID `json:"layer_ids"`
}

type Result struct {
	Values        json.RawMessage `json:"values"`
	Patches       []string        `json:"patches"`
	AppliedLayers []uuid.UUID     `json:"applied_layers"`
	Digest        string          `json:"digest"`
}

type ConflictError struct{ Conflicts []Conflict }

func (e *ConflictError) Error() string {
	return fmt.Sprintf("configuration contains %d same-precedence conflict(s)", len(e.Conflicts))
}

// Merge applies a base document followed by increasingly specific layers.
// Within one scope, precedence is ascending; UUID is the stable final tie
// breaker. Two layers at the same scope and precedence may not assign
// different values to the same leaf, preventing arbitrary last-writer wins.
func Merge(base json.RawMessage, layers []Layer) (Result, error) {
	root, err := decodeObject(base)
	if err != nil {
		return Result{}, fmt.Errorf("base values: %w", err)
	}
	ordered := append([]Layer(nil), layers...)
	for i := range ordered {
		if _, ok := scopeRank[ordered[i].Scope]; !ok {
			return Result{}, fmt.Errorf("layer %q has unsupported scope %q", ordered[i].Name, ordered[i].Scope)
		}
		if ordered[i].ID == uuid.Nil {
			return Result{}, fmt.Errorf("layer %q has no identity", ordered[i].Name)
		}
		if len(ordered[i].Patches) > 64 {
			return Result{}, fmt.Errorf("layer %q has too many patches", ordered[i].Name)
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		left, right := ordered[i], ordered[j]
		if scopeRank[left.Scope] != scopeRank[right.Scope] {
			return scopeRank[left.Scope] < scopeRank[right.Scope]
		}
		if left.Precedence != right.Precedence {
			return left.Precedence < right.Precedence
		}
		return left.ID.String() < right.ID.String()
	})

	var conflicts []Conflict
	for start := 0; start < len(ordered); {
		end := start + 1
		for end < len(ordered) && ordered[end].Scope == ordered[start].Scope && ordered[end].Precedence == ordered[start].Precedence {
			end++
		}
		groupValues := make([]map[string]any, end-start)
		for index := start; index < end; index++ {
			values, decodeErr := decodeObject(ordered[index].Values)
			if decodeErr != nil {
				return Result{}, fmt.Errorf("layer %q values: %w", ordered[index].Name, decodeErr)
			}
			groupValues[index-start] = values
		}
		conflicts = append(conflicts, findConflicts(ordered[start:end], groupValues)...)
		if len(conflicts) != 0 {
			return Result{}, &ConflictError{Conflicts: conflicts}
		}
		for index, values := range groupValues {
			mergeObject(root, values)
			_ = index
		}
		start = end
	}

	canonical, err := json.Marshal(root)
	if err != nil || len(canonical) > MaxDocumentBytes {
		return Result{}, errors.New("effective values exceed the configured limit")
	}
	patches := make([]string, 0)
	applied := make([]uuid.UUID, 0, len(ordered))
	for _, layer := range ordered {
		patches = append(patches, layer.Patches...)
		applied = append(applied, layer.ID)
	}
	digestInput, _ := json.Marshal(struct {
		Values  json.RawMessage `json:"values"`
		Patches []string        `json:"patches"`
	}{canonical, patches})
	sum := sha256.Sum256(digestInput)
	return Result{Values: canonical, Patches: patches, AppliedLayers: applied, Digest: "sha256:" + hex.EncodeToString(sum[:])}, nil
}

func decodeObject(raw json.RawMessage) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if len(raw) > MaxDocumentBytes {
		return nil, errors.New("document exceeds 256 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var result map[string]any
	if err := decoder.Decode(&result); err != nil || result == nil || decoder.Decode(&struct{}{}) == nil {
		return nil, errors.New("must be exactly one JSON object")
	}
	return result, nil
}

func mergeObject(target, overlay map[string]any) {
	for key, value := range overlay {
		if value == nil {
			delete(target, key)
			continue
		}
		child, childOK := value.(map[string]any)
		existing, existingOK := target[key].(map[string]any)
		if childOK && existingOK {
			mergeObject(existing, child)
			continue
		}
		target[key] = value
	}
}

type leafAssignment struct {
	value any
	ids   []uuid.UUID
}

func findConflicts(layers []Layer, values []map[string]any) []Conflict {
	seen := map[string]leafAssignment{}
	conflicts := map[string]Conflict{}
	for index, document := range values {
		flatten(document, "", func(path string, value any) {
			assignment, exists := seen[path]
			if !exists {
				seen[path] = leafAssignment{value: value, ids: []uuid.UUID{layers[index].ID}}
				return
			}
			if equalJSON(assignment.value, value) {
				assignment.ids = append(assignment.ids, layers[index].ID)
				seen[path] = assignment
				return
			}
			ids := append(append([]uuid.UUID(nil), assignment.ids...), layers[index].ID)
			conflicts[path] = Conflict{Path: path, Scope: layers[index].Scope, Precedence: layers[index].Precedence, LayerIDs: ids}
		})
	}
	paths := make([]string, 0, len(conflicts))
	for path := range conflicts {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	result := make([]Conflict, 0, len(paths))
	for _, path := range paths {
		result = append(result, conflicts[path])
	}
	return result
}

func flatten(value map[string]any, prefix string, visit func(string, any)) {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		path := prefix + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
		if child, ok := value[key].(map[string]any); ok && child != nil {
			flatten(child, path, visit)
			continue
		}
		visit(path, value[key])
	}
}

func equalJSON(left, right any) bool {
	a, _ := json.Marshal(left)
	b, _ := json.Marshal(right)
	return bytes.Equal(a, b)
}
