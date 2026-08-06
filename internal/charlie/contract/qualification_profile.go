package contract

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"time"
)

const qualificationProfileSchema = "charlie.live-qualification-scenarios/v1"

var (
	//go:embed pinned/live-qualification-scenarios-v1.json
	qualificationProfileJSON []byte

	qualificationIDPattern        = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	qualificationAssertionPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,95}$`)
)

type qualificationProfile struct {
	Schema string                       `json:"schema"`
	Groups []qualificationScenarioGroup `json:"groups"`
}

type qualificationScenarioGroup struct {
	ID        string                  `json:"id"`
	Scenarios []qualificationScenario `json:"scenarios"`
}

type qualificationScenario struct {
	ID                  string   `json:"id"`
	TimeoutMilliseconds int64    `json:"timeout_ms"`
	RequiredAssertions  []string `json:"required_assertions"`
}

// QualificationScenarioContract returns immutable-by-copy assertion and
// timeout maps derived from the reviewed Charlie contract pin. Astronomer does
// not maintain a second scenario catalog: malformed or incomplete pinned
// bytes make startup/tests fail immediately.
func QualificationScenarioContract() (map[string][]string, map[string]time.Duration) {
	profile, err := decodeQualificationProfile(qualificationProfileJSON)
	if err != nil {
		panic(fmt.Sprintf("invalid pinned Charlie qualification profile: %v", err))
	}
	assertions := make(map[string][]string)
	timeouts := make(map[string]time.Duration)
	for _, group := range profile.Groups {
		for _, scenario := range group.Scenarios {
			assertions[scenario.ID] = append([]string(nil), scenario.RequiredAssertions...)
			timeouts[scenario.ID] = time.Duration(scenario.TimeoutMilliseconds) * time.Millisecond
		}
	}
	return assertions, timeouts
}

func decodeQualificationProfile(raw []byte) (qualificationProfile, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var profile qualificationProfile
	if err := decoder.Decode(&profile); err != nil {
		return qualificationProfile{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return qualificationProfile{}, fmt.Errorf("multiple JSON values")
	}
	if profile.Schema != qualificationProfileSchema || len(profile.Groups) != 2 ||
		profile.Groups[0].ID != "zero_runtime" || profile.Groups[1].ID != "proof" {
		return qualificationProfile{}, fmt.Errorf("schema or ordered groups mismatch")
	}
	seenScenarios := make(map[string]struct{})
	for _, group := range profile.Groups {
		if len(group.Scenarios) == 0 {
			return qualificationProfile{}, fmt.Errorf("group %s is empty", group.ID)
		}
		for _, scenario := range group.Scenarios {
			if !qualificationIDPattern.MatchString(scenario.ID) {
				return qualificationProfile{}, fmt.Errorf("invalid scenario id")
			}
			if _, duplicate := seenScenarios[scenario.ID]; duplicate {
				return qualificationProfile{}, fmt.Errorf("duplicate scenario id")
			}
			seenScenarios[scenario.ID] = struct{}{}
			if scenario.TimeoutMilliseconds < 1_000 || scenario.TimeoutMilliseconds > 1_800_000 || len(scenario.RequiredAssertions) == 0 || len(scenario.RequiredAssertions) > 32 {
				return qualificationProfile{}, fmt.Errorf("invalid scenario bounds")
			}
			seenAssertions := make(map[string]struct{})
			for _, assertion := range scenario.RequiredAssertions {
				if !qualificationAssertionPattern.MatchString(assertion) {
					return qualificationProfile{}, fmt.Errorf("invalid assertion")
				}
				if _, duplicate := seenAssertions[assertion]; duplicate {
					return qualificationProfile{}, fmt.Errorf("duplicate assertion")
				}
				seenAssertions[assertion] = struct{}{}
			}
		}
	}
	if len(seenScenarios) != 27 {
		return qualificationProfile{}, fmt.Errorf("scenario count mismatch")
	}
	return profile, nil
}
