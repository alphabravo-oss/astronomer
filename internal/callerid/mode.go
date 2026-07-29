package callerid

import (
	"encoding/json"
	"strings"
)

// ModeAnnotation is the per-cluster tri-state flag from
// docs/design/downstream-impersonation.md §5, stored in `clusters.annotations`
// (JSONB, 001_initial.up.sql:80-81). VERIFIED: no migration is needed — the
// column already exists, is already `jsonb NOT NULL DEFAULT '{}'`, and is
// already round-tripped by CreateCluster/UpdateCluster and surfaced on the
// cluster response, so this is a new key in an existing free-form map.
const ModeAnnotation = "astronomer.io/downstream-impersonation"

// Mode is the tri-state. DEFAULT OFF: absent, blank, and unrecognized all
// resolve to ModeOff, so nothing can drift into a permissive state by typo.
type Mode string

const (
	// ModeOff is today's behaviour exactly. Identity rides on the payload and
	// the agent drops it. THIS IS THE DEFAULT AND THE ONLY MODE PHASE 0 SHIPS
	// A CONSUMER FOR.
	ModeOff Mode = "off"
	// ModeAttribute is Option C's attribution: the agent stamps User-Agent and
	// ?fieldManager= only. No RBAC change; no request can be denied by it.
	// Phase 1 — no consumer exists yet, see the note on Modes().
	ModeAttribute Mode = "attribute"
	// ModeEnforce additionally stamps the Impersonate-* headers. Requires the
	// downstream RBAC re-apply AND the agent's capability advertisement.
	// Phase 2 — the RBAC it depends on is not built.
	ModeEnforce Mode = "enforce"
)

// Modes lists the accepted values in increasing order of privilege.
//
// PHASE 0 STATUS: the flag is stored, validated, superuser-gated and surfaced,
// and it is read by nothing. No mode value reaches the agent, because the
// design's §8 item 7 defines the flag plumbing WITHOUT any wire transport
// ("always resolves to off until Phase 1") — so the agent-side consumers
// (attribution stamping, 403 disambiguation) cannot be wired in this phase and
// are not. See the return notes on items 8 and 9.
func Modes() []Mode { return []Mode{ModeOff, ModeAttribute, ModeEnforce} }

// ParseMode validates a raw annotation value. ok is false for anything that is
// not one of the three literals — callers surface that as a 400 rather than
// silently coercing, because silently coercing "enfrce" to off is a footgun in
// the other direction once Phase 2 ships.
func ParseMode(raw string) (Mode, bool) {
	switch Mode(strings.ToLower(strings.TrimSpace(raw))) {
	case ModeOff:
		return ModeOff, true
	case ModeAttribute:
		return ModeAttribute, true
	case ModeEnforce:
		return ModeEnforce, true
	default:
		return ModeOff, false
	}
}

// ModeFromAnnotations resolves the effective mode for a cluster. Absent,
// blank, or unparseable all yield ModeOff — fail closed to today's behaviour.
func ModeFromAnnotations(annotations map[string]string) Mode {
	if len(annotations) == 0 {
		return ModeOff
	}
	mode, ok := ParseMode(annotations[ModeAnnotation])
	if !ok {
		return ModeOff
	}
	return mode
}

// ModeFromJSON resolves the effective mode straight off a raw
// `clusters.annotations` JSONB blob.
//
// It exists because the blob is free-form and may legally hold non-string
// values: decoding it into map[string]string fails wholesale on
// `{"replicas":3,"astronomer.io/downstream-impersonation":"enforce"}` and would
// report "off" for a cluster that is in fact enforcing. Decoding only the one
// key is both correct and immune to the siblings.
//
// Absent, blank, non-object, non-string and unparseable all yield ModeOff —
// fail closed to today's behaviour.
func ModeFromJSON(raw []byte) Mode {
	if len(raw) == 0 {
		return ModeOff
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ModeOff
	}
	rawValue, ok := obj[ModeAnnotation]
	if !ok {
		return ModeOff
	}
	var value string
	if err := json.Unmarshal(rawValue, &value); err != nil {
		return ModeOff
	}
	mode, ok := ParseMode(value)
	if !ok {
		return ModeOff
	}
	return mode
}

// RequiresAgentCapability reports whether moving a cluster to this mode
// requires the agent to have advertised protocol.FeatureImpersonation. Only
// `enforce` does: `attribute` needs no downstream RBAC and can deny nothing,
// so an agent that ignores it is harmless, whereas turning on `enforce`
// against an agent without the grant 403s every request.
func (m Mode) RequiresAgentCapability() bool { return m == ModeEnforce }
