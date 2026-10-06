package cli

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/gkebnkctl/internal/forge"
	"github.com/JLCode-tech/gkebnkctl/internal/jumphost"
)

var (
	flagBenchSetupPreflight bool
	flagBenchSetupDaemon    bool
)

var benchmarkSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Prepare the jumphost and register benchmark agent & target in forge",
	Long: `setup prepares the environment for running benchmarks:
  1. Resolves jumphost and forge state from cluster.yaml or the active workspace.
  2. Ensures Python 3.11 and aiperf (>=0.10.0) are installed on the jumphost via IAP SSH.
  3. Registers the jumphost as an SSH AccessMethod in forge (gkebnkctl-jumphost-<id>).
  4. Registers the jumphost as a BenchmarkAgent in forge with 'aiperf' capability.
  5. Registers the cluster Gateway VIP / model as a BenchmarkTarget in forge.
  6. Optionally validates connectivity to the model endpoint via preflight probe.

After setup, run benchmarks with:
  gkebnkctl benchmark run -f cluster.yaml --scenario baseline`,
	RunE: runBenchmarkSetup,
}

func init() {
	benchmarkSetupCmd.Flags().BoolVar(&flagBenchSetupPreflight, "preflight", true,
		"run preflight probe against the served model endpoint to verify end-to-end data path")
	benchmarkSetupCmd.Flags().BoolVar(&flagBenchSetupDaemon, "daemon", false,
		"start the persistent benchmark agent daemon immediately after setup")
}

// ensureAiperfFn is the injectable seam for EnsureAiperf.
var ensureAiperfFn = jumphost.EnsureAiperf

// registerAccessMethodFn is the injectable seam for RegisterJumphostAccessMethod.
var registerAccessMethodFn = forge.RegisterJumphostAccessMethod

func runBenchmarkSetup(cmd *cobra.Command, _ []string) error {
	// Auto-resolve cluster state from cluster.yaml / workspace if not explicitly supplied.
	if err := resolveBenchmarkContext(cmd); err != nil {
		return err
	}

	// Validate minimal requirements for setup.
	switch {
	case flagBenchZone == "" && flagBenchRegion == "":
		return fmt.Errorf("--zone or --region is required (or specify -f cluster.yaml / -w workspace)")
	case flagBenchInstanceID == "":
		return fmt.Errorf("--instance-id is required (or specify -f cluster.yaml / -w workspace with testing.jumphost enabled)")
	}

	probOpts := jumphost.ProbeOptions{
		Project:    flagBenchProject,
		Zone:       flagBenchZone,
		Region:     flagBenchRegion,
		InstanceID: flagBenchInstanceID,
		SourceIP:   flagBenchSourceIP,
		VIP:        flagBenchVIP,
		User:       jumphost.DefaultSSHUser,
	}

	forgeCreds := effectiveForgeCreds()

	agentName := fmt.Sprintf("gkebnkctl-jumphost-%s", flagBenchInstanceID)

	fmt.Fprintf(os.Stderr, "==> Benchmark Setup: workspace=%s zone=%s region=%s jumphost=%s vip=%s\n",
		flagBenchWorkspace, flagBenchZone, flagBenchRegion, flagBenchInstanceID, flagBenchVIP)

	// Step 1: Install / verify aiperf on the jumphost via IAP SSH
	fmt.Fprintf(os.Stderr, "→ 1/4 Ensuring aiperf >= 0.10.0 on jumphost %s...\n", flagBenchInstanceID)
	parentCtx := cmd.Context()
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Minute)
	defer cancel()

	if err := ensureAiperfFn(ctx, probOpts); err != nil {
		return fmt.Errorf("jumphost aiperf setup failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ aiperf is ready on jumphost %s\n", flagBenchInstanceID)

	// Step 2: Register SSH AccessMethod in Forge (best-effort)
	accessMethodID := 0
	if flagBenchRegisterAccessMethod {
		fmt.Fprintf(os.Stderr, "→ 2/4 Registering SSH access method in forge (%s)...\n", flagBenchForgeURL)
		amResp, amErr := registerAccessMethodFn(ctx, forge.AccessMethodOptions{
			RestURL:    flagBenchForgeURL,
			Creds:      forgeCreds,
			Name:       agentName,
			Host:       flagBenchInstanceID,
			Project:    flagBenchProject,
			Zone:       flagBenchZone,
			Region:     flagBenchRegion,
			InstanceID: flagBenchInstanceID,
		})
		if amErr != nil {
			fmt.Fprintf(os.Stderr, "⚠ forge access-method registration failed (non-fatal): %v\n", amErr)
		} else {
			accessMethodID = amResp.ID
			fmt.Fprintf(os.Stderr, "✓ forge access-method registered: id=%d name=%s\n", amResp.ID, amResp.Name)
		}
	} else {
		fmt.Fprintf(os.Stderr, "→ 2/4 Skipping SSH access method registration (--register-access-method=false)\n")
	}

	// Step 3: Register BenchmarkAgent & BenchmarkTarget in Forge
	fmt.Fprintf(os.Stderr, "→ 3/4 Registering forge benchmark graph (agent & target)...\n")
	graph := resolveForgeGraph(ctx, forgeCreds, agentName)

	// Step 4: Optional Preflight Check if VIP & Model are available
	preflightStatus := "SKIPPED"
	if flagBenchSetupPreflight && flagBenchVIP != "" && flagBenchModel != "" {
		fmt.Fprintf(os.Stderr, "→ 4/4 Testing served model endpoint: model=%q vip=%s...\n", flagBenchModel, flagBenchVIP)
		if pfErr := checkServedModelFn(ctx, probOpts, flagBenchModel); pfErr != nil {
			fmt.Fprintf(os.Stderr, "⚠ model preflight probe failed: %v\n", pfErr)
			preflightStatus = fmt.Sprintf("FAILED (%v)", pfErr)
		} else {
			fmt.Fprintf(os.Stderr, "✓ model preflight probe succeeded: %s is responding at %s\n", flagBenchModel, flagBenchVIP)
			preflightStatus = "OK"
		}
	} else {
		fmt.Fprintf(os.Stderr, "→ 4/4 Skipping model preflight (vip or model not set, or --preflight=false)\n")
	}

	// Output summary table
	fmt.Println()
	fmt.Println("BENCHMARK SETUP SUMMARY")
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "  Jumphost Instance:\t%s\n", flagBenchInstanceID)
	fmt.Fprintf(tw, "  Jumphost Source IP:\t%s\n", flagBenchSourceIP)
	if flagBenchProject != "" {
		fmt.Fprintf(tw, "  GCP Project:\t%s\n", flagBenchProject)
	}
	if flagBenchZone != "" {
		fmt.Fprintf(tw, "  GCP Zone:\t%s\n", flagBenchZone)
	}
	fmt.Fprintf(tw, "  GCP Region:\t%s\n", flagBenchRegion)
	fmt.Fprintf(tw, "  Aiperf Status:\tINSTALLED (>=0.10.0)\n")
	fmt.Fprintf(tw, "  Forge Server:\t%s\n", flagBenchForgeURL)
	if accessMethodID > 0 {
		fmt.Fprintf(tw, "  Forge AccessMethod ID:\t%d (%s)\n", accessMethodID, agentName)
	} else {
		fmt.Fprintf(tw, "  Forge AccessMethod ID:\tunregistered\n")
	}
	if graph.agentID > 0 {
		fmt.Fprintf(tw, "  Forge Agent ID:\t%d (%s)\n", graph.agentID, agentName)
	} else {
		fmt.Fprintf(tw, "  Forge Agent ID:\tunregistered\n")
	}
	if graph.targetID > 0 {
		fmt.Fprintf(tw, "  Forge Target ID:\t%d\n", graph.targetID)
	} else {
		fmt.Fprintf(tw, "  Forge Target ID:\tunregistered\n")
	}
	if graph.proxyDeploymentID > 0 {
		fmt.Fprintf(tw, "  Forge Proxy Deployment ID:\t%d\n", graph.proxyDeploymentID)
	}
	if flagBenchVIP != "" {
		fmt.Fprintf(tw, "  Target VIP:\t%s\n", flagBenchVIP)
	}
	if flagBenchModel != "" {
		fmt.Fprintf(tw, "  Target Model:\t%s\n", flagBenchModel)
	}
	fmt.Fprintf(tw, "  Preflight Probe:\t%s\n", preflightStatus)
	_ = tw.Flush()

	fmt.Printf("\n✓ Benchmark environment is configured and ready.\n")

	if flagBenchSetupDaemon {
		fmt.Fprintf(os.Stderr, "\n→ Starting Forge benchmark agent daemon (--daemon)...\n")
		return runBenchmarkDaemon(cmd, nil)
	}
	return nil
}
