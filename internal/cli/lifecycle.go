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

Phases 00 (preflight), 10 (networks), 20 (cluster), 25 (discover) are real —
see internal/gcp/phases. Phases 30-50 (the BNK install + egress) remain
stubs pending Task C/D. GKEBNKCTL_SKIP_AUTH=1 skips Phase00's ADC + API-
enabled checks entirely (passing a nil *gcp.Clients through every real
phase) so CI can run --dry-run without live GCP credentials.`,
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

// upPhase describes one entry in the up phase graph. 00/10/20/25 dispatch to
// real internal/gcp/phases functions (Task B); 30-50 remain stubs pending
// Task C (BNK install) and Task D (egress).
type upPhase struct {
	num  string
	name string
	// needsLiveCluster phases (per docs/SPEC-go-tracer.md §6) render against
	// discovered state that only exists once the cluster is up; the stub
	// path prints "needs live cluster" instead of attempting real work.
	// Only meaningful for the still-stubbed phases (35, 38, 41, 50) — the
	// real Phase25Discover phase prints its own dry-run note directly.
	needsLiveCluster bool
}

var upPhases = []upPhase{
	{num: "00", name: "preflight"},
	{num: "10", name: "networks"},
	{num: "20", name: "cluster"},
	{num: "25", name: "discover"},
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

// realUpPhases dispatches the phase numbers Task B implemented for real to
// their internal/gcp/phases function. Every other phase in upPhases falls
// through to runStubPhase.
var realUpPhases = map[string]func(ctx context.Context, cl *intent.Cluster, st *state.State, clients *gcp.Clients, dryRun bool) error{
	"00": phases.Phase00Preflight,
	"10": phases.Phase10Networks,
	"20": phases.Phase20Cluster,
	"25": phases.Phase25Discover,
}

// runPhasedUp is the Go-SDK phased provisioning path activated by
// `gkebnkctl up -f <file>`. Phases 00/10/20/25 (see realUpPhases) call real
// internal/gcp/phases functions; 30-50 remain stubs pending Task C/D.
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
	fmt.Fprintf(os.Stderr, "✓ up complete: cluster=%s state=%s (phases 30-50 are stubs — see docs/SPEC-go-tracer.md §8 Task C/D)\n",
		cl.Metadata.Name, cl.StateDir())
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

// runStubPhase prints the `[phase NN] <name> (stub)` banner for the phases
// Task C/D still owns (30-50), plus a "needs live cluster" annotation for
// the ones that render against Phase25's discovered state.
func runStubPhase(p upPhase) {
	var notes []string
	if p.needsLiveCluster {
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
// dataplane VPCs (network.external/network.internal) are never referenced —
// PhaseDownNetworks does not even accept their names as arguments.
//
// When dryRun is true, printDownPlan runs and the function returns WITHOUT
// invoking any teardown step or prompting — guaranteeing zero GCP mutations.
func runPhasedDown(ctx context.Context, configPath string, yes, dryRun bool) error {
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

	gcl, err := buildGCPClients(ctx, cl, dryRun)
	if err != nil {
		return fmt.Errorf("down: %w", err)
	}

	if err := phases.PhaseDownCluster(ctx, cl, st, gcl); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.PhaseDownNetworks(ctx, cl, st, gcl); err != nil {
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
		st.Set(k, "")
	}
	if err := st.Save(); err != nil {
		return fmt.Errorf("down: saving state: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ down complete: cluster=%s\n", cl.Metadata.Name)
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
