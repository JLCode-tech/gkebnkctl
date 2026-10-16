package cli

import "github.com/spf13/cobra"

// Top-level shortcuts for the most common verbs (`get`, `logs`) so users
// do not need to learn the `k` prefix. Both delegate directly to the underlying
// in-process client-go implementations (runKGet and runKLogs).

func init() {
	if !hasCommand(rootCmd, "get") {
		rootCmd.AddCommand(newKGetCmd())
	}
	if !hasCommand(rootCmd, "logs") {
		rootCmd.AddCommand(newKLogsCmd())
	}
}

func hasCommand(parent *cobra.Command, name string) bool {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return true
		}
	}
	return false
}
