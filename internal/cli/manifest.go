package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/gkebnkctl/internal/manifest"
)

var (
	flagManifestFarPath string
	flagManifestAll     bool
)

var manifestCmd = &cobra.Command{
	Use:   "manifest",
	Short: "Inspect the BNK release-manifest (Bill of Materials) on repo.f5.com",
}

var manifestProbeCmd = &cobra.Command{
	Use:   "probe [manifest-version]",
	Short: "Inspect release-manifest BOM (FLO, cert-manager, CNE, container images) without a cluster",
	Long: `gkebnkctl manifest probe extracts and verifies the Bill of Materials (BOM)
for an F5 BNK release from repo.f5.com or a local FAR auth archive.

Example:
  gkebnkctl manifest probe
  gkebnkctl manifest probe 2.4.0-3.2882.0 --far keys/f5-far-auth-key.tgz
  gkebnkctl manifest probe --all`,
	Args: cobra.MaximumNArgs(1),
	RunE: runManifestProbe,
}

func init() {
	manifestProbeCmd.Flags().StringVar(&flagManifestFarPath, "far", "", "Path to FAR auth key tgz or manifest directory")
	manifestProbeCmd.Flags().BoolVar(&flagManifestAll, "all", false, "Print every chart and container image in the manifest")

	manifestCmd.AddCommand(manifestProbeCmd)
	rootCmd.AddCommand(manifestCmd)
}

func runManifestProbe(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	targetVersion := manifest.DefaultManifestVersion
	if len(args) > 0 {
		targetVersion = args[0]
	}

	fmt.Printf("Probing F5 BNK Release Manifest:\n")
	fmt.Printf("  Target Version: %s\n", targetVersion)
	if flagManifestFarPath != "" {
		fmt.Printf("  Source Archive: %s\n", flagManifestFarPath)
	}
	fmt.Println()

	m, err := manifest.Probe(ctx, targetVersion, flagManifestFarPath)
	if err != nil {
		return fmt.Errorf("probe release manifest: %w", err)
	}

	m.PrintSummary(os.Stdout)
	if flagManifestAll {
		m.PrintFullBOM(os.Stdout)
	}

	fmt.Println("\n✓ Release manifest inspected and verified.")
	return nil
}
