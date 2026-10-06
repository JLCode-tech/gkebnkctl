package forge

// proxy.go — discover and list ProxyDeployment records for a benchmark target.
//
// Forge API paths:
//   POST /api/benchmarks/targets/{target_id}/discover-proxies
//     → scans the live cluster for running proxies; auto-creates
//       ProxyDeployment records with status="discovered". Best-effort:
//       only returns real records against a live cluster.
//   GET  /api/benchmarks/targets/{target_id}/proxies
//     → returns list[ProxyDeploymentResponse] (bare JSON array).
//
// Valid proxy types (VALID_PROXY_TYPES in benchmark_target_service.py):
//   envoy, nginx, haproxy, f5-bnk, nodeport
//
// Uses the same benchmarkHTTPDoFn transport seam as PushBenchmarkResult.
// Do NOT introduce a new HTTP client.

import (
	"context"
	"fmt"
	"strings"
)

// ValidProxyTypes is the canonical set of proxy type strings accepted by forge.
// Source of truth: bnk-forge-v2/backend/services/benchmark_target_service.py VALID_PROXY_TYPES.
// Keep in sync if forge adds new types.
var ValidProxyTypes = map[string]bool{
	"envoy":    true,
	"nginx":    true,
	"haproxy":  true,
	"f5-bnk":   true,
	"nodeport": true,
}

// IsValidProxyType reports whether s is a forge-recognised proxy type.
// Case-sensitive: forge stores types in lowercase canonical form.
func IsValidProxyType(s string) bool {
	return ValidProxyTypes[s]
}

// BenchmarkProxyEndpoint is the forge REST path segment for target proxy deployments.
// Full path: /api/benchmarks/targets/{target_id}/proxies
const BenchmarkProxyEndpoint = "/api/benchmarks/targets"

// ProxyDiscoverOptions carries parameters for the discover-proxies call.
type ProxyDiscoverOptions struct {
	// RestURL is the forge REST base URL (e.g. "http://localhost:8000").
	RestURL string
	// Creds are the forge REST login credentials.
	Creds RestCreds
	// TargetID is the forge BenchmarkTarget.id to scan proxies for.
	TargetID int
}

// ProxyDiscoveryResultItem is one entry in the discovery response results list.
// Fields mirror forge's ProxyDiscoveryResultItem schema.
type ProxyDiscoveryResultItem struct {
	ProxyType string `json:"proxy_type"`
	Found     bool   `json:"found"`
}

// ProxyDiscoveryResult is the response from POST discover-proxies.
// Only the fields needed by gkebnkctl are decoded; unknown fields are ignored.
type ProxyDiscoveryResult struct {
	TargetID        int                        `json:"target_id"`
	DiscoveredCount int                        `json:"discovered_count"`
	TotalScanned    int                        `json:"total_scanned"`
	Results         []ProxyDiscoveryResultItem `json:"results"`
}

// ProxyDeployment is a single record from GET /proxies.
// proxy_url is the in-cluster DNS address (unreachable from the jumphost).
// external_url is the NodePort/LB URL for external agents (jumphost-reachable);
// populated by forge discovery from the Service's LoadBalancer ingress or NodePort.
// Both fields decode JSON null or absent as "" (plain string is null-safe for JSON).
type ProxyDeployment struct {
	ID          int    `json:"id"`
	TargetID    int    `json:"target_id"`
	ProxyType   string `json:"proxy_type"`
	Status      string `json:"status"`
	ProxyURL    string `json:"proxy_url"`
	ExternalURL string `json:"external_url"`
}

// FindProxyDeployment returns a pointer to the ProxyDeployment whose proxy_type
// matches proxyType (case-sensitive, forge canonical form).
// Returns nil when no match is found.
func FindProxyDeployment(proxies []ProxyDeployment, proxyType string) *ProxyDeployment {
	for i := range proxies {
		if proxies[i].ProxyType == proxyType {
			return &proxies[i]
		}
	}
	return nil
}

// DiscoverProxies calls POST /api/benchmarks/targets/{target_id}/discover-proxies.
//
// Best-effort: against a live cluster this auto-creates ProxyDeployment records
// with status="discovered". Offline or against a cluster with no proxies it
// returns discovered_count=0. The caller should always follow up with
// ListProxyDeployments to read back ids — ids are NOT in the discovery response.
func DiscoverProxies(ctx context.Context, opts ProxyDiscoverOptions) (ProxyDiscoveryResult, error) {
	if opts.RestURL == "" {
		return ProxyDiscoveryResult{}, fmt.Errorf("forge.DiscoverProxies: RestURL is required")
	}
	if opts.TargetID == 0 {
		return ProxyDiscoveryResult{}, fmt.Errorf("forge.DiscoverProxies: TargetID is required")
	}

	base := strings.TrimRight(opts.RestURL, "/")

	token, err := restLogin(ctx, base, opts.Creds.restUsername(), opts.Creds.restPassword())
	if err != nil {
		return ProxyDiscoveryResult{}, fmt.Errorf("forge discover proxies: login: %w", err)
	}

	url := fmt.Sprintf("%s%s/%d/discover-proxies", base, BenchmarkProxyEndpoint, opts.TargetID)
	var result ProxyDiscoveryResult
	if err := restPost(ctx, url, token, map[string]any{}, &result); err != nil {
		return ProxyDiscoveryResult{}, fmt.Errorf("forge discover proxies: %w", err)
	}
	return result, nil
}

// ListProxyDeployments calls GET /api/benchmarks/targets/{target_id}/proxies.
//
// Returns a bare JSON array of ProxyDeployment records. The caller uses
// ResolveProxyDeploymentID to look up the id for a specific proxy_type.
func ListProxyDeployments(ctx context.Context, opts ProxyDiscoverOptions) ([]ProxyDeployment, error) {
	if opts.RestURL == "" {
		return nil, fmt.Errorf("forge.ListProxyDeployments: RestURL is required")
	}
	if opts.TargetID == 0 {
		return nil, fmt.Errorf("forge.ListProxyDeployments: TargetID is required")
	}

	base := strings.TrimRight(opts.RestURL, "/")

	token, err := restLogin(ctx, base, opts.Creds.restUsername(), opts.Creds.restPassword())
	if err != nil {
		return nil, fmt.Errorf("forge list proxy deployments: login: %w", err)
	}

	url := fmt.Sprintf("%s%s/%d/proxies", base, BenchmarkProxyEndpoint, opts.TargetID)
	var list []ProxyDeployment
	if err := restGet(ctx, url, token, &list); err != nil {
		return nil, fmt.Errorf("forge list proxy deployments: %w", err)
	}
	return list, nil
}

// ResolveProxyDeploymentID returns the id of the ProxyDeployment whose
// proxy_type matches proxyType (case-sensitive, forge canonical form).
// Returns 0 when no match is found (caller should treat as "unlinked").
func ResolveProxyDeploymentID(proxies []ProxyDeployment, proxyType string) int {
	for _, p := range proxies {
		if p.ProxyType == proxyType {
			return p.ID
		}
	}
	return 0
}
