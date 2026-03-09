package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

func main() {
	fmt.Printf("astronomer-worker %s (commit: %s, built: %s)\n",
		version.Version, version.GitCommit, version.BuildDate)
	fmt.Println("worker: not yet implemented")

	// Block until signal so it behaves like a long-running process.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	fmt.Println("worker stopped")
}
