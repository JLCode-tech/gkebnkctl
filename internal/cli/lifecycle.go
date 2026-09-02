package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/phases"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
)

var (
	// flagConfig is bound to `-f`/`--config` on both upCmd and downCmd.
	flagConfig string

	// flagUpDryRun is bound ONLY to upCmd's --dry-run.
	flagUpDryRun bool

	// flagDownDryRun is bound ONLY to downCmd's --dry-run.
	flagDownDryRun bool

	// flagYes skips the interactive "type 'destroy' to proceed" prompt in
	// `gkebnkctl down`.
	flagYes bool

	// flagKeepDataplane retains per-cluster resources in shared dataplane VPCs
	flagKeepDataplane bool
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Provision the GKE cluster + BNK stack (requires -f <cluster.yaml>)",
	Long: `gkebnkctl up drives the full provisioning graph: main + shared
dataplane VPCs, the GKE cluster (default/tmm/agent pools), iface discovery,
and the BNK install (namespaces, cert-manager, far-secret, Multus, NADs,
FLO, node-sysctls, CNEInstance, /var/crash patches, License, F5SPKVlan +
StaticRoutes, TMM settle, egress, ingress-vxlan-routes).

  gkebnkctl up -f cluster.yaml [--dry-run]

When --dry-run is set, every phase prints its planned actions and makes zero
GCP mutations.

Every phase, 00 (preflight) through 51 (ingress-vxlan-routes), is real — see
internal/gcp/phases. GKEBNKCTL_SKIP_AUTH=1 skips Phase00's ADC +
API-enabled checks entirely (passing a nil *gcp.Clients through every real
phase) so CI can run --dry-run without live GCP credentials.
GKEBNKCTL_VERIFY_EGRESS=1 additionally enables Phase50's best-effort
pkts_in/curl egress verification (default: skipped).`,
	RunE: runUp,
}

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Destroy the GKE cluster + main VPC (requires -f <cluster.yaml>)",
	Long: `gkebnkctl down deletes the GKE cluster and the cluster-owned main
VPC (firewall -> subnet -> VPC, in that order). Unlike the cluster/main VPC,
the shared dataplane VPCs (network.external / network.internal) are NEVER
deleted — they are created-if-missing infrastructure that may be shared
across clusters.

  gkebnkctl down -f cluster.yaml [--yes] [--dry-run]

--dry-run prints the destroy plan and makes zero GCP mutations. Without
--yes, the operator is prompted to type 'destroy' to proceed.`,
	RunE: runDown,
}

func init() {
	upCmd.Flags().StringVarP(&flagConfig, "config", "f", "", "path to cluster.yaml (required)")
	upCmd.Flags().BoolVar(&flagUpDryRun, "dry-run", false, "print the phased plan and exit 0 with no GCP mutations")

	downCmd.Flags().StringVarP(&flagConfig, "config", "f", "", "path to cluster.yaml (required)")
	downCmd.Flags().BoolVar(&flagDownDryRun, "dry-run", false, "print the destroy plan and exit 0 with no GCP mutations")
	downCmd.Flags().BoolVar(&flagYes, "yes", false, "skip the interactive destroy confirmation")
	downCmd.Flags().BoolVar(&flagKeepDataplane, "keep-dataplane", false, "retain per-cluster resources in shared dataplane VPCs")

	rootCmd.AddCommand(upCmd, downCmd)
}

func runUp(cmd *cobra.Command, _ []string) error {
	if flagConfig == "" {
		return errors.New("gkebnkctl up requires -f <cluster.yaml>")
	}
	return runPhasedUp(cmd.Context(), flagConfig, flagUpDryRun)
}

func runDown(cmd *cobra.Command, _ []string) error {
	if flagConfig == "" {
		return errors.New("gkebnkctl down requires -f <cluster.yaml>")
	}
	return runPhasedDown(cmd.Context(), flagConfig, flagYes, flagDownDryRun, flagKeepDataplane)
}

// upPhase describes one entry in the up phase graph. Every phase (00-51)
// dispatches to a real internal/gcp/phases function via realUpPhases (Task
// B: 00/10/20/25; Task C: 30-42; Task D: 50; ingress-vxlan-routes: 51).
type upPhase struct {
	num  string
	name string
}

var upPhases = []upPhase{
	{num: "00", name: "preflight"},
	{num: "10", name: "networks"},
	{num: "15", name: "jumphost"},
	{num: "20", name: "cluster"},
	{num: "25", name: "discover"},
	{num: "30", name: "namespaces"},
	{num: "31", name: "cert-manager"},
	{num: "32", name: "issuers"},
	{num: "33", name: "far-secret"},
	{num: "34", name: "multus"},
	{num: "35", name: "nads"},
	{num: "36", name: "flo"},
	{num: "37", name: "node-sysctls"},
	{num: "38", name: "cneinstance"},
	{num: "39", name: "var-crash-patches"},
	{num: "40", name: "license"},
	{num: "41", name: "vlans-routes"},
	{num: "42", name: "tmm-settle"},
	{num: "50", name: "egress"},
	{num: "51", name: "ingress-vxlan-routes"},
}

// realUpPhases dispatches every phase number in upPhases to its
// internal/gcp/phases function. No phase falls through to runStubPhase
// (Task D landed 50/egress; 51/ingress-vxlan-routes landed after) —
// runStubPhase is kept only as a defensive fallback (see the loop in
// runPhasedUp) in case a future phase is added to upPhases before its
// implementation lands.
var realUpPhases = map[string]func(ctx context.Context, cl *intent.Cluster, st *state.State, clients *gcp.Clients, dryRun bool) error{
	"00": phases.Phase00Preflight,
	"10": phases.Phase10Networks,
	"15": phases.Phase15Jumphost,
	"20": phases.Phase20Cluster,
	"25": phases.Phase25Discover,
	"30": phases.Phase30Namespaces,
	"31": phases.Phase31CertManager,
	"32": phases.Phase32Issuers,
	"33": phases.Phase33FarSecret,
	"34": phases.Phase34Multus,
	"35": phases.Phase35NADs,
	"36": phases.Phase36FLO,
	"37": phases.Phase37NodeSysctls,
	"38": phases.Phase38CNEInstance,
	"39": phases.Phase39VarCrashPatches,
	"40": phases.Phase40License,
	"41": phases.Phase41VlansRoutes,
	"42": phases.Phase42TMMSettle,
	"50": phases.Phase50Egress,
	"51": phases.Phase51IngressVxlanRoutes,
}

// runPhasedUp is the Go-SDK phased provisioning path activated by
// `gkebnkctl up -f <file>`. Every phase 00-51 (see realUpPhases) calls a
// real internal/gcp/phases function.
//
// When dryRun is true, state.MarkReadOnly() is called immediately after
// Load so any placeholder state phases Set during the run never persists to
// the real state.env on disk (docs/SPEC-go-tracer.md §6).
//
// GCP clients are constructed once, up front, and threaded through every
// real phase — UNLESS GKEBNKCTL_SKIP_AUTH=1, in which case a nil
// *gcp.Clients is passed instead (only valid together with --dry-run; every
// real phase either ignores clients in its dry-run branch or, for Phase00,
// checks for nil and skips its own checks — see phases.Phase00Preflight).
func runPhasedUp(ctx context.Context, configPath string, dryRun bool) error {
	cl, err := intent.Load(configPath)
	if err != nil {
		return fmt.Errorf("up: %w", err)
	}

	st, err := state.Load(cl.StateDir())
	if err != nil {
		return fmt.Errorf("up: loading state: %w", err)
	}
	if dryRun {
		st.MarkReadOnly()
		fmt.Fprintln(os.Stderr, "→ dry-run: printing plan, no GCP mutations will be made")
	}

	gcl, err := buildGCPClients(ctx, cl, dryRun)
	if err != nil {
		return fmt.Errorf("up: %w", err)
	}

	for _, p := range upPhases {
		if fn, ok := realUpPhases[p.num]; ok {
			if err := fn(ctx, cl, st, gcl, dryRun); err != nil {
				return fmt.Errorf("up: %w", err)
			}
			continue
		}
		runStubPhase(p)
	}

	if !dryRun {
		if err := st.Save(); err != nil {
			return fmt.Errorf("up: saving state: %w", err)
		}
	}

	if dryRun {
		fmt.Fprintln(os.Stderr, "→ dry-run complete")
		return nil
	}
	fmt.Fprintf(os.Stderr, "✓ up complete: cluster=%s state=%s\n", cl.Metadata.Name, cl.StateDir())
	return nil
}

// buildGCPClients constructs the real *gcp.Clients bundle used by
// realUpPhases/PhaseDown*, UNLESS GKEBNKCTL_SKIP_AUTH=1 — the CI/test hook
// that lets --dry-run run without live GCP credentials (Phase00 and every
// other real phase's dry-run branch tolerate a nil *gcp.Clients). The hook
// is rejected outside --dry-run: a live (mutating) run must have real
// clients, or every real phase would nil-pointer-deref.
func buildGCPClients(ctx context.Context, cl *intent.Cluster, dryRun bool) (*gcp.Clients, error) {
	if os.Getenv("GKEBNKCTL_SKIP_AUTH") != "1" {
		return gcp.NewClients(ctx, cl.Metadata.Project)
	}
	if !dryRun {
		return nil, errors.New("GKEBNKCTL_SKIP_AUTH=1 is only valid together with --dry-run")
	}
	return nil, nil
}

// runStubPhase prints the `[phase NN] <name> (stub)` banner. As of Task D
// every entry in upPhases has a realUpPhases implementation, so this is
// unreachable in practice — kept as a defensive fallback so a future phase
// number added to upPhases without a matching realUpPhases entry fails
// loudly (a stub banner) instead of silently vanishing from the run.
func runStubPhase(p upPhase) {
	fmt.Fprintf(os.Stderr, "[phase %s] %s (stub)\n", p.num, p.name)
}

// confirmDestroy prompts the operator to type 'destroy' and reports whether
// teardown may proceed. Requires an AFFIRMATIVE read: a closed/empty stdin
// (EOF, `</dev/null`, CI pipe) or any input other than exactly "destroy"
// aborts.
func confirmDestroy(in io.Reader, out io.Writer, name, project, zone string) bool {
	fmt.Fprintf(out, "About to DESTROY cluster %q (project=%s zone=%s) and its main VPC.\n", name, project, zone)
	fmt.Fprintln(out, "Shared dataplane VPCs are NEVER deleted by this command.")
	fmt.Fprintln(out, "Type 'destroy' to proceed:")
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() || scanner.Text() != "destroy" {
		fmt.Fprintln(out, "Aborted.")
		return false
	}
	return true
}

// runPhasedDown is the Go-SDK phased destroy path activated by
// `gkebnkctl down -f <file>`. Teardown is deliberately asymmetric (mirrors
// bash bin/90-teardown.sh, see docs/SPEC-go-tracer.md §3 "Down phases"):
// only the cluster and the cluster-OWNED main VPC are deleted. The shared
// dataplane VPCs (network.external/network.internal) are never referenced —
// PhaseDownNetworks does not even accept their names as arguments.
//
// When dryRun is true, printDownPlan runs and the function returns WITHOUT
// invoking any teardown step or prompting — guaranteeing zero GCP mutations.
func runPhasedDown(ctx context.Context, configPath string, yes, dryRun, keepDataplane bool) error {
	cl, err := intent.Load(configPath)
	if err != nil {
		return fmt.Errorf("down: %w", err)
	}

	st, err := state.Load(cl.StateDir())
	if err != nil {
		return fmt.Errorf("down: loading state: %w", err)
	}

	if dryRun {
		printDownPlan(os.Stderr, cl, keepDataplane)
		return nil
	}

	if !yes && !confirmDestroy(os.Stdin, os.Stderr, cl.Metadata.Name, cl.Metadata.Project, cl.Metadata.Zone) {
		return nil
	}

	gcl, err := buildGCPClients(ctx, cl, dryRun)
	if err != nil {
		return fmt.Errorf("down: %w", err)
	}

	if err := phases.PhaseDownJumphost(ctx, cl, st, gcl); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.PhaseDownCluster(ctx, cl, st, gcl); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.PhaseDownNetworks(ctx, cl, st, gcl, keepDataplane); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	fmt.Fprintf(os.Stderr, "→ shared dataplane VPCs %s/%s are NEVER deleted by teardown.\n",
		cl.Network.External.VPC, cl.Network.Internal.VPC)

	// Clear discovered.* state keys, per docs/SPEC-go-tracer.md §3 "Down
	// phases". phases.DiscoverStateKeys (internal/gcp/phases/phase25_discover.go)
	// is the single source of truth for this key set — Phase25Discover writes
	// them, so down clears the same list rather than maintaining a second,
	// independently-drifting copy of the same 13 literals.
	for _, k := range phases.DiscoverStateKeys {
		st.Delete(k)
	}
	if err := st.Save(); err != nil {
		return fmt.Errorf("down: saving state: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ down complete: cluster=%s\n", cl.Metadata.Name)
	return nil
}

// printDownPlan renders the destroy plan for `gkebnkctl down --dry-run`. It
// ignores missing state and prints the maximal plan based on cluster.yaml.
func printDownPlan(w io.Writer, cl *intent.Cluster, keepDataplane bool) {
	fmt.Fprintf(w, "→ dry-run: destroy plan for cluster %q (project=%s zone=%s) — no GCP mutations\n",
		cl.Metadata.Name, cl.Metadata.Project, cl.Metadata.Zone)

	var plan []string
	plan = append(plan, fmt.Sprintf("  • delete  GKE cluster                %s", cl.Metadata.Name))
	if cl.Testing.Jumphost.Enabled {
		plan = append(plan, fmt.Sprintf("  • delete  jumphost VM                %s-jumphost", cl.Metadata.Name))
		plan = append(plan, fmt.Sprintf("  • delete  IAP firewall               %s-allow-iap-ssh", cl.Metadata.Name))
	}
	plan = append(plan, fmt.Sprintf("  • delete  main VPC firewall          %s-allow-internal", cl.Network.Main.VPC))
	plan = append(plan, fmt.Sprintf("  • delete  main VPC subnet            %s", cl.Network.Main.Subnet))
	plan = append(plan, fmt.Sprintf("  • delete  main VPC                   %s", cl.Network.Main.VPC))

	for _, line := range plan {
		fmt.Fprintln(w, line)
	}

	if !keepDataplane {
		fmt.Fprintln(w, "→ in SHARED dataplane VPCs (cluster-owned resources only):")
		fmt.Fprintln(w, "  (firewalls/routers are left in place while other clusters still use this VPC)")
		var sharedPlan []string
		for _, dp := range []intent.DataplaneSpec{cl.Network.External, cl.Network.Internal} {
			sharedPlan = append(sharedPlan, fmt.Sprintf("  • delete  subnet    %s", dp.Subnet))
			sharedPlan = append(sharedPlan, fmt.Sprintf("  • delete  firewall  %s-allow-internal", dp.VPC))
			if cl.Network.NatGatewaysEnabled() {
				sharedPlan = append(sharedPlan, fmt.Sprintf("  • delete  Cloud NAT %s-nat  (BILLABLE while it exists)", dp.VPC))
			}
			sharedPlan = append(sharedPlan, fmt.Sprintf("  • delete  Router    %s-router", dp.VPC))
		}
		for _, line := range sharedPlan {
			fmt.Fprintln(w, line)
		}
		plan = append(plan, sharedPlan...)
	}

	fmt.Fprintf(w, "→ shared dataplane VPCs %s/%s are NEVER deleted by teardown.\n",
		cl.Network.External.VPC, cl.Network.Internal.VPC)
	fmt.Fprintf(w, "→ dry-run complete: %d resource(s) would be destroyed, 0 GCP mutations made\n", len(plan))
}
