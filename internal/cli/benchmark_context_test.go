package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/JLCode-tech/gkebnkctl/internal/forge"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
)

func TestResolveBenchmarkContext_FromWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	wsName := "test-bench-ws"
	wsDir := filepath.Join(tmpDir, ".gkebnkctl", wsName)
	if err := os.MkdirAll(wsDir, 0750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// 1. Write state.env
	st, err := state.Load(wsDir)
	if err != nil {
		t.Fatalf("state.Load(): %v", err)
	}
	st.Set("PROJECT_ID", "test-gcp-proj")
	st.Set("ZONE", "us-central1-a")
	st.Set("REGION", "us-central1")
	st.Set("JUMPHOST_INSTANCE_NAME", "gkebnkctl-jumphost-test")
	st.Set("JUMPHOST_BNK_EXT_NIC_IP", "10.0.10.20")
	st.Set("DEMO_VIP_HTTPROUTE", "10.0.10.100")
	if err := st.Save(); err != nil {
		t.Fatalf("st.Save(): %v", err)
	}

	// 2. Write forge_link.json
	link := &forge.Link{
		ForgeURL:    "http://forge.example.internal:8000",
		ForgeMCPURL: "http://forge.example.internal:8001",
		ClusterID:   99,
		Status:      "registered",
	}
	if err := forge.WriteLink(wsDir, link); err != nil {
		t.Fatalf("forge.WriteLink(): %v", err)
	}

	// Change working dir temporarily so .gkebnkctl/ matches
	oldWd, _ := os.Getwd()
	_ = os.Chdir(tmpDir)
	defer func() { _ = os.Chdir(oldWd) }()

	// Clear flags
	flagBenchProject = ""
	flagBenchZone = ""
	flagBenchRegion = ""
	flagBenchInstanceID = ""
	flagBenchSourceIP = ""
	flagBenchVIP = ""
	flagBenchModel = ""
	flagBenchForgeURL = ""
	flagBenchWorkspace = wsName
	flagBenchConfig = ""

	err = resolveBenchmarkContext(benchmarkCmd)
	if err != nil {
		t.Fatalf("resolveBenchmarkContext failed: %v", err)
	}

	if flagBenchProject != "test-gcp-proj" {
		t.Errorf("flagBenchProject = %q, want 'test-gcp-proj'", flagBenchProject)
	}
	if flagBenchZone != "us-central1-a" {
		t.Errorf("flagBenchZone = %q, want 'us-central1-a'", flagBenchZone)
	}
	if flagBenchRegion != "us-central1" {
		t.Errorf("flagBenchRegion = %q, want 'us-central1'", flagBenchRegion)
	}
	if flagBenchInstanceID != "gkebnkctl-jumphost-test" {
		t.Errorf("flagBenchInstanceID = %q, want 'gkebnkctl-jumphost-test'", flagBenchInstanceID)
	}
	if flagBenchSourceIP != "10.0.10.20" {
		t.Errorf("flagBenchSourceIP = %q, want '10.0.10.20'", flagBenchSourceIP)
	}
	if flagBenchVIP != "10.0.10.100" {
		t.Errorf("flagBenchVIP = %q, want '10.0.10.100'", flagBenchVIP)
	}
	if flagBenchForgeURL != "http://forge.example.internal:8000" {
		t.Errorf("flagBenchForgeURL = %q, want 'http://forge.example.internal:8000'", flagBenchForgeURL)
	}
}

func TestResolveBenchmarkContext_SyntheticCluster(t *testing.T) {
	tmpDir := t.TempDir()
	jwtPath := filepath.Join(tmpDir, "license.jwt")
	if err := os.WriteFile(jwtPath, []byte("DUMMY-JWT\n"), 0600); err != nil {
		t.Fatalf("writing jwt: %v", err)
	}
	cfgPath := filepath.Join(tmpDir, "cluster.yaml")
	cfgContent := `apiVersion: gkebnkctl/v1
kind: Cluster

metadata:
  name: test-synth-cluster
  project: test-proj
  zone: us-central1-a

pattern: host-device

network:
  main:
    vpc: bnkhd-vpc
    subnet: bnkhd-subnet
    cidr: 10.60.0.0/24
    podsRangeName: bnkhd-pods
    podsCidr: 10.66.0.0/16
    svcsRangeName: bnkhd-svcs
    svcsCidr: 10.67.0.0/20
  external:
    vpc: bnkhd-ext
    subnet: bnkhd-ext
    cidr: 10.30.0.0/24
    mtu: 8896
    podsRangeName: tmm-ext-pods
    podsCidr: 10.31.0.0/24
    underlayCidr: 10.30.0.0/16
  internal:
    vpc: bnkhd-int
    subnet: bnkhd-int
    cidr: 10.50.0.0/24
    mtu: 8896
    podsRangeName: tmm-int-pods
    podsCidr: 10.51.0.0/24
  allowInternalCidr: 10.0.0.0/8
  natGateways: true

cluster:
  kubernetesVersion: "1.35"
  defaultPool:
    machineType: e2-standard-4
    nodeCount: 3
  tmmPool:
    name: tmm-pool
    machineType: e2-standard-8
    nodeCount: 1
    maxPodsPerNode: 8
  agentPool:
    name: agent-pool
    machineType: e2-standard-4
    nodeCount: 1
    maxPodsPerNode: 8

bnk:
  manifestVersion: "2.4.0-3.2882.0"
  tmmMtu: "1460"
  jwtPath: ` + jwtPath + `
  farSecret:
    source: kubecontext
    sourceContext: some-context
    namespaces: [f5-bnk, f5-utils]

ai:
  synthetic:
    enabled: true
    servedModelName: custom-llama
`
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	flagBenchProject = ""
	flagBenchZone = ""
	flagBenchRegion = ""
	flagBenchInstanceID = ""
	flagBenchSourceIP = ""
	flagBenchVIP = ""
	flagBenchModel = ""
	flagBenchForgeURL = ""
	flagBenchWorkspace = ""
	flagBenchConfig = cfgPath
	flagBenchSynthetic = false

	err := resolveBenchmarkContext(benchmarkCmd)
	if err != nil {
		t.Fatalf("resolveBenchmarkContext failed: %v", err)
	}

	if !flagBenchSynthetic {
		t.Error("flagBenchSynthetic = false, want true")
	}
	if flagBenchModel != "custom-llama" {
		t.Errorf("flagBenchModel = %q, want 'custom-llama'", flagBenchModel)
	}
	if flagBenchVIP != "10.30.0.100" {
		t.Errorf("flagBenchVIP = %q, want '10.30.0.100'", flagBenchVIP)
	}
	if flagBenchZone != "us-central1-a" {
		t.Errorf("flagBenchZone = %q, want 'us-central1-a'", flagBenchZone)
	}
	if flagBenchRegion != "us-central1" {
		t.Errorf("flagBenchRegion = %q, want 'us-central1'", flagBenchRegion)
	}
}

func TestResolveForgeGraph_ClusterScanAndProxyDiscovery(t *testing.T) {
	tmpDir := t.TempDir()
	wsName := "test-scan-ws"
	wsDir := filepath.Join(tmpDir, ".gkebnkctl", wsName)
	if err := os.MkdirAll(wsDir, 0750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := &forge.Link{
		ForgeURL:  "http://127.0.0.1:8000",
		ClusterID: 42,
		Status:    "registered",
	}
	if err := forge.WriteLink(wsDir, link); err != nil {
		t.Fatalf("forge.WriteLink(): %v", err)
	}

	oldWd, _ := os.Getwd()
	_ = os.Chdir(tmpDir)
	defer func() { _ = os.Chdir(oldWd) }()

	oldAgent := registerBenchmarkAgentFn
	oldDiscoverTargets := discoverTargetsFn
	oldListTargets := listBenchmarkTargetsFn
	oldDiscoverProxies := discoverProxiesFn
	oldListProxies := listProxyDeploymentsFn
	defer func() {
		registerBenchmarkAgentFn = oldAgent
		discoverTargetsFn = oldDiscoverTargets
		listBenchmarkTargetsFn = oldListTargets
		discoverProxiesFn = oldDiscoverProxies
		listProxyDeploymentsFn = oldListProxies
	}()

	agentCalled := false
	registerBenchmarkAgentFn = func(ctx context.Context, opts forge.BenchmarkAgentOptions) (forge.BenchmarkAgentResponse, error) {
		agentCalled = true
		return forge.BenchmarkAgentResponse{ID: 10, Name: opts.Name}, nil
	}

	discoverTargetsCalled := false
	discoverTargetsFn = func(ctx context.Context, opts forge.DiscoverTargetsOptions) (forge.DiscoverTargetsResponse, error) {
		discoverTargetsCalled = true
		if opts.ClusterID != 42 {
			t.Errorf("opts.ClusterID = %d, want 42", opts.ClusterID)
		}
		return forge.DiscoverTargetsResponse{
			ClusterID:       42,
			DiscoveredCount: 1,
			CreatedTargets: []forge.CreatedTargetItem{
				{ID: 2, Name: "vllm-gkebnkctl-scn-aiinference", Status: "created"},
			},
		}, nil
	}

	listTargetsCalled := false
	listBenchmarkTargetsFn = func(ctx context.Context, opts forge.ListBenchmarkTargetsOptions) ([]forge.BenchmarkTargetResponse, error) {
		listTargetsCalled = true
		return []forge.BenchmarkTargetResponse{
			{
				ID:           2,
				Name:         "vllm-gkebnkctl-scn-aiinference",
				ClusterID:    42,
				LLMBaseURL:   "http://vllm.gkebnkctl-scn-aiinference:80",
				LLMModel:     "meta-llama/Llama-3.1-8B-Instruct",
				LLMNamespace: "gkebnkctl-scn-aiinference",
			},
		}, nil
	}

	discoverProxiesCalled := false
	discoverProxiesFn = func(ctx context.Context, opts forge.ProxyDiscoverOptions) (forge.ProxyDiscoveryResult, error) {
		discoverProxiesCalled = true
		if opts.TargetID != 2 {
			t.Errorf("opts.TargetID = %d, want 2", opts.TargetID)
		}
		return forge.ProxyDiscoveryResult{TargetID: 2, DiscoveredCount: 1}, nil
	}

	listProxiesCalled := false
	listProxyDeploymentsFn = func(ctx context.Context, opts forge.ProxyDiscoverOptions) ([]forge.ProxyDeployment, error) {
		listProxiesCalled = true
		return []forge.ProxyDeployment{
			{
				ID:          1,
				TargetID:    2,
				ProxyType:   "f5-bnk",
				ExternalURL: "http://10.10.10.108:80",
				Status:      "discovered",
			},
		}, nil
	}

	flagBenchWorkspace = wsName
	flagBenchConfig = ""
	flagBenchInstanceID = "gkebnkctl-jumphost-test"
	flagBenchSourceIP = "10.0.1.50"
	flagBenchForgeURL = "http://127.0.0.1:8000"
	flagBenchModel = "llama3"
	flagBenchProxy = "f5-bnk"
	flagBenchVIP = ""

	graph := resolveForgeGraph(context.Background(), forge.RestCreds{}, "gkebnkctl-jumphost-test")

	if !agentCalled {
		t.Error("expected registerBenchmarkAgentFn to be called")
	}
	if !discoverTargetsCalled {
		t.Error("expected discoverTargetsFn to be called")
	}
	if !listTargetsCalled {
		t.Error("expected listBenchmarkTargetsFn to be called")
	}
	if !discoverProxiesCalled {
		t.Error("expected discoverProxiesFn to be called")
	}
	if !listProxiesCalled {
		t.Error("expected listProxyDeploymentsFn to be called")
	}

	if graph.targetID != 2 {
		t.Errorf("graph.targetID = %d, want 2", graph.targetID)
	}
	if graph.proxyDeploymentID != 1 {
		t.Errorf("graph.proxyDeploymentID = %d, want 1", graph.proxyDeploymentID)
	}
	if flagBenchVIP != "10.10.10.108" {
		t.Errorf("flagBenchVIP = %q, want '10.10.10.108'", flagBenchVIP)
	}
}
