package contract

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	bridgeschema "github.com/alphabravocompany/astronomer-go/internal/charlie/contract/internal/schema"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed pinned/schemas/onboarding-v1.schema.json
var onboardingSchemaJSON []byte

var (
	onboardingSchemaOnce sync.Once
	onboardingSchema     *jsonschema.Schema
	onboardingSchemaErr  error
)

type OnboardingPackage = bridgeschema.OnboardingV1SchemaJson
type OnboardingCredential = bridgeschema.Credential

const (
	CredentialPurposeAgentEnrollment = bridgeschema.CredentialPurposeAgentEnrollment
	CredentialPurposeArtifactPull    = bridgeschema.CredentialPurposeArtifactPull
)

// ParseOnboardingPackage validates the authoritative pinned JSON Schema before
// decoding generated types. Unknown fields and credential-purpose elevation are
// rejected by the schema rather than silently discarded by Go JSON decoding.
func ParseOnboardingPackage(raw []byte) (OnboardingPackage, error) {
	onboardingSchemaOnce.Do(func() {
		compiler := jsonschema.NewCompiler()
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(onboardingSchemaJSON))
		if err != nil {
			onboardingSchemaErr = err
			return
		}
		if err := compiler.AddResource("onboarding-v1.schema.json", document); err != nil {
			onboardingSchemaErr = err
			return
		}
		onboardingSchema, onboardingSchemaErr = compiler.Compile("onboarding-v1.schema.json")
	})
	if onboardingSchemaErr != nil {
		return OnboardingPackage{}, fmt.Errorf("compile pinned onboarding schema: %w", onboardingSchemaErr)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return OnboardingPackage{}, fmt.Errorf("decode onboarding JSON: %w", err)
	}
	if err := onboardingSchema.Validate(instance); err != nil {
		return OnboardingPackage{}, fmt.Errorf("onboarding schema: %w", err)
	}
	var result OnboardingPackage
	if err := json.Unmarshal(raw, &result); err != nil {
		return OnboardingPackage{}, fmt.Errorf("decode generated onboarding type: %w", err)
	}
	return result, nil
}
