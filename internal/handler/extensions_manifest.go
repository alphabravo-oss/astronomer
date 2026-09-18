package handler

import (
	"net/http"
)

type ExtensionManifest struct {
	APIVersion           string                  `json:"apiVersion"`
	Name                 string                  `json:"name"`
	DisplayName          string                  `json:"displayName"`
	Version              string                  `json:"version"`
	CompatibleAstronomer string                  `json:"compatibleAstronomer"`
	Entry                string                  `json:"entry"`
	Permissions          []string                `json:"permissions"`
	BackendAPIScopes     []string                `json:"backendApiScopes,omitempty"`
	CSP                  ExtensionCSP            `json:"csp,omitempty"`
	ExtensionPoints      ExtensionManifestPoints `json:"extensionPoints"`
}

type ExtensionCSP struct {
	ScriptSrc  []string `json:"scriptSrc,omitempty"`
	ConnectSrc []string `json:"connectSrc,omitempty"`
	FrameSrc   []string `json:"frameSrc,omitempty"`
	ImageSrc   []string `json:"imageSrc,omitempty"`
}

type ExtensionManifestPoints struct {
	Sidebar     []ExtensionSidebarPoint `json:"sidebar,omitempty"`
	Widgets     []ExtensionWidgetPoint  `json:"widgets,omitempty"`
	ClusterTabs []ExtensionClusterTab   `json:"clusterTabs,omitempty"`
	Settings    []ExtensionSettingsPage `json:"settings,omitempty"`
}

type ExtensionSidebarPoint struct {
	Label       string           `json:"label"`
	Path        string           `json:"path"`
	Render      *ExtensionRender `json:"render,omitempty"`
	DataSources []DataSourceRef  `json:"dataSources,omitempty"`
}

type ExtensionWidgetPoint struct {
	ID          string           `json:"id"`
	Title       string           `json:"title"`
	Render      *ExtensionRender `json:"render,omitempty"`
	DataSources []DataSourceRef  `json:"dataSources,omitempty"`
}

type ExtensionClusterTab struct {
	Label       string           `json:"label"`
	Component   string           `json:"component"`
	Render      *ExtensionRender `json:"render,omitempty"`
	DataSources []DataSourceRef  `json:"dataSources,omitempty"`
}

type ExtensionSettingsPage struct {
	Label       string           `json:"label"`
	Component   string           `json:"component"`
	Render      *ExtensionRender `json:"render,omitempty"`
	DataSources []DataSourceRef  `json:"dataSources,omitempty"`
}

// ExtensionRender is attached per extension point. Exactly one of Declarative
// (Tier 1) or Bundle (Tier 2) is set. Absent => legacy entry, mounts nothing.
type ExtensionRender struct {
	Declarative *DeclarativeWidget `json:"declarative,omitempty"` // Tier 1
	Bundle      *BundleDescriptor  `json:"bundle,omitempty"`      // Tier 2
}

// DataSourceRef is a named, RBAC-allowlisted data source (never a raw URL). It
// is referenced by widgets and bridge calls and re-checked at call time.
type DataSourceRef struct {
	ID              string            `json:"id"`
	Proxy           string            `json:"proxy"`  // host allowlist: "astronomer-api"|"k8s"|"prometheus"
	Method          string            `json:"method"` // "GET" | "POST"
	Path            string            `json:"path"`   // host template, e.g. "/api/v1/clusters/{clusterId}/pods"
	Query           map[string]string `json:"query,omitempty"`
	RBAC            RBACRequirement   `json:"rbac"`
	Shape           string            `json:"shape"`            // "list" | "object" | "series"
	Fields          []string          `json:"fields,omitempty"` // response projection allowlist (dot-paths; "*" rejected)
	MaxRows         int               `json:"maxRows,omitempty"`
	CacheTTLSeconds int               `json:"cacheTtlSeconds,omitempty"`
}

type RBACRequirement struct {
	Resource string `json:"resource"` // MUST be IsCanonicalResource (no "*")
	Verb     string `json:"verb"`     // MUST be IsCanonicalVerb (no "*")
	Scope    string `json:"scope"`    // "global" | "cluster" | "project"
}

// DeclarativeWidget is a Tier 1 widget spec (zero third-party JS).
type DeclarativeWidget struct {
	Kind       string         `json:"kind"`       // "table" | "chart" | "stat" | "form"
	DataSource string         `json:"dataSource"` // ref into the point's DataSources[].ID
	Fields     []FieldBinding `json:"fields,omitempty"`
	Chart      *ChartSpec     `json:"chart,omitempty"` // kind=chart
	Form       *FormSpec      `json:"form,omitempty"`  // kind=form
	Stat       *StatSpec      `json:"stat,omitempty"`  // kind=stat
	EmptyText  string         `json:"emptyText,omitempty"`
}

type FieldBinding struct {
	Path   string `json:"path"` // JSONPath-lite into a proxy row, "metadata.name"
	Label  string `json:"label"`
	Format string `json:"format,omitempty"` // closed enum: text|number|bytes|datetime|duration|badge|currency
}

type ChartSpec struct {
	Type string   `json:"type"` // "line" | "bar" | "area"
	X    string   `json:"x"`
	Y    []string `json:"y"`
}

type StatSpec struct {
	Value FieldBinding  `json:"value"`
	Delta *FieldBinding `json:"delta,omitempty"`
	Label string        `json:"label"`
}

type FormSpec struct {
	Submit      string      `json:"submit"` // a DataSources[].ID with Method=POST + write verb
	Inputs      []FormInput `json:"inputs"`
	SubmitLabel string      `json:"submitLabel"`
}

type FormInput struct {
	Name      string   `json:"name"`
	Label     string   `json:"label"`
	Type      string   `json:"type"`              // "text"|"number"|"select"|"toggle"
	Options   []string `json:"options,omitempty"` // type=select
	MaxLength int      `json:"maxLength,omitempty"`
	Required  bool     `json:"required"`
}

func (h *ExtensionHandler) SampleManifest(w http.ResponseWriter, r *http.Request) {
	RespondJSON(w, http.StatusOK, sampleExtensionManifest())
}

func sampleExtensionManifest() ExtensionManifest {
	return ExtensionManifest{
		APIVersion:           "extensions.astronomer.io/v1alpha1",
		Name:                 "cost-insights",
		DisplayName:          "Cost Insights",
		Version:              "0.1.0",
		CompatibleAstronomer: ">=0.2.0 <1.0.0",
		Entry:                "index.js",
		Permissions:          []string{"clusters:read", "monitoring:read"},
		CSP: ExtensionCSP{
			ConnectSrc: []string{"'self'"},
			ImageSrc:   []string{"'self'", "data:"},
		},
		ExtensionPoints: ExtensionManifestPoints{
			Sidebar: []ExtensionSidebarPoint{{
				Label: "Cost",
				Path:  "/dashboard/extensions/cost-insights",
			}},
			Widgets: []ExtensionWidgetPoint{{
				ID:    "cost-summary",
				Title: "Cost summary",
			}},
			ClusterTabs: []ExtensionClusterTab{{
				Label:     "Cost",
				Component: "ClusterCostTab",
			}},
		},
	}
}
