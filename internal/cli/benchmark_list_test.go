package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func TestBenchmarkList_TextOutput(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	flagOutput = "text"
	err := runBenchmarkList(nil, nil)
	_ = w.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("runBenchmarkList returned error: %v", err)
	}

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	// Ensure native scenarios are listed
	if !strings.Contains(output, "NATIVE FORGE SCENARIOS") {
		t.Errorf("expected NATIVE FORGE SCENARIOS header, got:\n%s", output)
	}
	if !strings.Contains(output, "baseline") {
		t.Errorf("expected 'baseline' scenario in output, got:\n%s", output)
	}
	if !strings.Contains(output, "mooncake") {
		t.Errorf("expected 'mooncake' scenario in output, got:\n%s", output)
	}

	// Ensure smoke presets are listed
	if !strings.Contains(output, "SMOKE PRESETS") {
		t.Errorf("expected SMOKE PRESETS header, got:\n%s", output)
	}
	if !strings.Contains(output, "latency") {
		t.Errorf("expected 'latency' preset in output, got:\n%s", output)
	}
}

func TestBenchmarkList_JSONOutput(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	flagOutput = "json"
	err := runBenchmarkList(nil, nil)
	_ = w.Close()
	os.Stdout = oldStdout
	flagOutput = "text" // reset

	if err != nil {
		t.Fatalf("runBenchmarkList returned error: %v", err)
	}

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	var payload struct {
		Schema    string             `json:"schema"`
		Scenarios []scenarioListItem `json:"scenarios"`
	}
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse JSON output: %v\nOutput was: %s", err, buf.String())
	}

	if payload.Schema != "gkebnkctl.benchmark.list.v1" {
		t.Errorf("Schema = %q, want 'gkebnkctl.benchmark.list.v1'", payload.Schema)
	}
	if len(payload.Scenarios) == 0 {
		t.Errorf("expected non-empty scenarios list")
	}

	foundBaseline := false
	foundLatency := false
	for _, sc := range payload.Scenarios {
		if sc.Key == "baseline" && sc.Type == "native" {
			foundBaseline = true
		}
		if sc.Key == "latency" && sc.Type == "preset" {
			foundLatency = true
		}
	}
	if !foundBaseline {
		t.Errorf("missing baseline native scenario in JSON output")
	}
	if !foundLatency {
		t.Errorf("missing latency smoke preset in JSON output")
	}
}
