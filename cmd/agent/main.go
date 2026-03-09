package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

func main() {
	rootCmd := &cobra.Command{
		Use:     "astronomer-agent",
		Short:   "Astronomer agent for connecting Kubernetes clusters",
		Version: version.Version,
	}

	connectCmd := &cobra.Command{
		Use:   "connect",
		Short: "Connect this agent to the Astronomer server",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("connect: not yet implemented")
			return nil
		},
	}

	rootCmd.AddCommand(connectCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
