package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/gkebnkctl/internal/journal"
)

var (
	flagJournalFile   string
	flagJournalActor  string
	flagJournalAction string
	flagJournalTags   []string
	flagJournalJSON   bool
)

var journalCmd = &cobra.Command{
	Use:   "journal",
	Short: "Maintain an append-only audit trail and notes for cluster operations",
	Long: `gkebnkctl journal logs notes, test results, deployment checkpoints, and
handoff summaries to artifacts/journal.jsonl for immutable auditability.

Example:
  gkebnkctl journal add "Completed Phase 50 egress verification"
  gkebnkctl journal add "Swapped license JWT to production" --action LICENSE --tag prod
  gkebnkctl journal list
  gkebnkctl journal list --json`,
}

var journalAddCmd = &cobra.Command{
	Use:   "add <message>",
	Short: "Append a new entry to the workspace journal",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runJournalAdd,
}

var journalListCmd = &cobra.Command{
	Use:   "list",
	Short: "Display all entries recorded in the workspace journal",
	RunE:  runJournalList,
}

func init() {
	journalCmd.PersistentFlags().StringVar(&flagJournalFile, "file", journal.DefaultJournalPath, "Path to journal file")

	journalAddCmd.Flags().StringVar(&flagJournalActor, "actor", "", "Actor / author name (default: current user)")
	journalAddCmd.Flags().StringVar(&flagJournalAction, "action", "NOTE", "Action category (e.g. DEPLOY, TEST, FIX, NOTE)")
	journalAddCmd.Flags().StringSliceVar(&flagJournalTags, "tag", nil, "Tags to attach to entry")

	journalListCmd.Flags().BoolVar(&flagJournalJSON, "json", false, "Output entries in JSON format")

	journalCmd.AddCommand(journalAddCmd)
	journalCmd.AddCommand(journalListCmd)
	rootCmd.AddCommand(journalCmd)
}

func runJournalAdd(_ *cobra.Command, args []string) error {
	msg := strings.Join(args, " ")
	entry := journal.Entry{
		Action:  strings.ToUpper(flagJournalAction),
		Message: msg,
		Actor:   flagJournalActor,
		Tags:    flagJournalTags,
	}

	if err := journal.Append(flagJournalFile, entry); err != nil {
		return fmt.Errorf("append journal: %w", err)
	}

	fmt.Fprintf(os.Stderr, "✓ Appended entry to %s\n", flagJournalFile)
	return nil
}

func runJournalList(_ *cobra.Command, _ []string) error {
	entries, err := journal.List(flagJournalFile)
	if err != nil {
		return fmt.Errorf("read journal: %w", err)
	}

	if flagJournalJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
	}

	journal.RenderText(os.Stdout, entries)
	return nil
}
