package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/gkebnkctl/internal/agent"
)

var (
	flagAgentFile  string
	flagAgentForce bool
)

var agentCmd = &cobra.Command{
	Use:   "agent [gemini|claude|opencode|cursor|all]",
	Short: "Generate AI pair-programming assistant instructions (AGENTS.md, CLAUDE.md, etc.)",
	Long: `gkebnkctl agent generates tailored instructions, cluster topology summaries,
and workflow recipes for AI agents (Antigravity, Claude Code, Cursor, OpenCode).

Example:
  gkebnkctl agent gemini
  gkebnkctl agent claude
  gkebnkctl agent all --force`,
	Args: cobra.MaximumNArgs(1),
	RunE: runAgent,
}

func init() {
	agentCmd.Flags().StringVarP(&flagAgentFile, "file", "f", "cluster.yaml", "Path to cluster.yaml")
	agentCmd.Flags().BoolVar(&flagAgentForce, "force", false, "Overwrite existing instruction files")
	rootCmd.AddCommand(agentCmd)
}

func runAgent(_ *cobra.Command, args []string) error {
	target := "all"
	if len(args) > 0 {
		target = args[0]
	}

	harness := agent.TargetHarness(target)
	generated, err := agent.Generate(harness, flagAgentFile, flagAgentForce)
	if err != nil {
		return fmt.Errorf("generate agent instructions: %w", err)
	}

	fmt.Fprintf(os.Stderr, "✓ Generated agent instructions:\n")
	for _, f := range generated {
		fmt.Fprintf(os.Stderr, "  • %s\n", f)
	}
	return nil
}
