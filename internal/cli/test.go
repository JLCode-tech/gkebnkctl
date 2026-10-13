package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/phases"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/test"
)

var (
	flagTestDNSTarget     string
	flagTestDNSType       string
	flagTestDNSServer     string
	flagTestDNSIterations int
	flagTestDNSTimeout    time.Duration
	flagTestDNSEDNSSubnet string
	flagTestDNSJSON       bool

	flagTestConnTarget  string
	flagTestConnProto   string
	flagTestConnCount   int
	flagTestConnTimeout time.Duration
	flagTestConnJSON    bool

	flagTestEgressFile string
)

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Run validation tests (DNS probe, network connectivity, egress verification, data-plane traffic)",
}

var testDNSCmd = &cobra.Command{
	Use:   "dns",
	Short: "Probe DNS resolution and calculate latency distribution (P50/P95/P99) + EDNS client subnet",
	Long: `gkebnkctl test dns issues DNS queries against a target resolver, calculates
latency percentiles (Min, P50, P95, P99, Max), and checks answer sets.

Example:
  gkebnkctl test dns --target f5.com
  gkebnkctl test dns --target f5.com --server 8.8.8.8:53 --iterations 10
  gkebnkctl test dns --target f5.com --edns-subnet 192.0.2.1 --json`,
	RunE: runTestDNS,
}

var testConnCmd = &cobra.Command{
	Use:   "connectivity",
	Short: "Probe TCP/UDP network reachability and round-trip time",
	Long: `gkebnkctl test connectivity tests TCP handshake or UDP reachability
against target endpoints (e.g. VIPs, TMM interfaces, or cloud gateways).

Example:
  gkebnkctl test connectivity --target 8.8.8.8:53
  gkebnkctl test connectivity --target 10.30.0.1:443 --proto tcp --count 5`,
	RunE: runTestConnectivity,
}

var testEgressCmd = &cobra.Command{
	Use:   "egress",
	Short: "Run live Phase 50 egress validation on a deployed GKE BNK cluster",
	Long: `gkebnkctl test egress verifies egress forwarding from agent test pods
through TMM and the external dataplane VPC.

Example:
  gkebnkctl test egress -f cluster.yaml`,
	RunE: runTestEgress,
}

func init() {
	// DNS flags
	testDNSCmd.Flags().StringVar(&flagTestDNSTarget, "target", "google.com", "DNS domain name to query")
	testDNSCmd.Flags().StringVar(&flagTestDNSType, "type", "A", "DNS record type (A, AAAA, CNAME, TXT, MX)")
	testDNSCmd.Flags().StringVar(&flagTestDNSServer, "server", "8.8.8.8:53", "DNS server IP:port")
	testDNSCmd.Flags().IntVar(&flagTestDNSIterations, "iterations", 5, "Number of probe queries to send")
	testDNSCmd.Flags().DurationVar(&flagTestDNSTimeout, "timeout", 2*time.Second, "Per-query timeout")
	testDNSCmd.Flags().StringVar(&flagTestDNSEDNSSubnet, "edns-subnet", "", "Client IP to pass in EDNS Client Subnet option")
	testDNSCmd.Flags().BoolVar(&flagTestDNSJSON, "json", false, "Output results in JSON format")

	// Connectivity flags
	testConnCmd.Flags().StringVar(&flagTestConnTarget, "target", "", "Target host:port (required)")
	testConnCmd.Flags().StringVar(&flagTestConnProto, "proto", "tcp", "Protocol (tcp or udp)")
	testConnCmd.Flags().IntVar(&flagTestConnCount, "count", 3, "Number of probes to send")
	testConnCmd.Flags().DurationVar(&flagTestConnTimeout, "timeout", 3*time.Second, "Per-probe timeout")
	testConnCmd.Flags().BoolVar(&flagTestConnJSON, "json", false, "Output results in JSON format")
	_ = testConnCmd.MarkFlagRequired("target")

	// Egress flags
	testEgressCmd.Flags().StringVarP(&flagTestEgressFile, "file", "f", "cluster.yaml", "Path to cluster.yaml")

	testCmd.AddCommand(testDNSCmd)
	testCmd.AddCommand(testConnCmd)
	testCmd.AddCommand(testEgressCmd)
	rootCmd.AddCommand(testCmd)
}

func runTestDNS(cmd *cobra.Command, _ []string) error {
	ctx := context.Background()
	if cmd != nil && cmd.Context() != nil {
		ctx = cmd.Context()
	}
	cfg := test.DNSProbeConfig{
		Target:     flagTestDNSTarget,
		Type:       flagTestDNSType,
		Server:     flagTestDNSServer,
		Iterations: flagTestDNSIterations,
		Timeout:    flagTestDNSTimeout,
		EDNSSubnet: flagTestDNSEDNSSubnet,
	}

	res, err := test.RunDNS(ctx, cfg)
	if err != nil && res == nil {
		return fmt.Errorf("dns probe failed: %w", err)
	}

	if flagTestDNSJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}

	fmt.Printf("DNS Probe Results for %s (%s):\n", res.Target, res.QueryType)
	fmt.Printf("  Server:       %s\n", res.Server)
	fmt.Printf("  Status:       %s (Authoritative: %t)\n", res.Rcode, res.Authoritative)
	fmt.Printf("  Iterations:   %d\n", res.Iterations)
	fmt.Printf("  Latency RTT:  Min: %.2fms | P50: %.2fms | P95: %.2fms | P99: %.2fms | Max: %.2fms\n",
		res.LatencyMs.Min, res.LatencyMs.P50, res.LatencyMs.P95, res.LatencyMs.P99, res.LatencyMs.Max)

	if len(res.Answers) > 0 {
		fmt.Printf("\nAnswers (%d):\n", len(res.Answers))
		for _, ans := range res.Answers {
			fmt.Printf("  • %-5s (ttl %ds): %s\n", ans.Type, ans.TTL, ans.RData)
		}
	}

	if res.Error != "" {
		fmt.Printf("\nWarnings/Errors: %s\n", res.Error)
	}

	return nil
}

func runTestConnectivity(cmd *cobra.Command, _ []string) error {
	ctx := context.Background()
	if cmd != nil && cmd.Context() != nil {
		ctx = cmd.Context()
	}
	cfg := test.ConnectivityConfig{
		Target:  flagTestConnTarget,
		Proto:   flagTestConnProto,
		Count:   flagTestConnCount,
		Timeout: flagTestConnTimeout,
	}

	res, err := test.RunConnectivity(ctx, cfg)
	if err != nil {
		return fmt.Errorf("connectivity test failed: %w", err)
	}

	if flagTestConnJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}

	fmt.Printf("Connectivity Results to %s (%s):\n", res.Target, res.Protocol)
	fmt.Printf("  Probes Sent:  %d (Successful: %d, Loss: %.1f%%)\n",
		res.Total, res.Successful, res.LossPercent)
	if res.Successful > 0 {
		fmt.Printf("  Latency RTT:  Min: %.2fms | Avg: %.2fms | Max: %.2fms\n",
			res.MinRTTMs, res.AvgRTTMs, res.MaxRTTMs)
	}
	if len(res.Errors) > 0 {
		fmt.Printf("  Errors:       %v\n", res.Errors)
	}

	return nil
}

func runTestEgress(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	cl, err := intent.Load(flagTestEgressFile)
	if err != nil {
		return fmt.Errorf("load %s: %w", flagTestEgressFile, err)
	}

	clients, err := gcp.NewClients(ctx, cl.Metadata.Project)
	if err != nil {
		return fmt.Errorf("init GCP clients: %w", err)
	}

	if err := clients.AttachK8s(ctx, cl.Metadata.Name, cl.Metadata.Zone); err != nil {
		return fmt.Errorf("attach k8s client: %w", err)
	}

	fmt.Printf("Running Phase 50 Egress verification against cluster %s...\n", cl.Metadata.Name)
	return phases.Phase50Egress(ctx, cl, nil, clients, false)
}
