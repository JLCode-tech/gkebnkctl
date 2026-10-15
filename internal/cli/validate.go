package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/gkebnkctl/internal/intent"
)

var flagValidateFile string

// validateCmd: cheap, GCP-free check that a cluster.yaml parses and passes
// the strict schema (unknown-field rejection, name regex, CIDR parsing,
// jwtPath existence, farSecret enum, ...). Useful in a tight edit loop
// without touching Application Default Credentials.
//
// Pattern borrowed from awsbnkctl's `validate` command (ADR D-008).
var validateCmd = &cobra.Command{
	Use:   "validate [<path>]",
	Short: "Parse and validate a cluster.yaml (no GCP API calls)",
	Long: `gkebnkctl validate reads the given cluster.yaml, applies the strict
schema (unknown fields are errors, metadata.name must match the GKE cluster
name regex, every network CIDR must parse, bnk.jwtPath must exist+be
readable, bnk.farSecret.source must be a recognised value with its
conditionally-required companion field, ...) and exits non-zero on any
validation failure.

It makes no GCP API calls — useful for tightening the edit loop on a
cluster.yaml without Application Default Credentials, or as a CI gate for
cluster.yaml files committed under examples/.

Example:
  gkebnkctl validate -f examples/host-device/cluster.yaml
  gkebnkctl validate examples/host-device/cluster.yaml`,
	Args: cobra.MaximumNArgs(1),
	RunE: runValidate,
}

func runValidate(_ *cobra.Command, args []string) error {
	path := flagValidateFile
	if path == "" && len(args) > 0 {
		path = args[0]
	}
	if path == "" {
		return fmt.Errorf("cluster.yaml path required (pass positional <path> or -f/--file)")
	}
	c, err := intent.Load(path)
	if err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ %s parses cleanly\n", path)
	fmt.Fprintf(os.Stderr, "  apiVersion=%s kind=%s name=%s project=%s zone=%s region=%s\n",
		c.APIVersion, c.Kind, c.Metadata.Name, c.Metadata.Project, c.Metadata.Zone, c.Region)
	fmt.Fprintf(os.Stderr, "  network: main=%s(%s) ext=%s(%s, shared) int=%s(%s, shared)\n",
		c.Network.Main.VPC, c.Network.Main.CIDR,
		c.Network.External.VPC, c.Network.External.CIDR,
		c.Network.Internal.VPC, c.Network.Internal.CIDR)
	fmt.Fprintf(os.Stderr, "  derived: extGwIP=%s intGwIP=%s\n", c.ExtGwIP, c.IntGwIP)
	fmt.Fprintf(os.Stderr, "  bnk: manifestVersion=%s tmmMtu=%s farSecret.source=%s\n",
		c.Bnk.ManifestVersion, c.Bnk.TmmMtu, c.Bnk.FarSecret.Source)
	fmt.Fprintf(os.Stderr, "  stateDir=%s\n", c.StateDir())
	return nil
}

func init() {
	validateCmd.Flags().StringVarP(&flagValidateFile, "file", "f", "", "path to cluster.yaml")
	rootCmd.AddCommand(validateCmd)
}
