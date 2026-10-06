package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/JLCode-tech/gkebnkctl/internal/jumphost"
)

func TestBenchmarkStatus_TextOutput(t *testing.T) {
	oldEnsure := ensureAiperfFn
	oldCheck := checkServedModelFn
	defer func() {
		ensureAiperfFn = oldEnsure
		checkServedModelFn = oldCheck
	}()

	ensureAiperfFn = func(ctx context.Context, opts jumphost.ProbeOptions) error {
		return nil
	}
	checkServedModelFn = func(ctx context.Context, opts jumphost.ProbeOptions, model string) error {
		return nil
	}

	flagBenchRegion = "us-central1"
	flagBenchZone = "us-central1-a"
	flagBenchProject = "test-proj"
	flagBenchInstanceID = "gkebnkctl-jumphost-vm"
	flagBenchSourceIP = "10.0.1.10"
	flagBenchVIP = "10.0.10.100"
	flagBenchModel = "meta-llama/Llama-3.1-8B-Instruct"
	flagBenchWorkspace = "ai-ws"
	flagBenchConfig = ""
	flagOutput = "text"

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runBenchmarkStatus(benchmarkStatusCmd, nil)
	_ = w.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("runBenchmarkStatus failed: %v", err)
	}

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	out := buf.String()

	if !strings.Contains(out, "BENCHMARK ENVIRONMENT STATUS") {
		t.Errorf("expected BENCHMARK ENVIRONMENT STATUS header, got:\n%s", out)
	}
	if !strings.Contains(out, "gkebnkctl-jumphost-vm") {
		t.Errorf("expected instance ID in status output")
	}
	if !strings.Contains(out, "READY") {
		t.Errorf("expected READY overall status")
	}
}

func TestBenchmarkStatus_JSONOutput(t *testing.T) {
	oldEnsure := ensureAiperfFn
	oldCheck := checkServedModelFn
	defer func() {
		ensureAiperfFn = oldEnsure
		checkServedModelFn = oldCheck
	}()

	ensureAiperfFn = func(ctx context.Context, opts jumphost.ProbeOptions) error {
		return nil
	}
	checkServedModelFn = func(ctx context.Context, opts jumphost.ProbeOptions, model string) error {
		return nil
	}

	flagBenchRegion = "us-central1"
	flagBenchZone = "us-central1-a"
	flagBenchProject = "test-proj"
	flagBenchInstanceID = "gkebnkctl-jumphost-vm"
	flagBenchSourceIP = "10.0.1.10"
	flagBenchVIP = "10.0.10.100"
	flagBenchModel = "meta-llama/Llama-3.1-8B-Instruct"
	flagBenchWorkspace = "ai-ws"
	flagBenchConfig = ""
	flagOutput = "json"

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runBenchmarkStatus(benchmarkStatusCmd, nil)
	_ = w.Close()
	os.Stdout = oldStdout
	flagOutput = "text" // reset

	if err != nil {
		t.Fatalf("runBenchmarkStatus failed: %v", err)
	}

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	var res benchmarkStatusResult
	if err := json.Unmarshal(buf.Bytes(), &res); err != nil {
		t.Fatalf("failed to parse JSON status output: %v\nOutput: %s", err, buf.String())
	}

	if res.InstanceID != "gkebnkctl-jumphost-vm" {
		t.Errorf("InstanceID = %q, want 'gkebnkctl-jumphost-vm'", res.InstanceID)
	}
	if res.OverallReadiness != "READY" {
		t.Errorf("OverallReadiness = %q, want 'READY'", res.OverallReadiness)
	}
}
