package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/kubeconfig"
)

var (
	flagKubeconfigClusterFile   string
	flagKubeconfigExport        bool
	flagKubeconfigWriteGlobal   bool
	flagKubeconfigRestoreGlobal bool
	flagKubeconfigOut           string
)

var kubeconfigCmd = &cobra.Command{
	Use:   "kubeconfig",
	Short: "Fetch and export GKE cluster kubeconfig, or sync with ~/.kube/config for k9s",
	Long: `gkebnkctl kubeconfig fetches cluster credentials from GKE and formats a valid Kubeconfig:
  - Writes to artifacts/kubeconfig by default
  - Prints to stdout with --export
  - Safely syncs to ~/.kube/config with --write-global (automatically backs up existing ~/.kube/config)
  - Restores previous ~/.kube/config with --restore-global

Example:
  gkebnkctl kubeconfig -f cluster.yaml
  gkebnkctl kubeconfig -f cluster.yaml --export
  gkebnkctl kubeconfig -f cluster.yaml --write-global
  gkebnkctl kubeconfig --restore-global`,
	RunE: runKubeconfig,
}

func init() {
	kubeconfigCmd.Flags().StringVarP(&flagKubeconfigClusterFile, "file", "f", "cluster.yaml", "Path to cluster.yaml")
	kubeconfigCmd.Flags().BoolVar(&flagKubeconfigExport, "export", false, "Print kubeconfig YAML to stdout")
	kubeconfigCmd.Flags().BoolVar(&flagKubeconfigWriteGlobal, "write-global", false, "Safely write to ~/.kube/config (backed up to ~/.kube/config.gkebnkctl-bak)")
	kubeconfigCmd.Flags().BoolVar(&flagKubeconfigRestoreGlobal, "restore-global", false, "Restore ~/.kube/config from backup")
	kubeconfigCmd.Flags().StringVar(&flagKubeconfigOut, "out", "", "Custom output path for kubeconfig file")

	rootCmd.AddCommand(kubeconfigCmd)
}

func runKubeconfig(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	if flagKubeconfigRestoreGlobal {
		restored, err := kubeconfig.RestoreGlobal()
		if err != nil {
			return fmt.Errorf("restore global kubeconfig: %w", err)
		}
		if !restored {
			fmt.Fprintln(os.Stderr, "No gkebnkctl backup found at ~/.kube/config.gkebnkctl-bak")
			return nil
		}
		fmt.Fprintln(os.Stderr, "✓ Successfully restored ~/.kube/config from backup.")
		return nil
	}

	cl, err := intent.Load(flagKubeconfigClusterFile)
	if err != nil {
		return fmt.Errorf("load %s: %w", flagKubeconfigClusterFile, err)
	}

	data, err := kubeconfig.FetchAndGenerate(ctx, cl)
	if err != nil {
		return fmt.Errorf("generate kubeconfig for cluster %s: %w", cl.Metadata.Name, err)
	}

	if flagKubeconfigExport {
		fmt.Print(string(data))
		return nil
	}

	outPath := flagKubeconfigOut
	if outPath == "" {
		outPath = filepath.Join("artifacts", "kubeconfig")
	}

	if err := kubeconfig.WriteFile(outPath, data); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	fmt.Fprintf(os.Stderr, "✓ Exported kubeconfig to %s\n", outPath)

	if flagKubeconfigWriteGlobal {
		backupPath, err := kubeconfig.SyncGlobal(data)
		if err != nil {
			return fmt.Errorf("sync global kubeconfig: %w", err)
		}
		if backupPath != "" {
			fmt.Fprintf(os.Stderr, "  (backed up previous ~/.kube/config to %s)\n", backupPath)
		}
		fmt.Fprintf(os.Stderr, "✓ Updated ~/.kube/config — you can now run 'kubectl' or 'k9s'\n")
	}

	return nil
}
