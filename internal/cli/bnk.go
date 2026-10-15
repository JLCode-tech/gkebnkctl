package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/client-go/dynamic"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
	"github.com/JLCode-tech/gkebnkctl/pkg/bnk"
)

var (
	flagBnkResyncNamespace    string
	flagBnkResyncAllInNS      bool
	flagBnkResyncGatewayClass string
	flagBnkResyncDryRun       bool
	flagBnkResyncKubeconfig   string
	flagBnkResyncConfig       string
	flagBnkResyncWatch        bool
	flagBnkResyncDebounce     time.Duration
)

var bnkCmd = &cobra.Command{
	Use:   "bnk",
	Short: "BNK runtime helpers (resync pool members, manage data plane, ...)",
	Long: `gkebnkctl bnk provides runtime helpers for BNK (F5 BIG-IP Next for Kubernetes)
cluster management.

Subcommands:
  heal         Detect and repair cluster plumbing (multus-token-watch, tmm-log-stream, pod-manager, cwc, dssm-probe, controller-endpointslices, tmm-k8s-routes, epp-namespaces, test-namespace)
  resync       Force the F5 cne-controller to re-resolve stale TMM pool members
  migrate-2.4  Translate 2.3.x CRs into the 2.4 Infra / GatewaySettings model
  upgrade      In-place FLO + CNEInstance upgrade to BNK 2.4
  mcp-session  Pin MCP sessions: F5BigPersistenceProfile MODEL_CONTEXT_PROTOCOL + NetPolicy`,
}

var bnkResyncCmd = &cobra.Command{
	Use:   "resync [httproute-name]",
	Short: "Force F5 cne-controller to re-resolve stale TMM pool members",
	Long: `gkebnkctl bnk resync forces the F5 cne-controller to re-push pool members
for the targeted HTTPRoute(s) by toggling each backendRef weight +/-1.

This is necessary because the cne-controller only reconciles pool members at
HTTPRoute spec-reconcile time -- it does NOT watch EndpointSlice changes. When
a backend pod is rescheduled or replaced, TMM retains the old pod IP and returns HTTP 500.

Target selection (exactly one required):
  httproute-name -n <namespace>   resync a single HTTPRoute
  --all-in-ns <namespace>         resync every HTTPRoute in the namespace
  --gateway-class <name>          resync HTTPRoutes whose parent Gateway uses
                                  gatewayClassName: <name> (all namespaces)

Watch mode (--watch):
  Stays running and watches EndpointSlices for the Services referenced by
  the targeted HTTPRoutes' backendRefs. When a backend pod is replaced
  (slice change), the affected routes are auto-resynced after a short
  --debounce window -- the VIP self-heals instead of serving HTTP 500
  until an operator notices.

Kubeconfig / Connection selection:
  --config/-f <cluster.yaml>   connect via GCP ADC / cluster metadata, or state kubeconfig
  --kubeconfig <path>          explicit path (takes precedence over --config)
  (neither)                    standard kubectl lookup: $KUBECONFIG -> ~/.kube/config`,
	Args: cobra.MaximumNArgs(1),
	RunE: runBnkResync,
}

func init() {
	bnkResyncCmd.Flags().StringVarP(&flagBnkResyncNamespace, "namespace", "n", "", "namespace of the HTTPRoute (required with a route name or --all-in-ns)")
	bnkResyncCmd.Flags().BoolVar(&flagBnkResyncAllInNS, "all-in-ns", false, "resync every HTTPRoute in the namespace given by -n")
	bnkResyncCmd.Flags().StringVar(&flagBnkResyncGatewayClass, "gateway-class", "", "resync HTTPRoutes whose parent Gateway uses this gatewayClassName (all namespaces)")
	bnkResyncCmd.Flags().BoolVar(&flagBnkResyncDryRun, "dry-run", false, "print what would be patched without making any API writes")
	bnkResyncCmd.Flags().BoolVar(&flagBnkResyncWatch, "watch", false, "stay running and auto-resync targeted HTTPRoutes whenever a referenced backend Service's EndpointSlice changes")
	bnkResyncCmd.Flags().DurationVar(&flagBnkResyncDebounce, "debounce", 2*time.Second, "with --watch: batch window after the first EndpointSlice event before the queued resyncs fire")
	bnkResyncCmd.Flags().StringVar(&flagBnkResyncKubeconfig, "kubeconfig", "", "explicit kubeconfig path (takes precedence over --config; default: $KUBECONFIG -> ~/.kube/config)")
	bnkResyncCmd.Flags().StringVarP(&flagBnkResyncConfig, "config", "f", "", "path to cluster.yaml; connects via GCP ADC or state kubeconfig (overridden by --kubeconfig)")

	bnkCmd.AddCommand(bnkResyncCmd)
	rootCmd.AddCommand(bnkCmd)
}

func runBnkResync(cmd *cobra.Command, args []string) error {
	opts, err := resolveBnkResyncOpts(args)
	if err != nil {
		return err
	}

	dyn, err := buildBnkDynamicClient(cmd.Context())
	if err != nil {
		return fmt.Errorf("building kube client: %w", err)
	}

	if cmd != nil {
		bnk.Stderr = cmd.ErrOrStderr()
	}

	if flagBnkResyncWatch {
		werr := bnk.WatchHTTPRoutes(cmd.Context(), dyn, bnk.WatchOptions{
			ResyncOptions: opts,
			Debounce:      flagBnkResyncDebounce,
		})
		if werr != nil && !errors.Is(werr, context.Canceled) {
			return fmt.Errorf("bnk resync --watch: %w", werr)
		}
		return nil
	}

	result, runErr := bnk.ResyncHTTPRoutes(cmd.Context(), dyn, opts)

	if flagOutput == "json" {
		var out io.Writer = os.Stdout
		if cmd != nil {
			out = cmd.OutOrStdout()
		}
		if err := json.NewEncoder(out).Encode(result); err != nil {
			return fmt.Errorf("encoding json: %w", err)
		}
	}

	if runErr != nil {
		return runErr
	}
	return nil
}

func buildBnkDynamicClient(ctx context.Context) (dynamic.Interface, error) {
	if flagBnkResyncKubeconfig != "" {
		return k8s.BuildDynamicClient(flagBnkResyncKubeconfig)
	}

	if flagBnkResyncConfig != "" {
		cl, err := intent.Load(flagBnkResyncConfig)
		if err != nil {
			return nil, fmt.Errorf("loading cluster.yaml: %w", err)
		}

		candidates := []string{
			filepath.Join(cl.StateDir(), "kubeconfig"),
			filepath.Join("artifacts", "kubeconfig"),
		}
		for _, p := range candidates {
			if _, statErr := os.Stat(p); statErr == nil {
				return k8s.BuildDynamicClient(p)
			}
		}

		clients, err := gcp.NewClients(ctx, cl.Metadata.Project)
		if err != nil {
			return nil, fmt.Errorf("init GCP clients: %w", err)
		}
		if err := clients.AttachK8s(ctx, cl.Metadata.Name, cl.Metadata.Zone); err != nil {
			return nil, fmt.Errorf("attaching k8s client: %w", err)
		}
		if clients.Dynamic == nil {
			return nil, fmt.Errorf("attaching k8s client: Dynamic k8s client is nil for GKE cluster %s in zone %s", cl.Metadata.Name, cl.Metadata.Zone)
		}
		return clients.Dynamic, nil
	}

	return k8s.BuildDynamicClient("")
}

func resolveBnkResyncOpts(args []string) (bnk.ResyncOptions, error) {
	opts := bnk.ResyncOptions{
		DryRun: flagBnkResyncDryRun,
	}

	hasName := len(args) == 1
	hasAllInNS := flagBnkResyncAllInNS
	hasGWClass := flagBnkResyncGatewayClass != ""

	selectors := 0
	if hasName {
		selectors++
	}
	if hasAllInNS {
		selectors++
	}
	if hasGWClass {
		selectors++
	}
	if selectors == 0 {
		return opts, fmt.Errorf("target required: provide a route name, --all-in-ns, or --gateway-class")
	}
	if selectors > 1 {
		return opts, fmt.Errorf("only one target selector may be used at a time (route name, --all-in-ns, or --gateway-class)")
	}

	switch {
	case hasName:
		if flagBnkResyncNamespace == "" {
			return opts, fmt.Errorf("-n / --namespace is required when providing a route name")
		}
		opts.Namespace = flagBnkResyncNamespace
		opts.Name = args[0]

	case hasAllInNS:
		if flagBnkResyncNamespace == "" {
			return opts, fmt.Errorf("-n / --namespace is required with --all-in-ns")
		}
		opts.Namespace = flagBnkResyncNamespace
		opts.AllInNamespace = true

	case hasGWClass:
		opts.GatewayClass = flagBnkResyncGatewayClass
	}

	return opts, nil
}
