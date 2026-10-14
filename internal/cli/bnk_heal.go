package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/gkebnkctl/internal/doctor"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/phases"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
)

var (
	flagBnkHealKubeconfig string
	flagBnkHealConfig     string
	flagBnkHealDryRun     bool
	flagBnkHealOnly       []string
)

// bnkHealResult is the JSON document bnk heal prints with -o json.
type bnkHealResult struct {
	Schema  string              `json:"schema"`
	Repairs []phases.HealStatus `json:"repairs"`
}

var bnkHealCmd = &cobra.Command{
	Use:   "heal",
	Short: "Detect and repair the cluster-side plumbing gkebnkctl up and bnk upgrade also fix, on any BNK generation",
	Long: `gkebnkctl bnk heal runs the idempotent repairs that gkebnkctl up and
gkebnkctl bnk upgrade apply, without provisioning or upgrading anything. Each
repair is detected first and fixed only when needed; --dry-run prints what
would change. gkebnkctl doctor --backend k8s shows the same detections as
rows. Works on 2.3 and 2.4 clusters.

Repairs (--only <name> limits the run):
  multus-token-watch         kube-multus-ds writes its kubeconfig once at pod
                             start unless started with --cleanup-config-on-exit;
                             the expired token fails every new pod with
                             "Multus ... Unauthorized" (TMM stays Pending)
  tmm-log-stream             f5-toda-fluentd stdout store, so logs tmm
                             --governance and the Loki collector see TMM lines
  pod-manager                f5-tmm-pod-manager gRPC client cert mount and the
                             cold-start crash loop against kube-proxy
  cwc                        cwc pod stuck in the DNS warm-up crash loop
  dssm-probe                 redis-cli --tls --insecure in the f5-dssm probes
                             (hostname check blocks the replica on cold start)
  controller-endpointslices  -f  ClusterRole so the 2.4 controller can read
                             EndpointSlices (FLO 2.30 omits it)
  tmm-k8s-routes         -f  TMM_K8S_ROUTES=<service CIDR> on the CNEInstance
                             so the TMM pod reaches the service network (dSSM,
                             DNS, the fluentd log forward); on 2.3 detected from
                             the f5-fluentbit sidecar log
  epp-namespaces             eppNamespaces [All] on a 2.4 CNEInstance, so the
                             controller runs the F5 Endpoint Picker (F5EPP)
  test-namespace             namespace gkebnkctl-test, where test --backend k8s
                             runs its iperf3 and dns probe Jobs

Repairs marked -f need cluster.yaml (state.env and the GCP clients); with
--kubeconfig alone they are detected and reported, not fixed.`,
	Example: `  gkebnkctl bnk heal -f clusters/lab/cluster.yaml
  gkebnkctl bnk heal -f clusters/lab/cluster.yaml --dry-run
  gkebnkctl bnk heal --kubeconfig ~/.kube/lab --only tmm-log-stream,pod-manager
  gkebnkctl bnk heal -f clusters/lab/cluster.yaml -o json`,
	RunE: runBnkHeal,
}

func init() {
	bnkHealCmd.Flags().StringVar(&flagBnkHealKubeconfig, "kubeconfig", "", "explicit kubeconfig path (takes precedence over --config; default: $KUBECONFIG -> ~/.kube/config)")
	bnkHealCmd.Flags().StringVarP(&flagBnkHealConfig, "config", "f", "", "path to cluster.yaml; derives kubeconfig from the cluster's state.env KUBECONFIG_PATH (overridden by --kubeconfig) and enables the repairs that need GCP or state")
	bnkHealCmd.Flags().BoolVar(&flagBnkHealDryRun, "dry-run", false, "report what would change without writing to the cluster")
	bnkHealCmd.Flags().StringSliceVar(&flagBnkHealOnly, "only", nil, "comma-separated repair names to run (default: all)")
	bnkCmd.AddCommand(bnkHealCmd)
}

func runBnkHeal(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	known := map[string]bool{}
	for _, r := range phases.Repairs {
		known[r.Name] = true
	}
	for _, n := range flagBnkHealOnly {
		if !known[n] {
			return fmt.Errorf("bnk heal: unknown repair %q (see --help for the list)", n)
		}
	}

	deps := &phases.HealDeps{DryRun: flagBnkHealDryRun, Log: os.Stderr}
	kubeconfigPath := flagBnkHealKubeconfig
	if flagBnkHealConfig != "" {
		cl, err := intent.Load(flagBnkHealConfig)
		if err != nil {
			return fmt.Errorf("bnk heal: loading --config %s: %w", flagBnkHealConfig, err)
		}
		st, err := state.Load(cl.StateDir())
		if err != nil {
			return fmt.Errorf("bnk heal: loading state for %s: %w", cl.Metadata.Name, err)
		}
		if kubeconfigPath == "" {
			derived, err := resolveKubeconfigFromConfig(flagBnkHealConfig)
			if err != nil {
				return fmt.Errorf("bnk heal: %w", err)
			}
			kubeconfigPath = derived
		}
		clients, err := phases.NewClients(ctx, cl.Metadata.Project, cl.Region)
		if err != nil {
			return fmt.Errorf("bnk heal: GCP clients: %w", err)
		}
		deps.Cluster, deps.State, deps.Clients = cl, st, clients
	} else {
		deps.Clients = &phases.Clients{}
	}
	if err := deps.Clients.AttachK8s(kubeconfigPath); err != nil {
		return fmt.Errorf("bnk heal: building kube clients: %w", err)
	}

	statuses := phases.RunHeal(ctx, deps, flagBnkHealOnly)

	if flagOutput == "json" {
		return json.NewEncoder(os.Stdout).Encode(bnkHealResult{Schema: "gkebnkctl.bnk-heal/v2", Repairs: statuses})
	}
	failed := 0
	for _, s := range statuses {
		mark := "ok     "
		switch {
		case s.Error != "":
			mark = "ERROR  "
			failed++
		case s.Changed:
			mark = "FIXED  "
		case s.Skipped:
			mark = "SKIP   "
		case flagBnkHealDryRun && !s.Healthy:
			mark = "WOULD  "
		}
		detail := s.Detail
		if s.Error != "" {
			detail = s.Error
		}
		fmt.Fprintf(os.Stdout, "%s %-26s %s\n", mark, s.Name, detail)
	}
	if failed > 0 {
		return fmt.Errorf("bnk heal: %d repair(s) failed", failed)
	}
	return nil
}

// healDoctorChecks renders the registry detections as doctor rows.
func healDoctorChecks(ctx context.Context, clients *phases.Clients) []doctor.Check {
	var out []doctor.Check
	for _, s := range phases.DetectAll(ctx, &phases.HealDeps{Clients: clients}) {
		c := doctor.Check{Name: s.Title, BackendName: "k8s"}
		switch {
		case s.Error != "":
			c.Status, c.Detail = doctor.StatusWarning, "could not inspect: "+s.Error
		case !s.Healthy:
			c.Status, c.Detail = doctor.StatusWarning, s.Detail
		default:
			c.Status, c.Detail = doctor.StatusOK, s.Detail
		}
		out = append(out, c)
	}
	return out
}
