package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/gkebnkctl/internal/self"
)

var (
	flagSelfRepo string
)

var selfCmd = &cobra.Command{
	Use:   "self",
	Short: "Check for gkebnkctl releases and self-update",
	Long: `gkebnkctl self checks GitHub releases for updates to the CLI binary
and optionally upgrades the local binary in place.

Example:
  gkebnkctl self check
  gkebnkctl self update`,
}

var selfCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Check GitHub for newer versions of gkebnkctl",
	RunE:  runSelfCheck,
}

var selfUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Download and install the latest gkebnkctl release",
	RunE:  runSelfUpdate,
}

func init() {
	selfCmd.PersistentFlags().StringVar(&flagSelfRepo, "repo", self.GitHubRepo, "GitHub repository to check for updates")

	selfCmd.AddCommand(selfCheckCmd)
	selfCmd.AddCommand(selfUpdateCmd)
	rootCmd.AddCommand(selfCmd)
}

func runSelfCheck(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	fmt.Printf("Checking for gkebnkctl updates (current: %s)...\n", Version)

	rel, hasUpdate, err := self.CheckUpdate(ctx, flagSelfRepo, Version)
	if err != nil {
		return fmt.Errorf("check updates: %w", err)
	}

	if hasUpdate {
		fmt.Printf("★ New release available: %s (current: %s)\n", rel.TagName, Version)
		fmt.Printf("  Release notes: %s\n", rel.HTMLURL)
		fmt.Printf("  To upgrade, run: gkebnkctl self update\n")
	} else {
		fmt.Printf("✓ You are up to date! (latest: %s)\n", rel.TagName)
	}

	return nil
}

func runSelfUpdate(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	rel, hasUpdate, err := self.CheckUpdate(ctx, flagSelfRepo, Version)
	if err != nil {
		return fmt.Errorf("check updates: %w", err)
	}

	if !hasUpdate && Version != "dev" {
		fmt.Printf("Already on latest version (%s).\n", Version)
		return nil
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable path: %w", err)
	}

	fmt.Printf("Downloading %s to %s...\n", rel.TagName, execPath)
	tmpPath := execPath + ".tmp"
	if err := self.DownloadAsset(ctx, rel, tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("download update: %w", err)
	}

	if err := os.Rename(tmpPath, execPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace binary %s: %w", execPath, err)
	}

	fmt.Printf("✓ Successfully updated gkebnkctl to %s\n", rel.TagName)
	return nil
}
