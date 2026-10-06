// Package cli wires the cobra command tree for gkebnkctl.
//
// Build-time variables (Version, Commit, BuildDate) are populated via
// -ldflags by the Makefile/goreleaser, mirroring awsbnkctl/internal/cli/root.go.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

// Build metadata, populated via -ldflags at link time.
var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

// Persistent flag values, bound on the root command.
var (
	flagVerbose   bool
	flagQuiet     bool
	flagNoColor   bool
	flagWorkspace string
	flagOutput    string
)

var rootCmd = &cobra.Command{
	Use:   "gkebnkctl",
	Short: "Deploy and validate F5 BIG-IP Next for Kubernetes (BNK) on GKE",
	Long: `gkebnkctl deploys F5 BIG-IP Next for Kubernetes (BNK) onto a GKE
host-device cluster: a cluster-owned main VPC, two shared dataplane VPCs
(external/internal — created if missing, never destroyed), a multi-networking
GKE cluster (DPv2 + host-device NADs), and the full BNK install (cert-manager,
Multus, FLO, CNEInstance, License, F5SPKVlan/StaticRoutes, egress).

  gkebnkctl validate <cluster.yaml>   Parse + validate (no GCP API calls)
  gkebnkctl up   -f <cluster.yaml>    Provision + install
  gkebnkctl down -f <cluster.yaml>    Tear down (cluster + main VPC only)

See docs/SPEC-go-tracer.md for the full phase model + intent schema.`,
	SilenceUsage: true,
}

// Execute runs the root command. Wires SIGINT (Ctrl+C) to a cancellable
// context so long-running operations like `gkebnkctl up` terminate promptly.
//
// Loads $PWD/.env at startup if present — godotenv's Load does NOT overwrite
// existing env vars, so anything already in the shell wins. Lets operators
// keep GOOGLE_APPLICATION_CREDENTIALS, GKEBNKCTL_SKIP_AUTH, etc. in a
// project-scoped file instead of shell profiles.
func Execute() {
	loadDotenv()
	rootCmd.Version = Version
	rootCmd.SetVersionTemplate(fmt.Sprintf(
		"gkebnkctl {{.Version}} (commit %s, built %s)\n", Commit, BuildDate))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "gkebnkctl: %v\n", err)
		os.Exit(1)
	}
}

// loadDotenv reads ./.env if present. Missing file is silent (the common
// case). Parse errors are loud — otherwise a typo in the file would leave
// creds mysteriously unset.
func loadDotenv() {
	if _, err := os.Stat(".env"); err != nil {
		return
	}
	if err := godotenv.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "gkebnkctl: warning: parsing .env: %v\n", err)
	}
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.BoolVarP(&flagVerbose, "verbose", "v", false, "verbose output")
	pf.BoolVarP(&flagQuiet, "quiet", "q", false, "suppress all but errors")
	pf.BoolVar(&flagNoColor, "no-color", false, "disable colored output")
	pf.StringVarP(&flagWorkspace, "workspace", "w", "", "workspace name (default: active workspace)")
	pf.StringVarP(&flagOutput, "output", "o", "text", "output format: text or json")
}

// RootCommand returns the wired-up root cobra command for tooling that needs
// to walk the command tree (e.g. doc generation, tests). Callers MUST NOT
// mutate the returned command — it's the same instance Execute() runs.
func RootCommand() *cobra.Command {
	return rootCmd
}
