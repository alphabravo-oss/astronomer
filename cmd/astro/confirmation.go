package main

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// confirmAction reads a complete answer from the command's input. EOF, read
// errors, and anything except an explicit affirmative leave the action denied.
func confirmAction(cmd *cobra.Command, prompt string) error {
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s [y/N] ", prompt); err != nil {
		return err
	}
	scanner := bufio.NewScanner(cmd.InOrStdin())
	if scanner.Scan() {
		switch strings.ToLower(strings.TrimSpace(scanner.Text())) {
		case "y", "yes":
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read confirmation: %w", err)
	}
	return fmt.Errorf("aborted")
}
