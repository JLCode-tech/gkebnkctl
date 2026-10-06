package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/gkebnkctl/internal/forge"
	"github.com/JLCode-tech/gkebnkctl/internal/jumphost"
)

var benchmarkStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check the status of the benchmark environment, jumphost, and forge linkage",
	Long: `status verifies the readiness of the benchmarking pipeline:
  • Jumphost GCE VM provisioning and IAP reachability
  • Installed aiperf version on the jumphost
  • Forge server connectivity and workspace cluster registration
  • VIP and LLM model endpoint availability

Example:
  gkebnkctl benchmark status -f cluster.yaml
  gkebnkctl benchmark status --workspace ai-rig`,
	RunE: runBenchmarkStatus,
}

type benchmarkStatusResult struct {
	Workspace        string `json:"workspace,omitempty"`
	Project          string `json:"project,omitempty"`
	Zone             string `json:"zone,omitempty"`
	Region           string `json:"region,omitempty"`
	InstanceID       string `json:"instance_id,omitempty"`
	SourceIP         string `json:"source_ip,omitempty"`
	VIP              string `json:"vip,omitempty"`
	Model            string `json:"model,omitempty"`
	AiperfStatus     string `json:"aiperf_status"`
	ForgeURL         string `json:"forge_url,omitempty"`
	ForgeLinkStatus  string `json:"forge_link_status"`
	ForgeClusterID   int    `json:"forge_cluster_id,omitempty"`
	PreflightStatus  string `json:"preflight_status"`
	OverallReadiness string `json:"overall_readiness"`
}

func runBenchmarkStatus(cmd *cobra.Command, _ []string) error {
	_ = resolveBenchmarkContext(cmd)

	res := benchmarkStatusResult{
		Workspace:       flagBenchWorkspace,
		Project:         flagBenchProject,
		Zone:            flagBenchZone,
		Region:          flagBenchRegion,
		InstanceID:      flagBenchInstanceID,
		SourceIP:        flagBenchSourceIP,
		VIP:             flagBenchVIP,
		Model:           flagBenchModel,
		ForgeURL:        flagBenchForgeURL,
		ForgeLinkStatus: "unknown",
		AiperfStatus:    "unknown",
		PreflightStatus: "skipped",
	}

	// 1. Check Forge Link
	if flagBenchWorkspace != "" {
		wsDir := fmt.Sprintf(".gkebnkctl/%s", flagBenchWorkspace)
		if link, err := forge.ReadLink(wsDir); err == nil && link != nil {
			res.ForgeLinkStatus = link.Status
			res.ForgeClusterID = link.ClusterID
			if link.ForgeURL != "" && (res.ForgeURL == "" || res.ForgeURL == "http://localhost:8000") {
				res.ForgeURL = link.ForgeURL
			}
		} else {
			res.ForgeLinkStatus = "missing (run gkebnkctl forge register)"
		}
	}

	// 2. Check Jumphost & Aiperf if instance is known
	if flagBenchInstanceID != "" && (flagBenchZone != "" || flagBenchRegion != "") {
		probOpts := jumphost.ProbeOptions{
			Project:    flagBenchProject,
			Zone:       flagBenchZone,
			Region:     flagBenchRegion,
			InstanceID: flagBenchInstanceID,
			SourceIP:   flagBenchSourceIP,
			VIP:        flagBenchVIP,
			User:       jumphost.DefaultSSHUser,
		}
		parentCtx := cmd.Context()
		if parentCtx == nil {
			parentCtx = context.Background()
		}
		ctx, cancel := context.WithTimeout(parentCtx, 45*time.Second)
		defer cancel()

		if err := ensureAiperfFn(ctx, probOpts); err != nil {
			res.AiperfStatus = fmt.Sprintf("failed: %v", err)
		} else {
			res.AiperfStatus = "ready (>=0.10.0)"
		}

		// 3. Preflight check if VIP & Model set
		if flagBenchVIP != "" && flagBenchModel != "" {
			if pfErr := checkServedModelFn(ctx, probOpts, flagBenchModel); pfErr != nil {
				res.PreflightStatus = fmt.Sprintf("failed: %v", pfErr)
			} else {
				res.PreflightStatus = "ready (responding)"
			}
		}
	} else {
		res.AiperfStatus = "unconfigured (no jumphost instance-id found)"
	}

	if res.AiperfStatus == "ready (>=0.10.0)" && (res.PreflightStatus == "ready (responding)" || res.PreflightStatus == "skipped") {
		res.OverallReadiness = "READY"
	} else {
		res.OverallReadiness = "NOT_READY"
	}

	if flagOutput == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "BENCHMARK ENVIRONMENT STATUS")
	fmt.Fprintf(tw, "  Workspace:\t%s\n", res.Workspace)
	if res.Project != "" {
		fmt.Fprintf(tw, "  GCP Project:\t%s\n", res.Project)
	}
	if res.Zone != "" {
		fmt.Fprintf(tw, "  GCP Zone:\t%s\n", res.Zone)
	}
	fmt.Fprintf(tw, "  GCP Region:\t%s\n", res.Region)
	fmt.Fprintf(tw, "  Jumphost Instance:\t%s\n", res.InstanceID)
	fmt.Fprintf(tw, "  Jumphost Source IP:\t%s\n", res.SourceIP)
	fmt.Fprintf(tw, "  Aiperf Engine:\t%s\n", res.AiperfStatus)
	fmt.Fprintf(tw, "  Forge Server:\t%s\n", res.ForgeURL)
	fmt.Fprintf(tw, "  Forge Link:\t%s (cluster_id=%d)\n", res.ForgeLinkStatus, res.ForgeClusterID)
	fmt.Fprintf(tw, "  Target VIP:\t%s\n", res.VIP)
	fmt.Fprintf(tw, "  Target Model:\t%s\n", res.Model)
	fmt.Fprintf(tw, "  Preflight Status:\t%s\n", res.PreflightStatus)
	fmt.Fprintf(tw, "  Overall Readiness:\t%s\n", res.OverallReadiness)
	_ = tw.Flush()

	return nil
}
