// Package netpol owns the rendering of network_policy_templates.spec_template
// into concrete NetworkPolicy YAML. The handler / reconciler both consume
// the same Render entrypoint so the wire format stays consistent between
// the "preview" UI button and the worker's actual SSA payload.
//
// Templates are Go text/template. Variables exposed:
//
//	{{.Namespace}}   target namespace (kebab-case Kubernetes name)
//	{{.Project}}     owning project name (may be "")
//	{{.PolicyName}}  resolved in-cluster object name; reconciler picks
//	                 "astronomer-np-<template_slug>" so re-applies update
//	                 the same object instead of cluster-bombing names.
//
// Migration 068.
package netpol

import (
	"bytes"
	"fmt"
	"text/template"
)

// Context is the parameter struct passed to text/template.Execute. Fields
// are exported so they're visible to templates via dot syntax.
type Context struct {
	// Namespace is the target Kubernetes namespace.
	Namespace string
	// Project is the owning Astronomer project name. Empty for cluster-
	// scoped applications that don't belong to a project; templates that
	// reference {{.Project}} should be a no-op or fall back gracefully
	// when this is empty.
	Project string
	// PolicyName is the resolved name of the in-cluster NetworkPolicy
	// resource. Picked by the caller (typically "astronomer-np-<slug>")
	// so re-applies converge on the same object rather than creating a
	// new one per render.
	PolicyName string
}

// PolicyName returns the canonical in-cluster NetworkPolicy object name
// for a given template slug. The "astronomer-np-" prefix is the marker
// the reconciler uses to distinguish OUR policies from operator-created
// ones with the same template suffix — we own (and re-stamp) anything
// with this prefix, and leave anything else alone.
func PolicyName(slug string) string {
	return "astronomer-np-" + slug
}

// Render executes the spec template against ctx. Returns the rendered
// YAML (one document) on success. Any template parse / execute error
// bubbles up with context so the reconciler can stamp a useful
// last_error on the application row.
func Render(spec string, ctx Context) ([]byte, error) {
	if spec == "" {
		return nil, fmt.Errorf("network policy template is empty")
	}
	tmpl, err := template.New("netpol").Option("missingkey=zero").Parse(spec)
	if err != nil {
		return nil, fmt.Errorf("parse network policy template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return nil, fmt.Errorf("execute network policy template: %w", err)
	}
	return buf.Bytes(), nil
}
