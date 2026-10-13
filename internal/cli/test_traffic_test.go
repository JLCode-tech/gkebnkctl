package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/jumphost"
)

func resetTrafficFlags() {
	flagTestTrafficConfig = "cluster.yaml"
	flagTestTrafficVIP = ""
	flagTestTrafficIterations = 5
	flagTestTrafficTimeout = 10 * time.Second
	flagTestTrafficHost = ""
	flagTestTrafficInstance = ""
	flagTestTrafficDryRun = false
	flagTestTrafficJSON = false
	flagOutput = "text"
	rootCmd.SetArgs(nil)
}

// writeTrafficTestCluster creates a minimal cluster.yaml for test traffic.
func writeTrafficTestCluster(t *testing.T, dir string) string {
	t.Helper()
	jwtPath := filepath.Join(dir, "license.jwt")
	if err := os.WriteFile(jwtPath, []byte("DUMMY-JWT\n"), 0o600); err != nil {
		t.Fatalf("writing dummy jwt: %v", err)
	}
	body := `apiVersion: gkebnkctl/v1
kind: Cluster
metadata:
  name: bnk-test
  project: test-gcp-project
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
bnk:
  manifestVersion: "2.4.0-3.2882.0"
  jwtPath: ` + jwtPath + `
  farSecret:
    source: kubecontext
    sourceContext: some-other-cluster
`
	path := filepath.Join(dir, "cluster.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing cluster.yaml: %v", err)
	}
	return path
}

func execTraffic(t *testing.T, args []string) (string, string, error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	rootCmd.SetErr(&errBuf)
	testTrafficCmd.SetOut(&outBuf)
	testTrafficCmd.SetErr(&errBuf)
	rootCmd.SetArgs(append([]string{"test", "traffic"}, args...))
	err := rootCmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestTestTraffic_DryRunAndFlagParsing(t *testing.T) {
	resetTrafficFlags()
	origFn := runCurlProbesFn
	t.Cleanup(func() {
		runCurlProbesFn = origFn
		resetTrafficFlags()
	})

	probesCalled := false
	runCurlProbesFn = func(ctx context.Context, opts jumphost.ProbeOptions) ([]jumphost.ProbeResult, error) {
		probesCalled = true
		return nil, nil
	}

	dir := t.TempDir()
	configPath := writeTrafficTestCluster(t, dir)
	withCWD(t, dir)

	_, stderr, err := execTraffic(t, []string{
		"-f", configPath,
		"--dry-run",
		"--vip", "10.30.0.222",
		"--host", "example.com",
		"--iterations", "7",
		"--timeout", "12s",
	})

	if err != nil {
		t.Fatalf("dry-run execute failed: %v", err)
	}

	if probesCalled {
		t.Fatalf("runCurlProbesFn should NOT have been called during dry-run")
	}

	for _, expected := range []string{
		"→ Traffic test dry-run plan:",
		"Cluster:    bnk-test",
		"Project:    test-gcp-project",
		"Zone:       us-central1-a",
		"Region:     us-central1",
		"Jumphost:   bnk-test-jumphost",
		"VIP:        10.30.0.222",
		"Host:       example.com",
		"Iterations: 7",
		"Timeout:    12s",
		"curl -s -o /dev/null",
		"-H 'Host: example.com'",
		"http://10.30.0.222/",
	} {
		if !strings.Contains(stderr, expected) {
			t.Errorf("dry-run output missing expected string %q;\ngot:\n%s", expected, stderr)
		}
	}
}

func TestTestTraffic_SuccessfulProbes_JSON(t *testing.T) {
	resetTrafficFlags()
	origFn := runCurlProbesFn
	t.Cleanup(func() {
		runCurlProbesFn = origFn
		resetTrafficFlags()
	})

	dir := t.TempDir()
	configPath := writeTrafficTestCluster(t, dir)
	withCWD(t, dir)

	// Populate state.env
	stDir := filepath.Join(dir, ".gkebnkctl", "bnk-test")
	if err := os.MkdirAll(stDir, 0o750); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	st, err := state.Load(stDir)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	st.Set("JUMPHOST_INSTANCE_NAME", "custom-jumphost-vm")
	st.Set("JUMPHOST_BNK_EXT_NIC_IP", "10.30.0.75")
	if err := st.Save(); err != nil {
		t.Fatalf("save state: %v", err)
	}

	mockProbes := []jumphost.ProbeResult{
		{Iteration: 1, HTTPCode: 200, Seconds: 0.010},
		{Iteration: 2, HTTPCode: 200, Seconds: 0.020},
		{Iteration: 3, HTTPCode: 200, Seconds: 0.030},
	}
	var capturedOpts jumphost.ProbeOptions
	runCurlProbesFn = func(ctx context.Context, opts jumphost.ProbeOptions) ([]jumphost.ProbeResult, error) {
		capturedOpts = opts
		return mockProbes, nil
	}

	stdout, _, err := execTraffic(t, []string{
		"-f", configPath,
		"--json",
		"--iterations", "3",
	})

	if err != nil {
		t.Fatalf("test traffic failed: %v", err)
	}

	// Verify ProbeOptions passed to jumphost
	if capturedOpts.InstanceID != "custom-jumphost-vm" {
		t.Errorf("InstanceID = %q, want custom-jumphost-vm", capturedOpts.InstanceID)
	}
	if capturedOpts.SourceIP != "10.30.0.75" {
		t.Errorf("SourceIP = %q, want 10.30.0.75", capturedOpts.SourceIP)
	}
	// Default VIP for 10.30.0.0/24 is 10.30.0.100
	if capturedOpts.VIP != "10.30.0.100" {
		t.Errorf("VIP = %q, want 10.30.0.100", capturedOpts.VIP)
	}

	var res TrafficTestResult
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("unmarshaling JSON output: %v\nstdout was:\n%s", err, stdout)
	}

	if res.Cluster != "bnk-test" {
		t.Errorf("Cluster = %q, want bnk-test", res.Cluster)
	}
	if res.VIP != "10.30.0.100" {
		t.Errorf("VIP = %q, want 10.30.0.100", res.VIP)
	}
	if res.Instance != "custom-jumphost-vm" {
		t.Errorf("Instance = %q, want custom-jumphost-vm", res.Instance)
	}
	if res.Total != 3 {
		t.Errorf("Total = %d, want 3", res.Total)
	}
	if res.Successful != 3 {
		t.Errorf("Successful = %d, want 3", res.Successful)
	}
	if res.Failed != 0 {
		t.Errorf("Failed = %d, want 0", res.Failed)
	}
	if res.MinRTTMs != 10.0 {
		t.Errorf("MinRTTMs = %f, want 10.0", res.MinRTTMs)
	}
	if res.AvgRTTMs != 20.0 {
		t.Errorf("AvgRTTMs = %f, want 20.0", res.AvgRTTMs)
	}
	if res.MaxRTTMs != 30.0 {
		t.Errorf("MaxRTTMs = %f, want 30.0", res.MaxRTTMs)
	}
	if len(res.Probes) != 3 {
		t.Errorf("len(res.Probes) = %d, want 3", len(res.Probes))
	}
}

func TestTestTraffic_FailedProbes(t *testing.T) {
	resetTrafficFlags()
	origFn := runCurlProbesFn
	t.Cleanup(func() {
		runCurlProbesFn = origFn
		resetTrafficFlags()
	})

	dir := t.TempDir()
	configPath := writeTrafficTestCluster(t, dir)
	withCWD(t, dir)

	mockProbes := []jumphost.ProbeResult{
		{Iteration: 1, HTTPCode: 200, Seconds: 0.010},
		{Iteration: 2, HTTPCode: 503, Seconds: 0.020},
		{Iteration: 3, HTTPCode: 0, Seconds: 0, Err: "connection timeout"},
	}
	runCurlProbesFn = func(ctx context.Context, opts jumphost.ProbeOptions) ([]jumphost.ProbeResult, error) {
		return mockProbes, nil
	}

	stdout, _, err := execTraffic(t, []string{
		"-f", configPath,
		"--instance", "override-jumphost",
		"--vip", "10.30.0.100",
	})

	if err == nil {
		t.Fatalf("expected error on failed probes, got nil")
	}
	if !strings.Contains(err.Error(), "2/3 probes failed") {
		t.Errorf("error = %q, want '2/3 probes failed'", err.Error())
	}

	// Verify text table contains FAIL markers
	if !strings.Contains(stdout, "FAIL (HTTP 503)") {
		t.Errorf("output missing 'FAIL (HTTP 503)':\n%s", stdout)
	}
	if !strings.Contains(stdout, "FAIL (connection timeout)") {
		t.Errorf("output missing 'FAIL (connection timeout)':\n%s", stdout)
	}
}

func TestTestTraffic_MissingJumphostInState(t *testing.T) {
	resetTrafficFlags()
	origFn := runCurlProbesFn
	t.Cleanup(func() {
		runCurlProbesFn = origFn
		resetTrafficFlags()
	})

	dir := t.TempDir()
	configPath := writeTrafficTestCluster(t, dir)
	withCWD(t, dir)

	// No state.env created and no --instance flag provided
	_, _, err := execTraffic(t, []string{
		"-f", configPath,
	})

	if err == nil {
		t.Fatalf("expected error for missing jumphost in state, got nil")
	}

	expectedErr := "jumphost instance not found in state"
	if !strings.Contains(err.Error(), expectedErr) {
		t.Errorf("error = %q, want substring %q", err.Error(), expectedErr)
	}
}

func TestTestTraffic_VIPPrecedence(t *testing.T) {
	resetTrafficFlags()
	origFn := runCurlProbesFn
	t.Cleanup(func() {
		runCurlProbesFn = origFn
		resetTrafficFlags()
	})

	dir := t.TempDir()
	configPath := writeTrafficTestCluster(t, dir)
	withCWD(t, dir)

	stDir := filepath.Join(dir, ".gkebnkctl", "bnk-test")
	if err := os.MkdirAll(stDir, 0o750); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	st, err := state.Load(stDir)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	st.Set("JUMPHOST_INSTANCE_NAME", "vm-1")
	st.Set("DEMO_VIP_HTTPROUTE", "10.30.0.150")
	st.Set("VIP", "10.30.0.180")
	if err := st.Save(); err != nil {
		t.Fatalf("save state: %v", err)
	}

	var capturedOpts jumphost.ProbeOptions
	runCurlProbesFn = func(ctx context.Context, opts jumphost.ProbeOptions) ([]jumphost.ProbeResult, error) {
		capturedOpts = opts
		return []jumphost.ProbeResult{{Iteration: 1, HTTPCode: 200, Seconds: 0.01}}, nil
	}

	// 1. Explicit --vip wins over everything
	resetTrafficFlags()
	if _, _, err := execTraffic(t, []string{"-f", configPath, "--vip", "10.30.0.254"}); err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if capturedOpts.VIP != "10.30.0.254" {
		t.Errorf("VIP with --vip flag = %q, want 10.30.0.254", capturedOpts.VIP)
	}

	// 2. cl.DefaultVIP() wins over state
	// Note: In our cluster.yaml, network.external.cidr is 10.30.0.0/24 so DefaultVIP() returns 10.30.0.100
	resetTrafficFlags()
	if _, _, err := execTraffic(t, []string{"-f", configPath}); err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if capturedOpts.VIP != "10.30.0.100" {
		t.Errorf("VIP with DefaultVIP = %q, want 10.30.0.100", capturedOpts.VIP)
	}
}

func TestTestTraffic_GlobalOutputJSON(t *testing.T) {
	resetTrafficFlags()
	origFn := runCurlProbesFn
	t.Cleanup(func() {
		runCurlProbesFn = origFn
		resetTrafficFlags()
	})

	dir := t.TempDir()
	configPath := writeTrafficTestCluster(t, dir)
	withCWD(t, dir)

	runCurlProbesFn = func(ctx context.Context, opts jumphost.ProbeOptions) ([]jumphost.ProbeResult, error) {
		return []jumphost.ProbeResult{{Iteration: 1, HTTPCode: 200, Seconds: 0.005}}, nil
	}

	stdout, _, err := execTraffic(t, []string{
		"-o", "json",
		"-f", configPath,
		"--instance", "jumphost-inst",
	})

	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	var res TrafficTestResult
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("global -o json did not produce valid JSON: %v\noutput: %s", err, stdout)
	}
	if res.Cluster != "bnk-test" {
		t.Errorf("res.Cluster = %q, want bnk-test", res.Cluster)
	}
}

func TestResolveTrafficVIP_Precedence(t *testing.T) {
	dir := t.TempDir()
	configPath := writeTrafficTestCluster(t, dir)
	cl, err := intent.Load(configPath)
	if err != nil {
		t.Fatalf("intent.Load: %v", err)
	}

	st, err := state.Load(t.TempDir())
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}

	// 1. Explicit flag wins over cluster intent and state
	st.Set("VIP", "10.0.1.1")
	got, err := resolveTrafficVIP(cl, st, "10.99.99.99")
	if err != nil || got != "10.99.99.99" {
		t.Fatalf("flag VIP override failed: got %q, err %v, want 10.99.99.99", got, err)
	}

	// 2. cl.DefaultVIP() wins over state when flag is empty
	got, err = resolveTrafficVIP(cl, st, "")
	if err != nil || got != "10.30.0.100" {
		t.Fatalf("cl.DefaultVIP precedence failed: got %q, err %v, want 10.30.0.100", got, err)
	}

	// 3. State hierarchy when cl is nil:
	// VIP > GATEWAY_VIP > VIP_STATIC_IP > DEMO_VIP_HTTPROUTE > TMM_EXT_SELFIP
	steps := []struct {
		name    string
		set     map[string]string
		wantVIP string
	}{
		{
			name: "VIP beats everything in state",
			set: map[string]string{
				"VIP":                "10.0.1.1",
				"GATEWAY_VIP":        "10.0.1.2",
				"VIP_STATIC_IP":      "10.0.1.3",
				"DEMO_VIP_HTTPROUTE": "10.0.1.4",
				"TMM_EXT_SELFIP":     "10.0.1.5",
			},
			wantVIP: "10.0.1.1",
		},
		{
			name: "GATEWAY_VIP beats VIP_STATIC_IP and demo keys",
			set: map[string]string{
				"VIP":                "",
				"GATEWAY_VIP":        "10.0.1.2",
				"VIP_STATIC_IP":      "10.0.1.3",
				"DEMO_VIP_HTTPROUTE": "10.0.1.4",
				"TMM_EXT_SELFIP":     "10.0.1.5",
			},
			wantVIP: "10.0.1.2",
		},
		{
			name: "VIP_STATIC_IP beats demo keys",
			set: map[string]string{
				"VIP":                "",
				"GATEWAY_VIP":        "",
				"VIP_STATIC_IP":      "10.0.1.3",
				"DEMO_VIP_HTTPROUTE": "10.0.1.4",
				"TMM_EXT_SELFIP":     "10.0.1.5",
			},
			wantVIP: "10.0.1.3",
		},
		{
			name: "DEMO_VIP_HTTPROUTE fallback beats TMM_EXT_SELFIP",
			set: map[string]string{
				"VIP":                "",
				"GATEWAY_VIP":        "",
				"VIP_STATIC_IP":      "",
				"DEMO_VIP_HTTPROUTE": "10.0.1.4",
				"TMM_EXT_SELFIP":     "10.0.1.5",
			},
			wantVIP: "10.0.1.4",
		},
		{
			name: "TMM_EXT_SELFIP final fallback",
			set: map[string]string{
				"VIP":                "",
				"GATEWAY_VIP":        "",
				"VIP_STATIC_IP":      "",
				"DEMO_VIP_HTTPROUTE": "",
				"TMM_EXT_SELFIP":     "10.0.1.5",
			},
			wantVIP: "10.0.1.5",
		},
	}

	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			testSt, err := state.Load(t.TempDir())
			if err != nil {
				t.Fatalf("state.Load: %v", err)
			}
			for k, v := range step.set {
				testSt.Set(k, v)
			}
			gotVIP, err := resolveTrafficVIP(nil, testSt, "")
			if err != nil {
				t.Fatalf("resolveTrafficVIP returned error: %v", err)
			}
			if gotVIP != step.wantVIP {
				t.Errorf("got VIP = %q, want %q", gotVIP, step.wantVIP)
			}
		})
	}

	// 4. Failure when no source resolves
	emptySt, err := state.Load(t.TempDir())
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	if _, err := resolveTrafficVIP(nil, emptySt, ""); err == nil {
		t.Errorf("expected error when resolving VIP with no sources, got nil")
	}
}
