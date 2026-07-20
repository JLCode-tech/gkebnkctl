package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

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
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Provision the GKE cluster + BNK stack (requires -f <cluster.yaml>)",
	Long: `gkebnkctl up drives the full provisioning graph: main + shared
dataplane VPCs, the GKE cluster (default/tmm/agent pools), iface discovery,
and the BNK install (namespaces, cert-manager, far-secret, Multus, NADs,
FLO, node-sysctls, CNEInstance, /var/crash patches, License, F5SPKVlan +
StaticRoutes, TMM settle, egress).

  gkebnkctl up -f cluster.yaml [--dry-run]

When --dry-run is set, every phase prints its planned actions and makes zero
GCP mutations.

This scaffold build (Task A) has every phase stubbed — real provisioning
lands in Task B (networks/cluster/discover) and Task C/D (BNK install +
egress). GKEBNKCTL_SKIP_AUTH=1 is honored as a preflight hook so CI can run
--dry-run without live GCP credentials once Phase00 does real ADC checks.`,
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
	return runPhasedDown(cmd.Context(), flagConfig, flagYes, flagDownDryRun)
}

// upPhase describes one entry in the up phase graph. Every phase in Task A
// is a stub — real implementations land in Task B (00/10/20/25 + down),
// Task C (30-42), and Task D (50). The struct/table shape here is the seam
// those tasks wire real functions into (replacing runStubPhase's generic
// print with a call to phases.PhaseNN(...)).
type upPhase struct {
	num  string
	name string
	// needsLiveCluster phases (per docs/SPEC-go-tracer.md §6) render against
	// discovered state that only exists once the cluster is up; dry-run
	// prints "needs live cluster" instead of attempting real work.
	needsLiveCluster bool
	// stateKeys are the discovered-state placeholders this phase sets during
	// dry-run so downstream render calls in later phases don't nil-panic
	// once those phases are real (Task C). Only Phase25 sets these today —
	// see docs/SPEC-go-tracer.md §3 Phase25 state keys.
	stateKeys []string
}

var upPhases = []upPhase{
	{num: "00", name: "preflight"},
	{num: "10", name: "networks"},
	{num: "20", name: "cluster"},
	{num: "25", name: "discover", needsLiveCluster: true, stateKeys: []string{
		"TMM_EXT_IFACE", "TMM_EXT_PCI", "TMM_INT_IFACE", "TMM_INT_PCI",
		"TMM_EXT_ALIAS_CIDR", "TMM_INT_ALIAS_CIDR", "TMM_EXT_SELFIP", "TMM_INT_SELFIP",
		"TMM_POD_CIDR", "TMM_POD_GW", "AGENT_EXT_IFACE", "TMM_NODE_NAME", "AGENT_NODE_NAME",
	}},
	{num: "30", name: "namespaces"},
	{num: "31", name: "cert-manager"},
	{num: "32", name: "issuers"},
	{num: "33", name: "far-secret"},
	{num: "34", name: "multus"},
	{num: "35", name: "nads", needsLiveCluster: true},
	{num: "36", name: "flo"},
	{num: "37", name: "node-sysctls"},
	{num: "38", name: "cneinstance", needsLiveCluster: true},
	{num: "39", name: "var-crash-patches"},
	{num: "40", name: "license"},
	{num: "41", name: "vlans-routes", needsLiveCluster: true},
	{num: "42", name: "tmm-settle"},
	{num: "50", name: "egress", needsLiveCluster: true},
}

// runPhasedUp is the Go-SDK phased provisioning path activated by
// `gkebnkctl up -f <file>`. Task A: every phase is a stub — see upPhases.
//
// When dryRun is true, state.MarkReadOnly() is called immediately after
// Load so any placeholder state phases Set during the run never persists to
// the real state.env on disk (docs/SPEC-go-tracer.md §6).
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

	skipAuth := os.Getenv("GKEBNKCTL_SKIP_AUTH") == "1"
	for _, p := range upPhases {
		runStubPhase(ctx, p, st, skipAuth)
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
	fmt.Fprintf(os.Stderr, "✓ stub plan complete: cluster=%s state=%s (every phase is a stub — see docs/SPEC-go-tracer.md §8 Task B/C/D)\n",
		cl.Metadata.Name, cl.StateDir())
	return nil
}

// runStubPhase prints the `[phase NN] <name> (stub)` banner every Task A
// phase produces, plus annotations for the preflight-auth hook (Phase00) and
// discovery-dependent phases (needsLiveCluster / stateKeys). ctx is accepted
// (and currently unused) so the signature already matches the real
// `phases.PhaseNN(ctx, cl, st, clients, dryRun)` shape Task B/C/D wire in.
func runStubPhase(_ context.Context, p upPhase, st *state.State, skipAuth bool) {
	var notes []string
	if p.num == "00" {
		if skipAuth {
			notes = append(notes, "GKEBNKCTL_SKIP_AUTH=1: ADC check will be skipped once Task B wires the real preflight")
		} else {
			notes = append(notes, "real preflight (ADC + compute/container API-enabled checks) lands in Task B")
		}
	}
	if len(p.stateKeys) > 0 {
		for _, k := range p.stateKeys {
			st.Set(k, "dry-run-"+strings.ToLower(k))
		}
		notes = append(notes, "needs live cluster; set placeholder discovered state")
	} else if p.needsLiveCluster {
		notes = append(notes, "needs live cluster")
	}

	if len(notes) == 0 {
		fmt.Fprintf(os.Stderr, "[phase %s] %s (stub)\n", p.num, p.name)
		return
	}
	fmt.Fprintf(os.Stderr, "[phase %s] %s (stub) — %s\n", p.num, p.name, strings.Join(notes, "; "))
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
// dataplane VPCs (network.external/network.internal) are never referenced.
//
// When dryRun is true, printDownPlan runs and the function returns WITHOUT
// invoking any teardown step or prompting — guaranteeing zero GCP mutations.
func runPhasedDown(_ context.Context, configPath string, yes, dryRun bool) error {
	cl, err := intent.Load(configPath)
	if err != nil {
		return fmt.Errorf("down: %w", err)
	}

	st, err := state.Load(cl.StateDir())
	if err != nil {
		return fmt.Errorf("down: loading state: %w", err)
	}

	if dryRun {
		printDownPlan(os.Stderr, cl)
		return nil
	}

	if !yes && !confirmDestroy(os.Stdin, os.Stderr, cl.Metadata.Name, cl.Metadata.Project, cl.Metadata.Zone) {
		return nil
	}

	fmt.Fprintln(os.Stderr, "[down] cluster (stub) — DeleteCluster lands in Task B (PhaseDownCluster)")
	fmt.Fprintln(os.Stderr, "[down] main-vpc (stub) — firewall -> subnet -> vpc teardown lands in Task B (PhaseDownNetworks)")
	fmt.Fprintf(os.Stderr, "→ shared dataplane VPCs %s/%s are NEVER deleted by teardown.\n",
		cl.Network.External.VPC, cl.Network.Internal.VPC)

	// Task A has no discovered state to clear yet (every phase is a stub);
	// Save is still exercised so the state-dir lifecycle is proven end to
	// end ahead of Task B wiring real Set/Clear calls here.
	if err := st.Save(); err != nil {
		return fmt.Errorf("down: saving state: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ stub teardown complete: cluster=%s (every phase is a stub — see docs/SPEC-go-tracer.md §8 Task B)\n",
		cl.Metadata.Name)
	return nil
}

// printDownPlan renders the destroy plan for `gkebnkctl down --dry-run`. It
// makes NO GCP calls and NO state writes — the read-only half of the down
// flow. Unlike awsbnkctl's printDownPlan (which enumerates resources
// recorded in state), gkebnkctl's plan is derived directly from cluster.yaml
// naming (docs/SPEC-go-tracer.md §4: idempotency here is deterministic
// naming, not label-discovery) — every resource down would touch has a name
// that's a pure function of the intent, so the plan is accurate even before
// Phase25/state ever runs.
func printDownPlan(w io.Writer, cl *intent.Cluster) {
	fmt.Fprintf(w, "→ dry-run: destroy plan for cluster %q (project=%s zone=%s) — no GCP mutations\n",
		cl.Metadata.Name, cl.Metadata.Project, cl.Metadata.Zone)
	fmt.Fprintf(w, "  • delete  GKE cluster                %s\n", cl.Metadata.Name)
	fmt.Fprintf(w, "  • delete  main VPC firewall          %s-allow-internal\n", cl.Network.Main.VPC)
	fmt.Fprintf(w, "  • delete  main VPC subnet            %s\n", cl.Network.Main.Subnet)
	fmt.Fprintf(w, "  • delete  main VPC                   %s\n", cl.Network.Main.VPC)
	fmt.Fprintf(w, "→ shared dataplane VPCs %s/%s are NEVER deleted by teardown.\n",
		cl.Network.External.VPC, cl.Network.Internal.VPC)
	fmt.Fprintln(w, "→ dry-run complete: 4 resource(s) would be destroyed, 0 GCP mutations made")
}
