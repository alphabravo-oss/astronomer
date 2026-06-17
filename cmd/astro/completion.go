// Shell-completion subcommand.
//
// Thin wrapper over cobra's built-in completion generators so the help
// text, examples, and install instructions live next to the rest of the
// CLI rather than relying on cobra's auto-injected default command.

package main

import (
	"github.com/spf13/cobra"
)

func newCompletionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:       "completion [bash|zsh|fish|powershell]",
		Short:     "Generate shell completion script",
		Long: `Generate a shell completion script for astro.

Load completions into the current shell session:

  Bash:
    source <(astro completion bash)

  Zsh:
    source <(astro completion zsh)
    # ensure 'autoload -U compinit; compinit' has run

  Fish:
    astro completion fish | source

  PowerShell:
    astro completion powershell | Out-String | Invoke-Expression

To load on every session, write the script to your shell's completion
directory (e.g. /etc/bash_completion.d/astro for bash).`,
		Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root := cmd.Root()
			w := cmd.OutOrStdout()
			switch args[0] {
			case "bash":
				return root.GenBashCompletionV2(w, true)
			case "zsh":
				return root.GenZshCompletion(w)
			case "fish":
				return root.GenFishCompletion(w, true)
			case "powershell":
				return root.GenPowerShellCompletionWithDesc(w)
			}
			return nil
		},
	}
	return cmd
}
