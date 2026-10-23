package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/topology"
)

var (
	flagTopologyConfig     string
	flagTopologyFormat     string
	flagTopologyOutput     string
	flagTopologyStatic     bool
	flagTopologyKubeconfig string
)

var topologyCmd = &cobra.Command{
	Use:   "topology",
	Short: "Render the cluster data-path topology (3 VPCs, node pools, TMM VLANs, jumphost, gateways)",
	Long: `gkebnkctl topology renders the whole-cluster GKE data-path topology in one view:

  Main/External/Internal VPCs + subnets + secondary ranges, GKE node pools
  (default/tmm/agent), TMM external/internal VLANs + self-IPs + PCI addresses,
  VXLAN egress overlay, jumphost, GatewayClass + VIP range.

Works OFFLINE from cluster.yaml (intent) + state.env, and optionally enriches
from live Kubernetes when reachable (unless --static is set). Fields not yet
provisioned show as "(not provisioned)".

Formats:
  ascii    (default) ASCII box-drawing diagram
  mermaid  Mermaid graph TD for embedding in docs or forge
  dot      Graphviz DOT digraph
  json     Structured JSON model`,
	RunE: runTopologyCmd,
}

func init() {
	topologyCmd.Flags().StringVarP(&flagTopologyConfig, "config", "f", "", "path to cluster.yaml (required)")
	topologyCmd.Flags().StringVar(&flagTopologyFormat, "format", "ascii", "output format: ascii, mermaid, dot, or json")
	topologyCmd.Flags().StringVarP(&flagTopologyOutput, "output", "o", "", "output format alias: ascii, mermaid, dot, or json")
	topologyCmd.Flags().BoolVar(&flagTopologyStatic, "static", false, "skip live Kubernetes API enrichment and render from intent + state.env only")
	topologyCmd.Flags().StringVar(&flagTopologyKubeconfig, "kubeconfig", "", "explicit kubeconfig path for live K8s enrichment")
	_ = topologyCmd.MarkFlagRequired("config")
	rootCmd.AddCommand(topologyCmd)
}

func runTopologyCmd(cmd *cobra.Command, _ []string) error {
	format := flagTopologyFormat
	if flagTopologyOutput != "" {
		format = flagTopologyOutput
	}
	switch format {
	case "ascii", "mermaid", "dot", "json":
		// valid
	default:
		return fmt.Errorf("unknown format %q: must be ascii, mermaid, dot, or json", format)
	}

	cl, err := intent.Load(flagTopologyConfig)
	if err != nil {
		return fmt.Errorf("loading cluster.yaml: %w", err)
	}

	// Load state — a missing or empty state dir is expected pre-provisioning.
	// Proceed with an empty state so topology renders from intent alone.
	st, err := state.Load(cl.StateDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "[topology] warning: could not load state.env (%v); rendering from intent only\n", err)
		st, _ = state.Load(os.TempDir())
	}

	m := topology.Build(cl, st)

	if !flagTopologyStatic {
		kubeconfig, _ := resolveKubeconfigFlags(flagTopologyKubeconfig, flagTopologyConfig)
		if kubeconfig != "" {
			if dyn, cs, kerr := ScanClients(kubeconfig); kerr == nil {
				ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
				topology.EnrichFromK8s(ctx, &m, cs, dyn)
				cancel()
			}
		}
	}

	var out string
	switch format {
	case "mermaid":
		out = topology.RenderMermaid(m)
	case "dot":
		out = topology.RenderDOT(m)
	case "json":
		out, err = topology.RenderJSON(m)
		if err != nil {
			return err
		}
	default:
		out = topology.RenderASCII(m)
	}

	fmt.Fprintln(cmd.OutOrStdout(), out)
	return nil
}
