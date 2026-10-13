package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/jumphost"
)

var (
	flagTestTrafficConfig     string
	flagTestTrafficVIP        string
	flagTestTrafficIterations int
	flagTestTrafficTimeout    time.Duration
	flagTestTrafficHost       string
	flagTestTrafficInstance   string
	flagTestTrafficDryRun     bool
	flagTestTrafficJSON       bool
)

// Seam override for unit testing without live GCE IAP/SSH connectivity.
var runCurlProbesFn = jumphost.RunCurlProbes

// TrafficTestResult records the aggregate and individual outcome of data-plane probes.
type TrafficTestResult struct {
	Cluster    string                 `json:"cluster"`
	VIP        string                 `json:"vip"`
	Host       string                 `json:"host,omitempty"`
	Instance   string                 `json:"instance"`
	Total      int                    `json:"total"`
	Successful int                    `json:"successful"`
	Failed     int                    `json:"failed"`
	MinRTTMs   float64                `json:"minRttMs"`
	AvgRTTMs   float64                `json:"avgRttMs"`
	MaxRTTMs   float64                `json:"maxRttMs"`
	Probes     []jumphost.ProbeResult `json:"probes"`
}

var testTrafficCmd = &cobra.Command{
	Use:   "traffic",
	Short: "Drive HTTP traffic through TMM from the test jumphost",
	Long: `gkebnkctl test traffic drives HTTP curl probes from the GCE test jumphost
through the BNK external dataplane VIP to verify end-to-end data-plane routing.

Resolves Gateway VIP and Jumphost instance from flags, cluster.yaml, and state.env.
Exit 0 when every probe returns HTTP 200, non-zero on any failure.

Example:
  gkebnkctl test traffic -f cluster.yaml
  gkebnkctl test traffic -f cluster.yaml --dry-run
  gkebnkctl test traffic -f cluster.yaml --vip 10.0.10.100 --iterations 10 --json
  gkebnkctl test traffic --host myapp.example.com`,
	RunE: runTestTraffic,
}

func init() {
	testTrafficCmd.Flags().StringVarP(&flagTestTrafficConfig, "config", "f", "cluster.yaml", "path to cluster.yaml")
	testTrafficCmd.Flags().StringVar(&flagTestTrafficVIP, "vip", "", "Gateway VIP to probe (optional; defaults to derived CIDR.100 or state)")
	testTrafficCmd.Flags().IntVar(&flagTestTrafficIterations, "iterations", 5, "number of curl probe iterations")
	testTrafficCmd.Flags().DurationVar(&flagTestTrafficTimeout, "timeout", 10*time.Second, "per-probe timeout")
	testTrafficCmd.Flags().StringVar(&flagTestTrafficHost, "host", "", "HTTP Host header (optional)")
	testTrafficCmd.Flags().StringVar(&flagTestTrafficInstance, "instance", "", "jumphost GCE instance override (optional)")
	testTrafficCmd.Flags().BoolVar(&flagTestTrafficDryRun, "dry-run", false, "resolve and print plan without executing network calls")
	testTrafficCmd.Flags().BoolVar(&flagTestTrafficJSON, "json", false, "output results in JSON format")

	testCmd.AddCommand(testTrafficCmd)
}

func runTestTraffic(cmd *cobra.Command, _ []string) error {
	ctx := context.Background()
	if cmd != nil && cmd.Context() != nil {
		ctx = cmd.Context()
	}

	cl, err := intent.Load(flagTestTrafficConfig)
	if err != nil {
		return fmt.Errorf("loading %s: %w", flagTestTrafficConfig, err)
	}

	st, err := state.Load(cl.StateDir())
	if err != nil {
		return fmt.Errorf("loading state from %s: %w", cl.StateDir(), err)
	}

	vip, err := resolveTrafficVIP(cl, st, flagTestTrafficVIP)
	if err != nil {
		return fmt.Errorf("unable to resolve VIP: specify --vip or configure external network CIDR in %s", flagTestTrafficConfig)
	}

	// Resolve Jumphost: --instance flag > JUMPHOST_INSTANCE_NAME > JUMPHOST_INSTANCE_ID > dry-run fallback
	instance := flagTestTrafficInstance
	if instance == "" {
		instance = st.Get("JUMPHOST_INSTANCE_NAME")
	}
	if instance == "" {
		instance = st.Get("JUMPHOST_INSTANCE_ID")
	}
	if instance == "" && flagTestTrafficDryRun {
		instance = cl.Metadata.Name + "-jumphost"
	}
	if instance == "" {
		return fmt.Errorf("jumphost instance not found in state (JUMPHOST_INSTANCE_NAME / JUMPHOST_INSTANCE_ID missing); specify --instance or deploy jumphost with phase 15")
	}

	// Resolve SourceIP: JUMPHOST_BNK_EXT_NIC_IP > JUMPHOST_IP
	sourceIP := st.Get("JUMPHOST_BNK_EXT_NIC_IP")
	if sourceIP == "" {
		sourceIP = st.Get("JUMPHOST_IP")
	}

	// Resolve Project/Zone/Region
	project := cl.Metadata.Project
	if project == "" {
		project = st.Get("PROJECT_ID")
	}
	zone := cl.Metadata.Zone
	if zone == "" {
		zone = st.Get("ZONE")
	}
	region := cl.Region
	if region == "" && zone != "" {
		region = zoneToRegion(zone)
	}
	if region == "" {
		region = st.Get("REGION")
	}

	timeoutSecs := int(flagTestTrafficTimeout.Seconds())
	if timeoutSecs <= 0 {
		timeoutSecs = 10
	}
	curlCmd := jumphost.BuildCurlCmd(sourceIP, vip, flagTestTrafficHost, timeoutSecs)

	if flagTestTrafficDryRun {
		errOut := io.Writer(os.Stderr)
		if cmd != nil {
			errOut = cmd.ErrOrStderr()
		}
		hostStr := flagTestTrafficHost
		if hostStr == "" {
			hostStr = "(none)"
		}
		sourceIPStr := sourceIP
		if sourceIPStr == "" {
			sourceIPStr = "(none)"
		}

		fmt.Fprintf(errOut, "→ Traffic test dry-run plan:\n")
		fmt.Fprintf(errOut, "  Cluster:    %s\n", cl.Metadata.Name)
		fmt.Fprintf(errOut, "  Project:    %s\n", project)
		fmt.Fprintf(errOut, "  Zone:       %s\n", zone)
		fmt.Fprintf(errOut, "  Region:     %s\n", region)
		fmt.Fprintf(errOut, "  Jumphost:   %s\n", instance)
		fmt.Fprintf(errOut, "  VIP:        %s\n", vip)
		fmt.Fprintf(errOut, "  Host:       %s\n", hostStr)
		fmt.Fprintf(errOut, "  Source IP:  %s\n", sourceIPStr)
		fmt.Fprintf(errOut, "  Iterations: %d\n", flagTestTrafficIterations)
		fmt.Fprintf(errOut, "  Timeout:    %s\n", flagTestTrafficTimeout)
		fmt.Fprintf(errOut, "  Command:    %s\n", curlCmd)
		return nil
	}

	opts := jumphost.ProbeOptions{
		Project:    project,
		Zone:       zone,
		Region:     region,
		InstanceID: instance,
		SourceIP:   sourceIP,
		VIP:        vip,
		Iterations: flagTestTrafficIterations,
		Timeout:    flagTestTrafficTimeout,
		User:       jumphost.DefaultSSHUser,
		Hostname:   flagTestTrafficHost,
	}

	probes, err := runCurlProbesFn(ctx, opts)
	if err != nil && len(probes) == 0 {
		return fmt.Errorf("running curl probes: %w", err)
	}
	if len(probes) == 0 {
		return fmt.Errorf("traffic test failed: no probes were executed")
	}

	var successful, failed int
	var rttTotal float64
	var minRTT, maxRTT float64
	firstSuccess := true

	for _, p := range probes {
		if p.HTTPCode == 200 && p.Err == "" {
			successful++
			rttMs := p.Seconds * 1000.0
			rttTotal += rttMs
			if firstSuccess || rttMs < minRTT {
				minRTT = rttMs
			}
			if firstSuccess || rttMs > maxRTT {
				maxRTT = rttMs
			}
			firstSuccess = false
		} else {
			failed++
		}
	}

	var avgRTT float64
	if successful > 0 {
		avgRTT = rttTotal / float64(successful)
	}

	res := TrafficTestResult{
		Cluster:    cl.Metadata.Name,
		VIP:        vip,
		Host:       flagTestTrafficHost,
		Instance:   instance,
		Total:      len(probes),
		Successful: successful,
		Failed:     failed,
		MinRTTMs:   minRTT,
		AvgRTTMs:   avgRTT,
		MaxRTTMs:   maxRTT,
		Probes:     probes,
	}

	isJSON := flagTestTrafficJSON || flagOutput == "json"
	out := io.Writer(os.Stdout)
	if cmd != nil {
		out = cmd.OutOrStdout()
	}

	if isJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if encErr := enc.Encode(res); encErr != nil {
			return fmt.Errorf("encoding json: %w", encErr)
		}
	} else {
		fmt.Fprintf(out, "Traffic Test Results for %s:\n", res.Cluster)
		fmt.Fprintf(out, "  VIP:          %s\n", res.VIP)
		if res.Host != "" {
			fmt.Fprintf(out, "  Host:         %s\n", res.Host)
		}
		fmt.Fprintf(out, "  Jumphost:     %s\n", res.Instance)
		fmt.Fprintf(out, "  Probes Sent:  %d (Successful: %d, Failed: %d)\n", res.Total, res.Successful, res.Failed)
		if res.Successful > 0 {
			fmt.Fprintf(out, "  Latency RTT:  Min: %.2fms | Avg: %.2fms | Max: %.2fms\n", res.MinRTTMs, res.AvgRTTMs, res.MaxRTTMs)
		}
		fmt.Fprintln(out)
		fmt.Fprintf(out, "  %-11s %-11s %-12s %s\n", "ITERATION", "HTTP_CODE", "RTT", "STATUS")
		for _, p := range res.Probes {
			status := "OK"
			if p.HTTPCode != 200 || p.Err != "" {
				if p.Err != "" {
					status = fmt.Sprintf("FAIL (%s)", p.Err)
				} else {
					status = fmt.Sprintf("FAIL (HTTP %d)", p.HTTPCode)
				}
			}
			fmt.Fprintf(out, "  %-11d %-11d %-12.2fms %s\n", p.Iteration, p.HTTPCode, p.Seconds*1000.0, status)
		}
	}

	if failed > 0 {
		return fmt.Errorf("traffic test failed: %d/%d probes failed", failed, len(probes))
	}

	return nil
}

// resolveTrafficVIP resolves the VIP target according to the strict precedence:
// 1. Explicit --vip flag
// 2. DefaultVIP from cluster intent (cl.DefaultVIP())
// 3. State "VIP"
// 4. State "GATEWAY_VIP"
// 5. State "VIP_STATIC_IP"
// 6. Legacy demo "DEMO_VIP_HTTPROUTE"
// 7. Fallback "TMM_EXT_SELFIP"
func resolveTrafficVIP(cl *intent.Cluster, st *state.State, flagVIP string) (string, error) {
	if flagVIP != "" {
		return flagVIP, nil
	}
	if cl != nil {
		if defVIP, err := cl.DefaultVIP(); err == nil && defVIP != "" {
			return defVIP, nil
		}
	}
	if st != nil {
		for _, key := range []string{
			"VIP",
			"GATEWAY_VIP",
			"VIP_STATIC_IP",
			"DEMO_VIP_HTTPROUTE",
			"TMM_EXT_SELFIP",
		} {
			if v := st.Get(key); v != "" {
				return v, nil
			}
		}
	}
	return "", fmt.Errorf("unable to resolve VIP: specify --vip or configure external network CIDR")
}
