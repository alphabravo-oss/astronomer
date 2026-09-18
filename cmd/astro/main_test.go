package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func TestRootCommandExposesTheOperatorSurface(t *testing.T) {
	root := newRootCmd()
	if root.Use != "astro" || root.Version == "" {
		t.Fatalf("root identity = use %q version %q", root.Use, root.Version)
	}
	if !root.SilenceErrors || !root.SilenceUsage {
		t.Fatal("root command must leave error rendering to main")
	}

	var names []string
	for _, command := range root.Commands() {
		names = append(names, command.Name())
	}
	for _, required := range []string{
		"admin",
		"audit",
		"backup",
		"catalog",
		"cluster",
		"cluster-agent",
		"config",
		"delivery",
		"docs",
		"k8s",
		"login",
		"monitoring",
		"projects",
		"rbac",
		"settings",
		"users",
		"workloads",
	} {
		if !slices.Contains(names, required) {
			t.Errorf("root commands %v missing %q", names, required)
		}
	}
	if root.PersistentFlags().Lookup("server") == nil ||
		root.PersistentFlags().Lookup("token") == nil ||
		root.PersistentFlags().Lookup(outputFlagName) == nil {
		t.Fatal("root command is missing a global connection/output flag")
	}
}

func TestRootHelpRendersWithoutConfiguration(t *testing.T) {
	root := newRootCmd()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"astro is the command-line client",
		"Available Commands",
		"Continuous Delivery",
		"--output",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("help output missing %q", expected)
		}
	}
}
