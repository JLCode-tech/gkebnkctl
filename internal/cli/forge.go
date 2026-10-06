package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	container "cloud.google.com/go/container/apiv1"
	containerpb "cloud.google.com/go/container/apiv1/containerpb"

	"github.com/JLCode-tech/gkebnkctl/internal/forge"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/kubeconfig"
)

var (
	flagForgeMCPURL      string
	flagForgeProject     string
	flagForgeScan        bool
	flagForgePurge       bool
	flagForgeKubeconf    string
	flagForgeClusterNm   string
	flagForgeConfig      string
	flagForgeCleanupWS   string // --workspace for `forge cleanup`
	flagForgeCleanupDry  bool   // --dry-run for `forge cleanup`
	flagForgeCleanupURL  string // --forge-rest-url for `forge cleanup`
	flagForgeCleanupUser string // --forge-user for `forge cleanup`
	flagForgeCleanupPass string // --forge-pass for `forge cleanup`
)

var forgeCmd = &cobra.Command{
	Use:   "forge",
	Short: "Register the current workspace with a BNK-Forge instance over MCP",
	Long: `forge wires this workspace into a running BNK-Forge instance so
forge can manage and observe the GCP infra + GKE + BNK deployment
gkebnkctl provisioned.

Talks to forge's MCP server (Streamable HTTP / JSON-RPC) at
http://localhost:8081/mcp/ by default. Override with --forge-mcp-url or
the GKEBNKCTL_FORGE_MCP_URL environment variable.`,
}

var forgeRegisterCmd = &cobra.Command{
	Use:   "register",
	Short: "Register the workspace's GKE cluster with forge (idempotent)",
	Long: `Creates a forge project (gkebnkctl-<workspace>) and registers
the workspace's GKE cluster under it. Idempotent — re-running on an
already-registered workspace reuses the existing project/cluster.

By default, gkebnkctl generates a kubeconfig and hands it to forge.
Pass --kubeconfig <path> to upload a pre-existing kubeconfig file instead.

Use --scan to also trigger forge's scan_cluster + bnk_health after
registration as a smoke check.`,
	RunE: runForgeRegister,
}

var forgeStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show this workspace's forge registration state",
	RunE:  runForgeStatus,
}

var forgeUnregisterCmd = &cobra.Command{
	Use:   "unregister",
	Short: "Remove this workspace's forge registration",
	Long: `Deletes the cluster registration from forge and removes the
local forge_link.json. Pass --purge to also delete the forge project
(use with caution — irreversible from gkebnkctl's side).`,
	RunE: runForgeUnregister,
}

var forgeCleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Delete all gkebnkctl benchmark artifacts from forge for a workspace (full purge)",
	Long: `forge cleanup is the explicit full-purge counterpart to the automatic
down-path cleanup. Where 'gkebnkctl down' deletes cluster-scoped records
(targets + their proxy deployments) and the exact-name BenchmarkAgent + SSH
access-method, this command also deletes:

  • BenchmarkAgent records whose name starts with "gkebnkctl-"
  • BenchmarkConfig records whose name starts with "gkebnkctl-"

Configs and agents carry no cluster identity and are shared/reusable across
clusters, so deleting them is an intentional operator action rather than an
automatic side-effect of 'down'.

Run/result rows are never deleted — forge nulls their FKs via SET NULL so all
historical data is preserved.

All operations are idempotent (404 = already gone = success).

Example:
  gkebnkctl forge cleanup --workspace ai-rig
  gkebnkctl forge cleanup --workspace ai-rig --dry-run`,
	RunE: runForgeCleanup,
}

func runForgeCleanup(cmd *cobra.Command, _ []string) error {
	if flagForgeCleanupWS == "" {
		return fmt.Errorf("--workspace is required: name the workspace whose artifacts should be purged (e.g. ai-rig)")
	}

	// Resolve cluster_id from forge_link.json in the workspace state dir.
	wsDir := fmt.Sprintf(".gkebnkctl/%s", flagForgeCleanupWS)
	link, err := forge.ReadLink(wsDir)
	if err != nil {
		return fmt.Errorf("forge cleanup: could not read forge_link.json for workspace %q: %w\n(hint: ensure the workspace has been registered with forge, or run 'gkebnkctl forge register')", flagForgeCleanupWS, err)
	}
	if link.ClusterID <= 0 {
		return fmt.Errorf("forge cleanup: forge_link.json for workspace %q has no cluster_id (status=%q) — workspace may not be fully registered", flagForgeCleanupWS, link.Status)
	}

	restURL := flagForgeCleanupURL
	creds := forge.RestCreds{
		Username: flagForgeCleanupUser,
		Password: flagForgeCleanupPass,
	}

	fmt.Fprintf(os.Stderr, "→ forge cleanup: workspace=%s cluster_id=%d forge=%s\n",
		flagForgeCleanupWS, link.ClusterID, restURL)

	if flagForgeCleanupDry {
		fmt.Fprintf(os.Stderr, "  dry-run: would delete:\n")
		fmt.Fprintf(os.Stderr, "    • targets with cluster_id=%d (and their proxy deployments)\n", link.ClusterID)
		fmt.Fprintf(os.Stderr, "    • BenchmarkAgent records with name prefix \"gkebnkctl-\"\n")
		fmt.Fprintf(os.Stderr, "    • BenchmarkConfig records with name prefix \"gkebnkctl-\"\n")
		fmt.Fprintf(os.Stderr, "  (run without --dry-run to apply)\n")
		return nil
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
	defer cancel()

	if err := forge.DeleteAllClusterBenchmarkArtifacts(ctx, restURL, creds, link.ClusterID); err != nil {
		return fmt.Errorf("forge cleanup: %w", err)
	}

	fmt.Printf("✓ forge cleanup complete: workspace=%s cluster_id=%d\n", flagForgeCleanupWS, link.ClusterID)
	fmt.Printf("  run/result rows preserved (FK nulled by forge)\n")
	return nil
}

func init() {
	forgeCmd.PersistentFlags().StringVar(&flagForgeMCPURL, "forge-mcp-url", "",
		"forge MCP endpoint (default $GKEBNKCTL_FORGE_MCP_URL, fallback "+forge.DefaultMCPURL+")")
	forgeCmd.PersistentFlags().StringVarP(&flagForgeConfig, "config", "f", "",
		"path to cluster.yaml (intent mode); when set, forge targets the cluster.yaml's metadata.name/region and stores the link in the cluster's state dir instead of the legacy workspace")

	forgeRegisterCmd.Flags().StringVar(&flagForgeProject, "project-name", "",
		"forge project name (default gkebnkctl-<workspace>)")
	forgeRegisterCmd.Flags().StringVar(&flagForgeClusterNm, "cluster-name", "",
		"GKE cluster name to register (default: workspace cluster.name)")
	forgeRegisterCmd.Flags().StringVar(&flagForgeKubeconf, "kubeconfig", "",
		"path to a pre-existing kubeconfig (default: generate in-process)")
	forgeRegisterCmd.Flags().BoolVar(&flagForgeScan, "scan", false,
		"after register, call scan_cluster + bnk_health as a smoke check")

	forgeUnregisterCmd.Flags().BoolVar(&flagForgePurge, "purge", false,
		"also delete the forge project (default: cluster only; project preserved)")

	forgeCleanupCmd.Flags().StringVar(&flagForgeCleanupWS, "workspace", "",
		"workspace name (e.g. ai-rig); reads .gkebnkctl/<workspace>/forge_link.json for cluster_id [required]")
	forgeCleanupCmd.Flags().BoolVar(&flagForgeCleanupDry, "dry-run", false,
		"print what would be deleted without issuing any DELETE requests")
	forgeCleanupCmd.Flags().StringVar(&flagForgeCleanupURL, "forge-rest-url", "http://localhost:8000",
		"forge REST base URL")
	forgeCleanupCmd.Flags().StringVar(&flagForgeCleanupUser, "forge-user", "",
		"forge username (default: admin)")
	forgeCleanupCmd.Flags().StringVar(&flagForgeCleanupPass, "forge-pass", "",
		"forge password (default: changeme)")

	forgeCmd.AddCommand(forgeRegisterCmd, forgeStatusCmd, forgeUnregisterCmd, forgeCleanupCmd)
	rootCmd.AddCommand(forgeCmd)
}

// forgeTarget carries the resolved identity for any forge sub-command.
type forgeTarget struct {
	clusterName string // GKE cluster name
	project     string // GCP project
	region      string // GCP region
	zone        string // GCP zone
	linkDir     string // directory where forge_link.json lives
	label       string // human-facing identity label (workspace name or cluster name)
	mcpURL      string // preferred MCP URL from intent ("" → fall through to flag/env/default)
	cluster     *intent.Cluster
}

// resolveForgeTarget returns a forgeTarget from either:
//   - intent mode (--config <cluster.yaml>), or
//   - workspace mode (.gkebnkctl/<ws>/...).
func resolveForgeTarget() (*forgeTarget, error) {
	var cl *intent.Cluster
	var wsDir string
	var label string

	if flagForgeConfig != "" {
		loaded, err := intent.Load(flagForgeConfig)
		if err != nil {
			return nil, fmt.Errorf("loading --config %s: %w", flagForgeConfig, err)
		}
		cl = loaded
		wsDir = cl.StateDir()
		label = cl.Metadata.Name
	} else {
		wsName := flagWorkspace
		if wsName == "" {
			if _, err := os.Stat("cluster.yaml"); err == nil {
				if loaded, err := intent.Load("cluster.yaml"); err == nil {
					cl = loaded
					wsDir = cl.StateDir()
					label = cl.Metadata.Name
				}
			}
		} else {
			wsDir = fmt.Sprintf(".gkebnkctl/%s", wsName)
			label = wsName
			cfgPath := filepath.Join(wsDir, "cluster.yaml")
			if _, err := os.Stat(cfgPath); err == nil {
				if loaded, err := intent.Load(cfgPath); err == nil {
					cl = loaded
				}
			}
		}
	}

	if cl != nil {
		if cl.Metadata.Name == "" {
			return nil, fmt.Errorf("cluster.yaml: metadata.name is required")
		}
		project := cl.Metadata.Project
		if project == "" {
			project = os.Getenv("GCP_PROJECT")
		}
		region := cl.Region
		zone := cl.Metadata.Zone
		if region == "" && zone != "" {
			region = zoneToRegion(zone)
		}
		t := &forgeTarget{
			clusterName: cl.Metadata.Name,
			project:     project,
			region:      region,
			zone:        zone,
			linkDir:     wsDir,
			label:       label,
			cluster:     cl,
		}
		if cl.Forge != nil && cl.Forge.MCPURL != "" {
			t.mcpURL = cl.Forge.MCPURL
		}
		return t, nil
	}

	if wsDir != "" {
		st, _ := state.Load(wsDir)
		project := os.Getenv("GCP_PROJECT")
		region := os.Getenv("GCP_REGION")
		zone := os.Getenv("GCP_ZONE")
		if st != nil {
			if p := st.Get("PROJECT_ID"); p != "" {
				project = p
			}
			if r := st.Get("REGION"); r != "" {
				region = r
			}
			if z := st.Get("ZONE"); z != "" {
				zone = z
			}
		}
		return &forgeTarget{
			clusterName: label,
			project:     project,
			region:      region,
			zone:        zone,
			linkDir:     wsDir,
			label:       label,
		}, nil
	}

	return nil, errors.New("no cluster configuration found (specify -f cluster.yaml or -w workspace)")
}

func pickMCPURL(intentMCP string) string {
	if flagForgeMCPURL != "" {
		return flagForgeMCPURL
	}
	return intentMCP
}

func runForgeRegister(cmd *cobra.Command, _ []string) error {
	t, err := resolveForgeTarget()
	if err != nil {
		return err
	}

	clusterName := t.clusterName
	if flagForgeClusterNm != "" {
		clusterName = flagForgeClusterNm
	}
	if clusterName == "" {
		return errors.New("no cluster name — specify in cluster.yaml or pass --cluster-name")
	}

	// 1) build kubeconfig: either read --kubeconfig <path> or generate
	kubeconfigYAML, err := buildKubeconfig(cmd.Context(), t.cluster, clusterName, t.project, t.zone)
	if err != nil {
		return err
	}

	// 2) talk to forge over MCP
	fc := forge.NewClient(pickMCPURL(t.mcpURL))
	if !flagQuiet {
		fmt.Fprintf(os.Stderr, "→ forge MCP: %s\n", fc.URL())
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
	defer cancel()

	res, err := forge.Register(ctx, fc, forge.RegisterRequest{
		WorkspaceName:    t.label,
		WorkspaceDir:     t.linkDir,
		ProjectName:      flagForgeProject,
		ClusterName:      clusterName,
		Region:           t.region,
		GCPProject:       t.project,
		Kubeconfig:       kubeconfigYAML,
		PostRegisterScan: flagForgeScan,
	})
	if err != nil {
		return err
	}

	// 3) report
	fmt.Printf("✓ registered with forge\n")
	fmt.Printf("  project:   %s (id=%d)\n", res.Link.ProjectName, res.Link.ProjectID)
	fmt.Printf("  cluster:   %s (id=%d)\n", res.Link.ClusterName, res.Link.ClusterID)
	fmt.Printf("  link:      %s\n", forge.LinkPath(t.linkDir))
	fmt.Printf("  mcp:       %s\n", res.ForgeURL)
	if flagForgeScan {
		fmt.Printf("  scan:      %s\n", oneLine(res.ScanOutput))
		fmt.Printf("  health:    %s\n", oneLine(res.HealthCheck))
	}
	return nil
}

func runForgeStatus(cmd *cobra.Command, _ []string) error {
	t, err := resolveForgeTarget()
	if err != nil {
		return err
	}

	fc := forge.NewClient(pickMCPURL(t.mcpURL))
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()

	st, err := forge.Status(ctx, fc, t.linkDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "workspace %q has no forge link — run `gkebnkctl forge register`\n", t.label)
			return nil
		}
		return err
	}

	fmt.Printf("workspace:    %s\n", st.Link.Workspace)
	fmt.Printf("project:      %s (id=%d)\n", st.Link.ProjectName, st.Link.ProjectID)
	fmt.Printf("cluster:      %s (id=%d)\n", st.Link.ClusterName, st.Link.ClusterID)
	fmt.Printf("registered:   %s\n", st.Link.RegisteredAt.Format(time.RFC3339))
	fmt.Printf("forge mcp:    %s\n", st.Link.ForgeMCPURL)
	fmt.Printf("reachable:    %v\n", st.Reachable)
	if st.ForgeVersion != "" {
		fmt.Printf("forge version: %s\n", oneLine(st.ForgeVersion))
	}
	if st.Err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", st.Err)
	}
	return nil
}

func runForgeUnregister(cmd *cobra.Command, _ []string) error {
	t, err := resolveForgeTarget()
	if err != nil {
		return err
	}

	fc := forge.NewClient(pickMCPURL(t.mcpURL))
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()

	if err := forge.Unregister(ctx, fc, t.linkDir, flagForgePurge); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "workspace %q has no forge link — nothing to do\n", t.label)
			return nil
		}
		return err
	}
	fmt.Printf("✓ workspace unregistered from forge%s\n",
		map[bool]string{true: " (project purged)", false: ""}[flagForgePurge])
	return nil
}

func buildKubeconfig(ctx context.Context, cl *intent.Cluster, clusterName, project, zone string) ([]byte, error) {
	if flagForgeKubeconf != "" {
		b, err := os.ReadFile(flagForgeKubeconf) // #nosec G304 -- explicit operator-supplied --kubeconfig path
		if err != nil {
			return nil, fmt.Errorf("read --kubeconfig %s: %w", flagForgeKubeconf, err)
		}
		return b, nil
	}

	if cl != nil {
		return kubeconfig.FetchAndGenerate(ctx, cl)
	}

	clusterClient, err := container.NewClusterManagerClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("init GCP container client: %w", err)
	}
	defer clusterClient.Close()

	clusterFullName := fmt.Sprintf("projects/%s/locations/%s/clusters/%s", project, zone, clusterName)
	resp, err := clusterClient.GetCluster(ctx, &containerpb.GetClusterRequest{Name: clusterFullName})
	if err != nil {
		return nil, fmt.Errorf("fetch GKE cluster %s: %w", clusterName, err)
	}
	caCert := resp.GetMasterAuth().GetClusterCaCertificate()
	endpoint := resp.GetEndpoint()
	return kubeconfig.Generate(clusterName, project, zone, endpoint, caCert)
}

func oneLine(s string) string {
	const max = 160
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' || r == '\r' {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if len(out) > max {
		cut := max
		for cut > 0 && (out[cut]&0xC0) == 0x80 {
			cut--
		}
		return out[:cut] + "…"
	}
	return out
}

func zoneToRegion(zone string) string {
	parts := strings.Split(zone, "-")
	if len(parts) >= 2 {
		return strings.Join(parts[:len(parts)-1], "-")
	}
	return zone
}
