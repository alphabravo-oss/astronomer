// Package contract owns Astronomer's pinned Charlie Product Bridge client.
package contract

//go:generate go run github.com/atombender/go-jsonschema@v0.24.1 -p bridgeschema -o internal/schema/schema.gen.go pinned/schemas/onboarding-v1.schema.json pinned/schemas/capability-v1.schema.json pinned/schemas/connector-capability-v1.schema.json pinned/schemas/kubernetes-visibility-v1.schema.json pinned/schemas/finding-v1.schema.json pinned/schemas/alert-delivery-v1.schema.json
//go:generate go run ./cmd/specprep pinned/bridge.openapi.yaml pinned/schemas/finding-v1.schema.json pinned/schemas/alert-delivery-v1.schema.json internal/wire/bridge.codegen.openapi.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.5.0 -config codegen.yaml internal/wire/bridge.codegen.openapi.yaml
