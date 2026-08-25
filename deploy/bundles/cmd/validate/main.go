package main

import (
	"fmt"
	"os"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: validate <catalog.json>")
		os.Exit(2)
	}
	payload, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "read built-in catalog: %v\n", err)
		os.Exit(1)
	}
	if _, err := builtinbundles.Parse(payload); err != nil {
		fmt.Fprintf(os.Stderr, "validate built-in catalog: %v\n", err)
		os.Exit(1)
	}
}
