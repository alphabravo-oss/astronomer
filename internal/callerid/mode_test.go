package callerid

import "testing"

// TestModeDefaultsOff is the flag's whole Phase 0 contract: everything that is
// not an explicit, valid, non-off value resolves to off.
func TestModeDefaultsOff(t *testing.T) {
	for name, annotations := range map[string]map[string]string{
		"nil map":          nil,
		"empty map":        {},
		"unrelated keys":   {"astronomer.io/agent-privilege-profile": "operator"},
		"blank value":      {ModeAnnotation: ""},
		"whitespace":       {ModeAnnotation: "   "},
		"typo":             {ModeAnnotation: "enfroce"},
		"explicit off":     {ModeAnnotation: "off"},
		"case-insensitive": {ModeAnnotation: "OFF"},
	} {
		if got := ModeFromAnnotations(annotations); got != ModeOff {
			t.Errorf("%s: mode = %q, want off", name, got)
		}
	}
}

func TestModeParsing(t *testing.T) {
	for raw, want := range map[string]Mode{
		"off":        ModeOff,
		"attribute":  ModeAttribute,
		"enforce":    ModeEnforce,
		" Enforce\t": ModeEnforce,
		"ATTRIBUTE":  ModeAttribute,
	} {
		got, ok := ParseMode(raw)
		if !ok || got != want {
			t.Errorf("ParseMode(%q) = (%q, %v), want (%q, true)", raw, got, ok, want)
		}
	}
	// A typo must be REJECTED, not coerced. Coercing "enfroce" to off is the
	// friendly-looking behaviour that becomes a footgun the moment somebody
	// believes they enabled enforcement and did not.
	for _, raw := range []string{"", "enfroce", "on", "true", "attribute+"} {
		if _, ok := ParseMode(raw); ok {
			t.Errorf("ParseMode(%q) must not be accepted", raw)
		}
	}
}

// TestModeFromJSONSurvivesNonStringSiblings is why ModeFromJSON exists rather
// than ModeFromAnnotations(clusterAnnotations(raw)): `clusters.annotations` is a
// free-form JSONB blob, and decoding it into map[string]string fails WHOLESALE
// on a single non-string value — reporting "off" for a cluster that is in fact
// enforcing, and (in the write gate) destroying every sibling key.
func TestModeFromJSONSurvivesNonStringSiblings(t *testing.T) {
	for name, tc := range map[string]struct {
		raw  string
		want Mode
	}{
		"non-string sibling":  {`{"replicas":3,"` + ModeAnnotation + `":"enforce"}`, ModeEnforce},
		"nested object":       {`{"tags":{"a":"b"},"` + ModeAnnotation + `":"attribute"}`, ModeAttribute},
		"plain":               {`{"` + ModeAnnotation + `":"attribute"}`, ModeAttribute},
		"absent":              {`{"owner":"sre"}`, ModeOff},
		"empty object":        {`{}`, ModeOff},
		"json null":           {`null`, ModeOff},
		"array":               {`[1,2]`, ModeOff},
		"scalar":              {`"nope"`, ModeOff},
		"malformed":           {`{`, ModeOff},
		"non-string value":    {`{"` + ModeAnnotation + `":3}`, ModeOff},
		"typo value":          {`{"` + ModeAnnotation + `":"enfroce"}`, ModeOff},
		"case and whitespace": {`{"` + ModeAnnotation + `":" ENFORCE "}`, ModeEnforce},
	} {
		if got := ModeFromJSON([]byte(tc.raw)); got != tc.want {
			t.Errorf("%s: ModeFromJSON(%s) = %q, want %q", name, tc.raw, got, tc.want)
		}
	}
	if got := ModeFromJSON(nil); got != ModeOff {
		t.Errorf("ModeFromJSON(nil) = %q, want off", got)
	}
}

// TestOnlyEnforceRequiresTheAgentCapability: attribute can deny nothing, so an
// agent that ignores it is harmless; enforce against an agent without the grant
// 403s every request.
func TestOnlyEnforceRequiresTheAgentCapability(t *testing.T) {
	if ModeOff.RequiresAgentCapability() || ModeAttribute.RequiresAgentCapability() {
		t.Fatal("only enforce requires the capability handshake")
	}
	if !ModeEnforce.RequiresAgentCapability() {
		t.Fatal("enforce must require the capability handshake")
	}
}
