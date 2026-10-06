package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/JLCode-tech/gkebnkctl/internal/forge"
	"github.com/JLCode-tech/gkebnkctl/internal/jumphost"
	"github.com/spf13/cobra"
)

var (
	flagDaemonHeartbeatInterval time.Duration
)

var benchmarkDaemonCmd = &cobra.Command{
	Use:     "daemon",
	Aliases: []string{"agent", "listen", "connect"},
	Short:   "Run the persistent Forge benchmark agent daemon",
	Long: `daemon connects to Forge over a persistent WebSocket connection, registers
the test agent, and maintains heartbeats (every 15s) to keep the agent in 'connected' status.

When an operator triggers 'Run Benchmark' in the Forge Web UI, Forge dispatches
the benchmark command down the WebSocket. The daemon executes aiperf on the jumphost
(or local test client) and streams progress and results back to Forge.

Works seamlessly when Forge is running on localhost, behind NAT, or in a private network.`,
	RunE: runBenchmarkDaemon,
}

func init() {
	benchmarkDaemonCmd.Flags().DurationVar(&flagDaemonHeartbeatInterval, "heartbeat-interval", 15*time.Second, "cadence for sending heartbeat pings to Forge")
	benchmarkCmd.AddCommand(benchmarkDaemonCmd)
}

func runBenchmarkDaemon(cmd *cobra.Command, args []string) error {
	parentCtx := cmd.Context()
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, stop := signal.NotifyContext(parentCtx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 1. Resolve workspace / cluster / jumphost context
	if err := resolveBenchmarkContext(cmd); err != nil {
		fmt.Fprintf(os.Stderr, "Notice: could not auto-resolve workspace context: %v\n", err)
	}

	agentName := flagBenchAgentName
	if agentName == "" {
		if flagBenchInstanceID != "" {
			agentName = fmt.Sprintf("gkebnkctl-jumphost-%s", flagBenchInstanceID)
		} else if flagBenchWorkspace != "" {
			agentName = fmt.Sprintf("gkebnkctl-agent-%s", flagBenchWorkspace)
		} else {
			agentName = "gkebnkctl-agent-local"
		}
	}

	hostname := flagBenchInstanceID
	if hostname == "" {
		h, err := os.Hostname()
		if err == nil {
			hostname = h
		} else {
			hostname = "gkebnkctl-host"
		}
	}

	ipAddr := flagBenchSourceIP
	if ipAddr == "" {
		ipAddr = "127.0.0.1"
	}

	fmt.Fprintf(os.Stderr, "==> Forge Benchmark Agent Daemon starting...\n")
	fmt.Fprintf(os.Stderr, "    Forge REST:  %s\n", flagBenchForgeURL)
	fmt.Fprintf(os.Stderr, "    Agent Name:  %s\n", agentName)
	fmt.Fprintf(os.Stderr, "    Hostname:    %s\n", hostname)
	fmt.Fprintf(os.Stderr, "    Zone:        %s\n", flagBenchZone)
	fmt.Fprintf(os.Stderr, "    Region:      %s\n", flagBenchRegion)
	fmt.Fprintf(os.Stderr, "    Jumphost ID: %s\n", flagBenchInstanceID)

	worker, err := forge.NewBenchmarkAgentWorker(forge.AgentWorkerOptions{
		RestURL: flagBenchForgeURL,
		Creds: forge.RestCreds{
			Username: flagBenchForgeUser,
			Password: flagBenchForgePass,
		},
		AgentName:         agentName,
		AgentToken:        flagBenchAgentToken,
		WorkspaceDir:      resolveWorkspaceDir(),
		Hostname:          hostname,
		IPAddress:         ipAddr,
		Tags:              map[string]string{"role": "benchmark-agent", "managed_by": "gkebnkctl"},
		Capabilities:      []string{"aiperf"},
		HeartbeatInterval: flagDaemonHeartbeatInterval,
		Logger: func(format string, a ...any) {
			msg := fmt.Sprintf(format, a...)
			timestamp := time.Now().Format("15:04:05")
			fmt.Fprintf(os.Stderr, "[%s] %s\n", timestamp, msg)
		},
		RunHandler: func(runCtx context.Context, runID int, config map[string]any) (map[string]any, error) {
			return executeDispatchedRun(runCtx, runID, config)
		},
		CancelHandler: func(runID int) {
			fmt.Fprintf(os.Stderr, "[Forge Agent] Run #%d canceled by Forge\n", runID)
		},
	})
	if err != nil {
		return fmt.Errorf("init agent worker: %w", err)
	}

	fmt.Fprintf(os.Stderr, "✓ Agent worker ready — connecting to Forge WebSocket...\n")
	return worker.Run(ctx)
}

func executeDispatchedRun(ctx context.Context, runID int, config map[string]any) (map[string]any, error) {
	cfg, targetVIP := jumphost.ConfigMapToAiperfConfig(config)
	if targetVIP == "" {
		targetVIP = flagBenchVIP
	}

	if cfg.HostHeader == "" && flagBenchHostHeader != "" {
		cfg.HostHeader = flagBenchHostHeader
	}

	probOpts := jumphost.ProbeOptions{
		Project:    flagBenchProject,
		Zone:       flagBenchZone,
		Region:     flagBenchRegion,
		InstanceID: flagBenchInstanceID,
		VIP:        targetVIP,
		Hostname:   cfg.HostHeader,
	}

	// Auto-resolve model name if placeholder or empty: --model first, else ask
	// the endpoint (a real vLLM rejects any name but its served model).
	if cfg.Model == "" || cfg.Model == "auto-discovered/vllm" {
		if flagBenchModel != "" {
			cfg.Model = flagBenchModel
		} else if model, err := resolveServedModelCached(ctx, probOpts, cfg.HostHeader); err == nil {
			cfg.Model = model
		} else {
			fmt.Fprintf(os.Stderr, "[Run #%d] could not resolve the served model, keeping %q: %v\n", runID, cfg.Model, err)
		}
	}

	fmt.Fprintf(os.Stderr, "[Run #%d] Executing benchmark: model=%s vip=%s host=%s concurrency=%d requests=%d rate=%g streaming=%v\n",
		runID, cfg.Model, targetVIP, cfg.HostHeader, cfg.Concurrency, cfg.NumRequests, cfg.RequestRate, cfg.Streaming)

	runOpts := jumphost.AiperfRunOptions{
		ProbeOptions: probOpts,
		Config:       cfg,
		RunLabel:     fmt.Sprintf("forge-run-%d", runID),
		ResultID:     fmt.Sprintf("forge-%d", runID),
	}

	result, err := executeBenchmarkRun(ctx, runOpts)
	if err != nil {
		return nil, fmt.Errorf("aiperf execution failed: %w", err)
	}

	if result.RawJSON != "" {
		var rawMap map[string]any
		if err := json.Unmarshal([]byte(result.RawJSON), &rawMap); err == nil {
			return rawMap, nil
		}
	}

	// Fallback to marshaled AiperfResult
	b, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}
	var resMap map[string]any
	if err := json.Unmarshal(b, &resMap); err != nil {
		return nil, fmt.Errorf("unmarshal result map: %w", err)
	}
	return resMap, nil
}

// servedModels caches the resolved model per VIP and Host header: every step
// of a sweep targets the same endpoint, so one lookup per daemon is enough.
var servedModels sync.Map

func resolveServedModelCached(ctx context.Context, opts jumphost.ProbeOptions, hostHeader string) (string, error) {
	key := opts.VIP + "|" + hostHeader
	if v, ok := servedModels.Load(key); ok {
		return v.(string), nil
	}
	model, err := jumphost.ResolveServedModel(ctx, opts, hostHeader)
	if err != nil {
		return "", err
	}
	servedModels.Store(key, model)
	return model, nil
}
