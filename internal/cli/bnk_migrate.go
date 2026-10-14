package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp/phases"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/bnkscan"
	k8smanifests "github.com/JLCode-tech/gkebnkctl/internal/k8s/manifests"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/migrate"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/render"
	"github.com/JLCode-tech/gkebnkctl/internal/manifest"
)

// Flags of bnk migrate-2.4.
var (
	flagBnkMigrateDryRun       bool
	flagBnkMigrateApply        bool
	flagBnkMigrateKubeconfig   string
	flagBnkMigrateConfig       string
	flagBnkMigrateNamespace    string
	flagBnkMigrateInfraName    string
	flagBnkMigrateGatewayClass string
	flagBnkMigrateSingleSelfIP bool
	flagBnkMigrateWait         time.Duration
)

// Flags of bnk upgrade.
var (
	flagBnkUpgradeConfig      string
	flagBnkUpgradeKubeconfig  string
	flagBnkUpgradeManifest    string
	flagBnkUpgradeFLO         string
	flagBnkUpgradeNamespace   string
	flagBnkUpgradeInstance    string
	flagBnkUpgradeDryRun      bool
	flagBnkUpgradeTimeout     time.Duration
	flagBnkUpgradeServiceCIDR string
)

// Client constructors, replaced by tests.
var (
	bnkMigrateDynamicClient = k8s.BuildDynamicClient
	bnkMigrateApplier       = func(kubeconfigPath string) (migrate.Applier, error) {
		cfg, err := k8s.BuildRESTConfig(kubeconfigPath)
		if err != nil {
			return nil, err
		}
		return migrate.NewDynamicApplier(cfg)
	}
	bnkUpgradeIdentity = func(ctx context.Context, cl *intent.Cluster, st *state.State, kubeconfigPath string) error {
		return nil
	}
	bnkUpgradeDeps = func(kubeconfigPath, farKeyB64 string) (migrate.UpgradeDeps, error) {
		helm, err := phases.NewHelmInstaller(kubeconfigPath, farKeyB64)
		if err != nil {
			return migrate.UpgradeDeps{}, fmt.Errorf("helm: %w", err)
		}
		cfg, err := k8s.BuildRESTConfig(kubeconfigPath)
		if err != nil {
			return migrate.UpgradeDeps{}, err
		}
		dyn, err := dynamic.NewForConfig(cfg)
		if err != nil {
			return migrate.UpgradeDeps{}, fmt.Errorf("dynamic client: %w", err)
		}
		applier, err := bnkMigrateApplier(kubeconfigPath)
		if err != nil {
			return migrate.UpgradeDeps{}, fmt.Errorf("applier: %w", err)
		}
		cs, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			return migrate.UpgradeDeps{}, fmt.Errorf("clientset: %w", err)
		}
		return migrate.UpgradeDeps{Helm: helm, Dyn: dyn, K8s: cs, Applier: applier}, nil
	}
)

var bnkMigrateCmd = &cobra.Command{
	Use:   "migrate-2.4",
	Short: "Translate a BNK 2.3.x configuration into the 2.4 Infra / GatewaySettings model",
	Long: `gkebnkctl bnk migrate-2.4 reads the legacy CRs of a running BNK 2.3.x cluster
and generates their BNK 2.4 equivalents.

Inspection (namespace -n, default f5-cne-system, plus every tenant namespace):
  F5SPKVlan, F5SPKStaticRoute, Vrf, Vxlan     -> one Infra CR
  F5SPKEgress, F5SPKSnatpool                  -> Infra egressDefaults, GatewaySettings
                                                 egressConfigs, EgressGateway, SecPolicy
  F5BnkGateway + Gateway                      -> GatewaySettings per Gateway and a
                                                 spec.infrastructure.parametersRef patch
  BNKSecPolicy, BNKNetPolicy                  -> SecPolicy, NetPolicy (gateway.k8s.f5.com)

--dry-run prints the generated manifests on stdout (YAML, or JSON with -o json)
and the report on stderr; nothing is written. --apply server-side-applies them
in order and waits for the Infra CR to report Programmed=True. Without either
flag the command behaves like --dry-run.

The legacy CRs stay in place: with USE_GATEWAY_SETTINGS the 2.4 controller
ignores them, and they are what a rollback needs. Run gkebnkctl bnk upgrade
first (or after, on a cluster that already has the 2.4 CRDs), then apply.

--config/-f cluster.yaml lets the command derive the kubeconfig from state and
fill the availability zone of every IPAM pool from network data path.`,
	Args: cobra.NoArgs,
	RunE: runBnkMigrate,
}

var bnkUpgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "In-place upgrade of FLO and the CNEInstance to BNK 2.4",
	Long: `gkebnkctl bnk upgrade moves a running BNK install to 2.4 in place:

  1. helm upgrade f5-lifecycle-operator to the FLO chart paired with the target
     manifest (v2.30.0-0.5.2 for 2.4.0), with values rendered from cluster.yaml
  2. patch the CNEInstance: spec.manifestVersion and the cne-controller env
     USE_GATEWAY_SETTINGS=true (MAX_ACTIVE_TMM_REPLICAS and the TMM ZEBOS_STATE=legacy
     the 2.4 examples set are added when absent; every other entry is kept)
  3. wait for the Infra CRD, the CNEInstance CNEControllerAvailable and
     F5TmmAvailable conditions, the f5-cne-controller rollout carrying the flag,
     and Ready TMM pods

--config/-f is required: the FAR key logs the Helm SDK in to repo.f5.com and the
JWT goes into the FLO values. --manifest-version accepts the documented
"2.4.0-3.3175.0+0.0.380" and rewrites it to the registry tag "2.4.0".
--dry-run prints each step without changing anything.

Follow with gkebnkctl bnk migrate-2.4 --apply to move the network and tenant
configuration to Infra / GatewaySettings.`,
	Args: cobra.NoArgs,
	RunE: runBnkUpgrade,
}

func init() {
	f := bnkMigrateCmd.Flags()
	f.BoolVar(&flagBnkMigrateDryRun, "dry-run", false, "print the generated 2.4 manifests without modifying the cluster (default when --apply is absent)")
	f.BoolVar(&flagBnkMigrateApply, "apply", false, "server-side apply the generated manifests and wait for Infra Programmed=True")
	f.StringVar(&flagBnkMigrateKubeconfig, "kubeconfig", "", "explicit kubeconfig path (takes precedence over --config; default: $KUBECONFIG -> ~/.kube/config)")
	f.StringVarP(&flagBnkMigrateConfig, "config", "f", "", "path to cluster.yaml; derives the kubeconfig from state.env and the pool availability zones")
	f.StringVarP(&flagBnkMigrateNamespace, "namespace", "n", migrate.DefaultInstanceNamespace, "CNE namespace holding the CNEInstance and the underlay CRs")
	f.StringVar(&flagBnkMigrateInfraName, "infra-name", migrate.DefaultInfraName, "name of the generated Infra CR")
	f.StringVar(&flagBnkMigrateGatewayClass, "gateway-class", "", "BNK GatewayClass for the EgressGateways (default: the class of the BNK Gateways)")
	f.BoolVar(&flagBnkMigrateSingleSelfIP, "single-self-ip", false, "keep each self-IP pool at the 2.3 address instead of the /27 block around it")
	f.DurationVar(&flagBnkMigrateWait, "wait", 5*time.Minute, "with --apply: how long to wait for Infra Programmed=True (0 = do not wait)")

	u := bnkUpgradeCmd.Flags()
	u.StringVarP(&flagBnkUpgradeConfig, "config", "f", "", "path to cluster.yaml (required: FAR key, JWT, kubeconfig)")
	u.StringVar(&flagBnkUpgradeKubeconfig, "kubeconfig", "", "explicit kubeconfig path (takes precedence over the one in state.env)")
	u.StringVar(&flagBnkUpgradeManifest, "manifest-version", manifest.DefaultManifestVersion, "target CNEInstance manifestVersion")
	u.StringVar(&flagBnkUpgradeFLO, "flo-version", "", "f5-lifecycle-operator chart version (default: the one paired with --manifest-version)")
	u.StringVarP(&flagBnkUpgradeNamespace, "namespace", "n", migrate.DefaultInstanceNamespace, "CNE namespace of the CNEInstance")
	u.StringVar(&flagBnkUpgradeInstance, "instance", "", "CNEInstance name (default: the only one in the namespace)")
	u.BoolVar(&flagBnkUpgradeDryRun, "dry-run", false, "print the steps without changing anything")
	u.DurationVar(&flagBnkUpgradeTimeout, "timeout", 20*time.Minute, "bound for each rollout wait")
	u.StringVar(&flagBnkUpgradeServiceCIDR, "service-cidr", "", "Kubernetes service range TMM keeps reachable over eth0 (TMM env TMM_K8S_ROUTES); default: SERVICE_CIDR from state.env, else cluster's svcsCidr")

	bnkCmd.AddCommand(bnkMigrateCmd)
	bnkCmd.AddCommand(bnkUpgradeCmd)
}

func runBnkMigrate(cmd *cobra.Command, _ []string) error {
	if flagBnkMigrateDryRun && flagBnkMigrateApply {
		return fmt.Errorf("--dry-run and --apply are mutually exclusive")
	}
	apply := flagBnkMigrateApply
	if !apply && !flagBnkMigrateDryRun {
		fmt.Fprintln(os.Stderr, "[migrate-2.4] neither --dry-run nor --apply given: printing the plan only")
	}

	kubeconfigPath, err := resolveKubeconfigFlags(flagBnkMigrateKubeconfig, flagBnkMigrateConfig)
	if err != nil {
		return fmt.Errorf("bnk migrate-2.4: %w", err)
	}
	var cl *intent.Cluster
	if flagBnkMigrateConfig != "" {
		if cl, err = intent.Load(flagBnkMigrateConfig); err != nil {
			return fmt.Errorf("bnk migrate-2.4: loading --config: %w", err)
		}
	}

	dyn, err := bnkMigrateDynamicClient(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("building kube client: %w", err)
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	inv, err := migrate.Inspect(ctx, dyn, flagBnkMigrateNamespace)
	if err != nil {
		return fmt.Errorf("bnk migrate-2.4: inspect: %w", err)
	}
	opts := migrate.Options{
		InfraName:        flagBnkMigrateInfraName,
		GatewayClassName: flagBnkMigrateGatewayClass,
		SingleSelfIP:     flagBnkMigrateSingleSelfIP,
		ZoneByNetwork:    zonesFromCluster(cl, inv),
	}
	plan, err := migrate.Translate(inv, opts)
	if err != nil {
		return fmt.Errorf("bnk migrate-2.4: %w", err)
	}
	migrate.WriteReport(os.Stderr, inv, plan)

	if !apply {
		if flagOutput == "json" {
			return migrate.WriteJSON(cmd.OutOrStdout(), inv, plan)
		}
		return migrate.WriteYAML(cmd.OutOrStdout(), plan)
	}
	if plan.Empty() {
		fmt.Fprintln(os.Stderr, "[migrate-2.4] nothing to apply")
		return nil
	}
	applier, err := bnkMigrateApplier(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("bnk migrate-2.4: %w", err)
	}
	if err := migrate.ApplyPlan(ctx, applier, plan, os.Stderr); err != nil {
		return fmt.Errorf("bnk migrate-2.4: %w", err)
	}
	if plan.Infra != nil && flagBnkMigrateWait > 0 {
		fmt.Fprintf(os.Stderr, "[migrate-2.4] waiting for Infra %s/%s Programmed=True (up to %s)\n", inv.Namespace, plan.Infra.GetName(), flagBnkMigrateWait)
		if err := migrate.WaitConditionTrue(ctx, dyn, migrate.InfraGVR, inv.Namespace, plan.Infra.GetName(), "Programmed", flagBnkMigrateWait, 5*time.Second); err != nil {
			return fmt.Errorf("bnk migrate-2.4: %w", err)
		}
		fmt.Fprintf(os.Stderr, "[migrate-2.4] Infra %s/%s Programmed=True\n", inv.Namespace, plan.Infra.GetName())
	}
	fmt.Fprintf(os.Stderr, "[migrate-2.4] applied %d object(s); the 2.3 CRs were left in place\n", len(plan.Objects()))
	if flagOutput == "json" {
		return migrate.WriteJSON(cmd.OutOrStdout(), inv, plan)
	}
	return nil
}

// zonesFromCluster maps every F5SPKVlan of the inventory to the GCP zone
// of the cluster.
func zonesFromCluster(cl *intent.Cluster, inv *migrate.Inventory) map[string]string {
	if cl == nil || inv == nil || cl.Metadata.Zone == "" {
		return nil
	}
	out := make(map[string]string)
	for _, v := range inv.Vlans {
		out[v.GetName()] = cl.Metadata.Zone
	}
	return out
}

func runBnkUpgrade(cmd *cobra.Command, _ []string) error {
	if flagBnkUpgradeConfig == "" {
		return fmt.Errorf("--config/-f cluster.yaml is required (FAR key and JWT for the FLO release)")
	}
	cl, err := intent.Load(flagBnkUpgradeConfig)
	if err != nil {
		return fmt.Errorf("bnk upgrade: loading --config: %w", err)
	}
	if cl.Bnk.FARArchive == "" {
		return fmt.Errorf("bnk upgrade: cluster.yaml has no bnk: block")
	}
	kubeconfigPath, err := resolveKubeconfigFlags(flagBnkUpgradeKubeconfig, flagBnkUpgradeConfig)
	if err != nil {
		return fmt.Errorf("bnk upgrade: %w", err)
	}
	farKey, values, err := floUpgradeInputs(cl)
	if err != nil {
		return fmt.Errorf("bnk upgrade: %w", err)
	}
	deps, err := bnkUpgradeDeps(kubeconfigPath, farKey)
	if err != nil {
		return fmt.Errorf("bnk upgrade: %w", err)
	}
	rbacTmpl, err := k8smanifests.FS.ReadFile(phases.CNEControllerRBACYAMLPath)
	if err != nil {
		return fmt.Errorf("bnk upgrade: reading cne-controller rbac template: %w", err)
	}
	rbacYAML, err := render.RenderCNEControllerRBACNamespace(rbacTmpl, cl, flagBnkUpgradeNamespace)
	if err != nil {
		return fmt.Errorf("bnk upgrade: %w", err)
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	serviceCIDR, cidrNote := bnkUpgradeServiceCIDR(ctx, cl, flagBnkUpgradeServiceCIDR)
	if cidrNote != "" {
		fmt.Fprintf(os.Stderr, "[upgrade] %s\n", cidrNote)
	}
	res, err := migrate.Upgrade(ctx, deps, migrate.UpgradeOptions{
		Namespace:       flagBnkUpgradeNamespace,
		CNEInstanceName: flagBnkUpgradeInstance,
		ManifestVersion: flagBnkUpgradeManifest,
		FLOChart:        flagBnkUpgradeFLO,
		Values:          values,
		DryRun:          flagBnkUpgradeDryRun,
		Timeout:         flagBnkUpgradeTimeout,
		ControllerRBAC:  rbacYAML,
		ServiceCIDR:     serviceCIDR,
		Log:             os.Stderr,
	})
	if err == nil && !flagBnkUpgradeDryRun {
		// Step 4: Identity re-bind if applicable.
		if st, lerr := state.Load(cl.StateDir()); lerr == nil {
			if ierr := bnkUpgradeIdentity(ctx, cl, st, kubeconfigPath); ierr != nil {
				err = fmt.Errorf("identity re-bind: %w", ierr)
			}
		}
		// Step 5: TMM log stream.
		if err == nil && deps.K8s != nil {
			switch changed, lerr := k8s.EnableTMMLogStream(ctx, deps.K8s, flagBnkUpgradeTimeout); {
			case lerr != nil:
				fmt.Fprintf(os.Stderr, "[upgrade] step 5: TMM log stream skipped (%v)\n", lerr)
			case changed:
				res.LogStreamEnabled = true
				fmt.Fprintf(os.Stderr, "[upgrade] step 5: enabled the stdout store in %s/%s and bounced the fluentd pod (TMM log stream for `gkebnkctl logs tmm`)\n", k8s.TMMLogNamespace, k8s.TMMLogCustomConfigMap)
			default:
				fmt.Fprintln(os.Stderr, "[upgrade] step 5: TMM log stream already on")
			}
		}
		res.Readiness = upgradeReadiness(ctx, deps, flagBnkUpgradeNamespace, os.Stderr)
	}
	if err == nil && flagBnkUpgradeDryRun {
		fmt.Fprintln(os.Stderr, "[upgrade] step 4: dry-run: would re-bind the CNE controller service account when configured")
	}
	if res != nil && flagOutput == "json" {
		if encErr := json.NewEncoder(cmd.OutOrStdout()).Encode(res); encErr != nil && err == nil {
			err = encErr
		}
	}
	if err != nil {
		return fmt.Errorf("bnk upgrade: %w", err)
	}
	return nil
}

// upgradeReadiness runs the shared bnkscan readiness check after an upgrade
// and logs the summary, the problems and the migrate-2.4 advice. Never fails
// the command.
func upgradeReadiness(ctx context.Context, deps migrate.UpgradeDeps, ns string, log io.Writer) *bnkscan.Readiness {
	if deps.Dyn == nil {
		return nil
	}
	rd, _, err := bnkscan.CheckReadiness(ctx, deps.Dyn, deps.K8s, bnkscan.Options{ControllerNamespace: ns})
	if err != nil {
		fmt.Fprintf(log, "[upgrade] readiness: %v\n", err)
		return nil
	}
	fmt.Fprintf(log, "[upgrade] %s\n", rd.Summary())
	for _, p := range rd.Problems {
		fmt.Fprintf(log, "[upgrade]   %s\n", p)
	}
	if advice := rd.MigrationAdvice(); advice != "" {
		fmt.Fprintf(log, "[upgrade] next: %s\n", advice)
	}
	return &rd
}

// floUpgradeInputs reads the FAR key and renders the FLO Helm values from
// cluster.yaml the same way phase 14 does.
func floUpgradeInputs(cl *intent.Cluster) (farKeyB64 string, values map[string]any, err error) {
	farPath := resolveClusterPath(cl.SourcePath, cl.Bnk.FARArchive)
	jwtPath := resolveClusterPath(cl.SourcePath, cl.Bnk.JWT)
	farData, err := os.ReadFile(farPath) // #nosec G304 -- operator-supplied path via cluster.yaml
	if err != nil {
		return "", nil, fmt.Errorf("reading FAR archive %s: %w", farPath, err)
	}
	jwtData, err := os.ReadFile(jwtPath) // #nosec G304 -- operator-supplied path via cluster.yaml
	if err != nil {
		return "", nil, fmt.Errorf("reading JWT %s: %w", jwtPath, err)
	}
	tmpl, err := k8smanifests.FS.ReadFile(phases.FLOValuesTemplatePath)
	if err != nil {
		return "", nil, fmt.Errorf("reading embedded flo-values template: %w", err)
	}
	rendered, err := render.RenderFLOValues(tmpl, cl, strings.TrimSpace(string(jwtData)))
	if err != nil {
		return "", nil, fmt.Errorf("rendering flo-values: %w", err)
	}
	if err := yaml.Unmarshal(rendered, &values); err != nil {
		return "", nil, fmt.Errorf("parsing rendered flo-values: %w", err)
	}
	farPayload := phases.ExtractFARPayload(farData)
	return strings.TrimSpace(string(farPayload)), values, nil
}

// resolveClusterPath joins a relative cluster.yaml path to the file's directory.
func resolveClusterPath(sourcePath, path string) string {
	if path == "" || filepath.IsAbs(path) || sourcePath == "" {
		return path
	}
	return filepath.Join(filepath.Dir(sourcePath), path)
}

// bnkUpgradeServiceCIDR resolves the Kubernetes service range step 2 writes
// into the TMM env (render.TMMK8sRoutesEnv): the --service-cidr flag, else
// state.env or cluster's svcsCidr.
var bnkUpgradeServiceCIDR = func(ctx context.Context, cl *intent.Cluster, flag string) (cidr, note string) {
	if flag != "" {
		return flag, ""
	}
	if st, err := state.Load(cl.StateDir()); err == nil && st.Get(phases.ServiceCIDRStateKey) != "" {
		return st.Get(phases.ServiceCIDRStateKey), ""
	}
	if cl.Network.Main.SvcsCIDR != "" {
		return cl.Network.Main.SvcsCIDR, ""
	}
	return "", ""
}
