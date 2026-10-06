package jumphost

import (
	"strings"
	"testing"
	"time"
)

func TestConfigMapToAiperfConfig(t *testing.T) {
	input := map[string]any{
		"url":                         "http://10.10.10.100:8000/v1/chat/completions",
		"model":                       "meta-llama/Meta-Llama-3-8B-Instruct",
		"endpoint_type":               "chat",
		"endpoint":                    "/v1/chat/completions",
		"streaming":                   true,
		"request_timeout_seconds":     float64(300),
		"concurrency":                 float64(25),
		"request_count":               float64(100),
		"synthetic_input_tokens_mean": float64(256),
		"output_tokens_mean":          float64(64),
		"extra_inputs":                []any{"ignore_eos:true"},
		"host_header":                 "vllm.example.com",
	}

	cfg, vip := ConfigMapToAiperfConfig(input)

	if vip != "10.10.10.100:8000" {
		t.Errorf("expected VIP 10.10.10.100:8000, got %q", vip)
	}
	if cfg.Model != "meta-llama/Meta-Llama-3-8B-Instruct" {
		t.Errorf("unexpected model: %q", cfg.Model)
	}
	if cfg.Concurrency != 25 {
		t.Errorf("unexpected concurrency: %d", cfg.Concurrency)
	}
	if cfg.NumRequests != 100 {
		t.Errorf("unexpected num requests: %d", cfg.NumRequests)
	}
	if cfg.ISL != 256 {
		t.Errorf("unexpected ISL: %d", cfg.ISL)
	}
	if cfg.OSL != 64 {
		t.Errorf("unexpected OSL: %d", cfg.OSL)
	}
	if !cfg.Streaming {
		t.Errorf("expected streaming to be true")
	}
	if cfg.Timeout != 300*time.Second {
		t.Errorf("unexpected timeout: %v", cfg.Timeout)
	}
	if len(cfg.ExtraInputs) != 1 || cfg.ExtraInputs[0] != "ignore_eos:true" {
		t.Errorf("unexpected extra inputs: %v", cfg.ExtraInputs)
	}
	if cfg.HostHeader != "vllm.example.com" {
		t.Errorf("unexpected host header: %q", cfg.HostHeader)
	}
}

// A Forge Poisson rate step must run open loop: no forced --concurrency 1 or
// --request-count 10, and the rate/warmup/duration/goodput/prefix settings reach aiperf.
func TestConfigMapToAiperfConfig_PoissonStep(t *testing.T) {
	cfg, vip := ConfigMapToAiperfConfig(map[string]any{
		"url":                         "http://10.0.10.112:80",
		"model":                       "m",
		"request_rate":                float64(8),
		"arrival_pattern":             "poisson",
		"warmup_duration":             float64(30),
		"benchmark_duration":          float64(120),
		"goodput":                     "time_to_first_token:2000 inter_token_latency:200",
		"random_seed":                 float64(42),
		"synthetic_input_tokens_mean": float64(1000),
		"prefix_prompt_length":        float64(4000),
		"num_prefix_prompts":          float64(20),
	})
	if vip != "10.0.10.112:80" {
		t.Fatalf("vip = %q", vip)
	}
	if cfg.Timeout < 180*time.Second {
		t.Fatalf("session timeout %v shorter than warmup + duration", cfg.Timeout)
	}
	cmd := buildAiperfCmd(AiperfRunOptions{Config: cfg, ProbeOptions: ProbeOptions{VIP: vip}})
	for _, want := range []string{
		"--request-rate 8", "--arrival-pattern 'poisson'", "--warmup-duration 30", "--benchmark-duration 120",
		"--goodput 'time_to_first_token:2000 inter_token_latency:200'", "--random-seed 42",
		"--prefix-prompt-length 4000", "--num-prefix-prompts 20",
		// One prompt per request (8 rps × 150 s), not aiperf's 100 a KV cache holds whole.
		"--num-dataset-entries 1200",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command missing %q:\n%s", want, cmd)
		}
	}
	for _, unwanted := range []string{"--concurrency", "--request-count"} {
		if strings.Contains(cmd, unwanted) {
			t.Errorf("open-loop command must not set %s:\n%s", unwanted, cmd)
		}
	}
}
