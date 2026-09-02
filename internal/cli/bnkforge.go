package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/gkebnkctl/internal/bnkforge"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
)

var (
	flagForgeURL  string
	flagForgeFile string
	flagForgeJSON bool
)

var bnkforgeCmd = &cobra.Command{
	Use:   "bnkforge",
	Short: "Integrate with BNK Forge testbed and registration daemon",
	Long: `gkebnkctl bnkforge registers, queries, and unregisters your GKE BNK deployment
with the central BNK Forge testbed topology daemon.

Example:
  gkebnkctl bnkforge register --url http://bnkforge.corp.local:8080 -f cluster.yaml
  gkebnkctl bnkforge status --url http://bnkforge.corp.local:8080 -f cluster.yaml
  gkebnkctl bnkforge unregister --url http://bnkforge.corp.local:8080 -f cluster.yaml`,
}

var bnkforgeRegisterCmd = &cobra.Command{
	Use:   "register",
	Short: "Register cluster with BNK Forge",
	RunE:  runBNKForgeRegister,
}

var bnkforgeUnregisterCmd = &cobra.Command{
	Use:   "unregister",
	Short: "Unregister cluster from BNK Forge",
	RunE:  runBNKForgeUnregister,
}

var bnkforgeStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Query cluster status in BNK Forge",
	RunE:  runBNKForgeStatus,
}

func init() {
	bnkforgeCmd.PersistentFlags().StringVar(&flagForgeURL, "url", "http://localhost:8080", "BNK Forge daemon base URL")
	bnkforgeCmd.PersistentFlags().StringVarP(&flagForgeFile, "file", "f", "cluster.yaml", "Path to cluster.yaml")

	bnkforgeStatusCmd.Flags().BoolVar(&flagForgeJSON, "json", false, "Output status in JSON format")

	bnkforgeCmd.AddCommand(bnkforgeRegisterCmd)
	bnkforgeCmd.AddCommand(bnkforgeUnregisterCmd)
	bnkforgeCmd.AddCommand(bnkforgeStatusCmd)
	rootCmd.AddCommand(bnkforgeCmd)
}

func runBNKForgeRegister(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	cl, err := intent.Load(flagForgeFile)
	if err != nil {
		return fmt.Errorf("load %s: %w", flagForgeFile, err)
	}

	client := bnkforge.NewClient(flagForgeURL)
	payload := bnkforge.BuildPayload(cl, "ACTIVE")

	if err := client.Register(ctx, payload); err != nil {
		return fmt.Errorf("register with bnkforge: %w", err)
	}

	fmt.Fprintf(os.Stdout, "✓ Registered cluster %s with BNK Forge (%s)\n", cl.Metadata.Name, flagForgeURL)
	return nil
}

func runBNKForgeUnregister(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	cl, err := intent.Load(flagForgeFile)
	if err != nil {
		return fmt.Errorf("load %s: %w", flagForgeFile, err)
	}

	client := bnkforge.NewClient(flagForgeURL)
	if err := client.Unregister(ctx, cl.Metadata.Name); err != nil {
		return fmt.Errorf("unregister from bnkforge: %w", err)
	}

	fmt.Fprintf(os.Stdout, "✓ Unregistered cluster %s from BNK Forge\n", cl.Metadata.Name)
	return nil
}

func runBNKForgeStatus(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	cl, err := intent.Load(flagForgeFile)
	if err != nil {
		return fmt.Errorf("load %s: %w", flagForgeFile, err)
	}

	client := bnkforge.NewClient(flagForgeURL)
	st, err := client.GetStatus(ctx, cl.Metadata.Name)
	if err != nil {
		return fmt.Errorf("query bnkforge status: %w", err)
	}

	if flagForgeJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(st)
	}

	fmt.Printf("BNK Forge Status for %s:\n", st.Name)
	fmt.Printf("  Provider:   %s\n", st.Provider)
	fmt.Printf("  Status:     %s\n", st.Status)
	fmt.Printf("  Registered: %s\n", st.RegisteredAt)
	return nil
}
