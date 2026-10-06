package jumphost

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ConfigMapToAiperfConfig converts a Forge WebSocket config dict into an AiperfConfig
// and extracts the target VIP (if present in the URL field).
func ConfigMapToAiperfConfig(m map[string]any) (AiperfConfig, string) {
	var cfg AiperfConfig
	var targetVIP string

	// Model
	if v, ok := m["model"].(string); ok && v != "" {
		cfg.Model = v
	}

	// Endpoint Type
	if v, ok := m["endpoint_type"].(string); ok && v != "" {
		cfg.EndpointType = v
	}

	// Endpoint Path
	if v, ok := m["endpoint_path"].(string); ok && v != "" {
		cfg.EndpointPath = v
	}

	// Host header
	if v, ok := m["host_header"].(string); ok && v != "" {
		cfg.HostHeader = v
	}

	// Concurrency
	if v := getInt(m, "concurrency"); v > 0 {
		cfg.Concurrency = v
	}

	// Request count
	if v := getInt(m, "request_count"); v > 0 {
		cfg.NumRequests = v
	} else if v := getInt(m, "num_requests"); v > 0 {
		cfg.NumRequests = v
	} else if v := getInt(m, "total_requests"); v > 0 {
		cfg.NumRequests = v
	}

	// ISL / synthetic_input_tokens_mean
	if v := getInt(m, "synthetic_input_tokens_mean"); v > 0 {
		cfg.ISL = v
	} else if v := getInt(m, "isl"); v > 0 {
		cfg.ISL = v
	}

	// OSL / output_tokens_mean
	if v := getInt(m, "output_tokens_mean"); v > 0 {
		cfg.OSL = v
	} else if v := getInt(m, "osl"); v > 0 {
		cfg.OSL = v
	}

	// Streaming
	if v, ok := m["streaming"].(bool); ok {
		cfg.Streaming = v
	} else if v, ok := m["stream"].(bool); ok {
		cfg.Streaming = v
	}

	// Timeout
	if v := getInt(m, "request_timeout_seconds"); v > 0 {
		cfg.Timeout = time.Duration(v) * time.Second
	} else if v := getInt(m, "timeout"); v > 0 {
		cfg.Timeout = time.Duration(v) * time.Second
	}

	// Extra inputs
	if extraList, ok := m["extra_inputs"].([]any); ok {
		for _, item := range extraList {
			if str, ok := item.(string); ok && str != "" {
				cfg.ExtraInputs = append(cfg.ExtraInputs, str)
			}
		}
	} else if extraStrList, ok := m["extra_inputs"].([]string); ok {
		cfg.ExtraInputs = append(cfg.ExtraInputs, extraStrList...)
	}

	// Workload shape
	if v := getInt(m, "synthetic_input_tokens_stddev"); v > 0 {
		cfg.ISLStddev = v
	}
	if v := getInt(m, "prefix_prompt_length"); v > 0 {
		cfg.PrefixPromptLength = v
	}
	if v := getInt(m, "num_prefix_prompts"); v > 0 {
		cfg.NumPrefixPrompts = v
	}
	if v, ok := m["seq_dist"].(string); ok && v != "" {
		cfg.SeqDist = v
	}
	if v := getInt(m, "random_seed"); v > 0 {
		cfg.RandomSeed = v
	}
	if v := getInt(m, "num_dataset_entries"); v > 0 {
		cfg.NumDatasetEntries = v
	}
	if v, ok := m["goodput"].(string); ok && v != "" {
		cfg.Goodput = v
	}

	// Open-loop load: a request rate or a fixed duration drives the run, so
	// the closed-loop defaults (concurrency 1, 10 requests) must not apply
	// unless Forge sent them.
	if v := getFloat(m, "request_rate"); v > 0 {
		cfg.RequestRate = v
	}
	if v, ok := m["arrival_pattern"].(string); ok && v != "" {
		cfg.ArrivalPattern = v
	}
	if v := getFloat(m, "warmup_duration"); v > 0 {
		cfg.WarmupDuration = v
	}
	if v := getFloat(m, "benchmark_duration"); v > 0 {
		cfg.BenchmarkDuration = v
	}
	if cfg.RequestRate > 0 || cfg.BenchmarkDuration > 0 {
		if getInt(m, "concurrency") <= 0 {
			cfg.Concurrency = 0
		}
		if getInt(m, "request_count") <= 0 && getInt(m, "num_requests") <= 0 && getInt(m, "total_requests") <= 0 {
			cfg.NumRequests = 0
		}
	}

	// Trace URL / mooncake
	if v, ok := m["trace_url"].(string); ok && v != "" {
		cfg.TraceURL = v
	}
	if v := getInt(m, "workers_max"); v > 0 {
		cfg.WorkersMax = v
	}
	if v, ok := m["fixed_schedule"].(bool); ok {
		cfg.FixedSchedule = v
	}

	cfg.FitTimeoutToDuration()

	// URL parsing for VIP
	if rawURL, ok := m["url"].(string); ok && rawURL != "" {
		if !strings.Contains(rawURL, "://") {
			targetVIP = rawURL
		} else {
			u, err := url.Parse(rawURL)
			if err == nil && u.Host != "" {
				targetVIP = u.Host
			} else {
				targetVIP = rawURL
			}
		}
	}

	return cfg, targetVIP
}

func getFloat(m map[string]any, key string) float64 {
	switch n := m[key].(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		if f, err := strconv.ParseFloat(n, 64); err == nil {
			return f
		}
	}
	return 0
}

func getInt(m map[string]any, key string) int {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case int64:
		return int(n)
	case string:
		var parsed int
		if _, err := fmt.Sscanf(n, "%d", &parsed); err == nil {
			return parsed
		}
	}
	return 0
}
