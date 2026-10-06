package forge

// cleanup.go — delete forge benchmark artifacts created by gkebnkctl on `down`
// and via the explicit `gkebnkctl forge cleanup` subcommand.
//
// TWO cleanup scopes:
//
//   - DeleteClusterBenchmarkArtifacts (down path): deletes cluster-scoped
//     records plus instance-scoped records. Delete order:
//       1. Proxy deployments per target
//       2. Targets (cluster_id-scoped)
//       3. BenchmarkAgent named "gkebnkctl-jumphost-<instanceID>" (EXACT match)
//       4. SSHCredential named "gkebnkctl-jumphost-<instanceID>" (EXACT match)
//     Steps 3 and 4 are skipped when jumphostInstanceID is empty (FORGE_CLUSTER_ID
//     present but jumphost not yet created). Configs are NOT deleted — they are
//     idempotent-on-create (upsert), accumulate harmlessly, and have no cluster or
//     instance identity.
//
//   - DeleteAllClusterBenchmarkArtifacts (forge cleanup subcommand): same as
//     above PLUS deletes ALL gkebnkctl-prefixed agents, SSH credentials, and
//     configs. This is an explicit operator action; the broader name-match is
//     acceptable.
//
// All delete methods are idempotent: 404 → nil.
// Partial failures are collected and returned after all items are attempted so a
// single bad record does not block the rest of teardown.
// Run/result rows are never touched; forge nulls their FKs via SET NULL
// (models/benchmark.py:62-69).

import (
	"context"
	"fmt"
	"strings"
)

const benchmarkArtifactPrefix = "gkebnkctl-"

// ProxyDeploymentResponse is the subset of a forge ProxyDeployment record
// that the cleanup path needs.
type ProxyDeploymentResponse struct {
	ID int `json:"id"`
}

// ListTargetsByClusterID returns all BenchmarkTarget records whose cluster_id
// matches clusterID. It GETs /api/benchmarks/targets and filters client-side.
//
// The response shape of GET /api/benchmarks/targets is a plain JSON array
// (confirmed by benchmarkTargetFindByName in target.go, which decodes the same
// endpoint as []BenchmarkTargetResponse). As a defensive fallback the decoder
// also accepts the wrapped shape {"targets": [...], "total": N} so that a future
// forge server-side change does not silently orphan records.
func ListTargetsByClusterID(ctx context.Context, restURL string, creds RestCreds, clusterID int) ([]BenchmarkTargetResponse, error) {
	base := strings.TrimRight(restURL, "/")
	token, err := restLogin(ctx, base, creds.restUsername(), creds.restPassword())
	if err != nil {
		return nil, fmt.Errorf("forge.ListTargetsByClusterID: login: %w", err)
	}
	url := base + BenchmarkTargetEndpoint

	// Try the plain-array shape first — this matches what forge currently returns
	// and what benchmarkTargetFindByName (target.go) uses.
	var list []BenchmarkTargetResponse
	if err := restGet(ctx, url, token, &list); err != nil {
		return nil, fmt.Errorf("forge.ListTargetsByClusterID: %w", err)
	}

	// If the plain decode returned a non-nil but empty result, also try the
	// wrapped shape {"targets": [...]} in case forge changed its schema.
	// A non-empty bare decode wins unconditionally.
	if len(list) == 0 {
		var wrapped struct {
			Targets []BenchmarkTargetResponse `json:"targets"`
		}
		// Ignore error — if this also fails we just return the empty slice below.
		_ = restGet(ctx, url, token, &wrapped)
		if len(wrapped.Targets) > 0 {
			list = wrapped.Targets
		}
	}

	// Filter client-side by cluster_id.
	var out []BenchmarkTargetResponse
	for _, t := range list {
		if t.ClusterID == clusterID {
			out = append(out, t)
		}
	}
	return out, nil
}

// ListProxiesForTarget returns the ProxyDeployment records for a target.
// Uses GET /api/benchmarks/targets/{targetID}/proxies.
// Forge returns a bare array for this endpoint.
func ListProxiesForTarget(ctx context.Context, restURL string, creds RestCreds, targetID int) ([]ProxyDeploymentResponse, error) {
	base := strings.TrimRight(restURL, "/")
	token, err := restLogin(ctx, base, creds.restUsername(), creds.restPassword())
	if err != nil {
		return nil, fmt.Errorf("forge.ListProxiesForTarget: login: %w", err)
	}
	url := fmt.Sprintf("%s%s/%d/proxies", base, BenchmarkTargetEndpoint, targetID)
	// forge returns a bare array for this endpoint.
	var list []ProxyDeploymentResponse
	if err := restGet(ctx, url, token, &list); err != nil {
		return nil, fmt.Errorf("forge.ListProxiesForTarget: %w", err)
	}
	return list, nil
}

// DeleteProxyDeployment deletes one proxy deployment record. 404 → nil.
func DeleteProxyDeployment(ctx context.Context, restURL string, creds RestCreds, targetID, proxyID int) error {
	base := strings.TrimRight(restURL, "/")
	token, err := restLogin(ctx, base, creds.restUsername(), creds.restPassword())
	if err != nil {
		return fmt.Errorf("forge.DeleteProxyDeployment: login: %w", err)
	}
	url := fmt.Sprintf("%s%s/%d/proxies/%d", base, BenchmarkTargetEndpoint, targetID, proxyID)
	if err := restDelete(ctx, url, token); err != nil && !is404(err) {
		return fmt.Errorf("forge.DeleteProxyDeployment: %w", err)
	}
	return nil
}

// DeleteBenchmarkTarget deletes a BenchmarkTarget by ID. 404 → nil.
func DeleteBenchmarkTarget(ctx context.Context, restURL string, creds RestCreds, targetID int) error {
	base := strings.TrimRight(restURL, "/")
	token, err := restLogin(ctx, base, creds.restUsername(), creds.restPassword())
	if err != nil {
		return fmt.Errorf("forge.DeleteBenchmarkTarget: login: %w", err)
	}
	url := fmt.Sprintf("%s%s/%d", base, BenchmarkTargetEndpoint, targetID)
	if err := restDelete(ctx, url, token); err != nil && !is404(err) {
		return fmt.Errorf("forge.DeleteBenchmarkTarget: %w", err)
	}
	return nil
}

// DeleteBenchmarkAgent deletes a BenchmarkAgent by ID. 404 → nil.
func DeleteBenchmarkAgent(ctx context.Context, restURL string, creds RestCreds, agentID int) error {
	base := strings.TrimRight(restURL, "/")
	token, err := restLogin(ctx, base, creds.restUsername(), creds.restPassword())
	if err != nil {
		return fmt.Errorf("forge.DeleteBenchmarkAgent: login: %w", err)
	}
	url := fmt.Sprintf("%s%s/%d", base, BenchmarkAgentEndpoint, agentID)
	if err := restDelete(ctx, url, token); err != nil && !is404(err) {
		return fmt.Errorf("forge.DeleteBenchmarkAgent: %w", err)
	}
	return nil
}

// DeleteBenchmarkConfig deletes a BenchmarkConfig by ID. 404 → nil.
func DeleteBenchmarkConfig(ctx context.Context, restURL string, creds RestCreds, configID int) error {
	base := strings.TrimRight(restURL, "/")
	token, err := restLogin(ctx, base, creds.restUsername(), creds.restPassword())
	if err != nil {
		return fmt.Errorf("forge.DeleteBenchmarkConfig: login: %w", err)
	}
	url := fmt.Sprintf("%s%s/%d", base, BenchmarkConfigEndpoint, configID)
	if err := restDelete(ctx, url, token); err != nil && !is404(err) {
		return fmt.Errorf("forge.DeleteBenchmarkConfig: %w", err)
	}
	return nil
}

// DeleteSSHCredential deletes a forge SSHCredential by ID. 404 → nil.
// 409 (FK conflict from a project referencing this credential) is treated as a
// soft warning: the error is returned so callers can collect it, but teardown
// continues — the credential may have already been reassigned to another project.
func DeleteSSHCredential(ctx context.Context, restURL string, creds RestCreds, credID int) error {
	base := strings.TrimRight(restURL, "/")
	token, err := restLogin(ctx, base, creds.restUsername(), creds.restPassword())
	if err != nil {
		return fmt.Errorf("forge.DeleteSSHCredential: login: %w", err)
	}
	url := fmt.Sprintf("%s%s/%d", base, SSHCredentialEndpoint, credID)
	dErr := restDelete(ctx, url, token)
	if dErr == nil || is404(dErr) {
		return nil
	}
	// 409 = FK conflict (a project still references this credential).
	// Treat as a soft warning (collected by callers), not a fatal error.
	return fmt.Errorf("forge.DeleteSSHCredential id=%d: %w", credID, dErr)
}

// DeleteClusterBenchmarkArtifacts removes benchmark artifacts created by
// gkebnkctl for the given forge cluster ID. This is the down-path cleanup.
// Delete order:
//
//  1. Proxy deployments per target (cluster-scoped via target → cluster_id)
//  2. Targets (cluster-scoped — only records with cluster_id == clusterID)
//  3. BenchmarkAgent named "gkebnkctl-jumphost-<jumphostInstanceID>" (EXACT)
//  4. SSHCredential named "gkebnkctl-jumphost-<jumphostInstanceID>" (EXACT)
//
// Steps 3 and 4 are skipped when jumphostInstanceID is empty — the caller logs
// a notice in that case. Configs are deferred to the full purge
// (DeleteAllClusterBenchmarkArtifacts via `gkebnkctl forge cleanup`).
//
// Run/result rows are never deleted. Forge nulls their FKs via SET NULL.
//
// All delete operations are idempotent (404 = already gone = success).
// Partial failures are collected; the function returns an error after attempting
// all items so a single bad record does not block the rest of teardown.
//
// Returns an error on login failure or delete failure; the caller
// logs and discards the error to soft-fail.
func DeleteClusterBenchmarkArtifacts(ctx context.Context, restURL string, creds RestCreds, clusterID int, jumphostInstanceID string) error {
	var errs []string

	if err := deleteTargetsAndProxies(ctx, restURL, creds, clusterID); err != nil {
		errs = append(errs, err.Error())
	}

	if jumphostInstanceID == "" {
		// No instance ID: skip agent + ssh credential deletion.
		if len(errs) > 0 {
			return fmt.Errorf("forge benchmark cleanup: %s", strings.Join(errs, "; "))
		}
		return nil
	}

	exactName := fmt.Sprintf("gkebnkctl-jumphost-%s", jumphostInstanceID)

	base := strings.TrimRight(restURL, "/")
	token, loginErr := restLogin(ctx, base, creds.restUsername(), creds.restPassword())
	if loginErr != nil {
		errs = append(errs, fmt.Sprintf("login for agent/ssh-credential: %v", loginErr))
		return fmt.Errorf("forge benchmark cleanup: %s", strings.Join(errs, "; "))
	}

	// Step 3: delete the exact-name BenchmarkAgent.
	agent, agentErr := benchmarkAgentFindByName(ctx, base, token, exactName)
	if agentErr == nil {
		url := fmt.Sprintf("%s%s/%d", base, BenchmarkAgentEndpoint, agent.ID)
		if dErr := restDelete(ctx, url, token); dErr != nil && !is404(dErr) {
			errs = append(errs, fmt.Sprintf("delete agent %d (%s): %v", agent.ID, exactName, dErr))
		}
	} else if !strings.Contains(agentErr.Error(), "not found") {
		errs = append(errs, fmt.Sprintf("find agent %s: %v", exactName, agentErr))
	}
	// "not found" = agent already gone or never created → treat as success.

	// Step 4: delete the exact-name SSHCredential.
	sshCred, sshErr := sshCredFindByName(ctx, base, token, exactName)
	if sshErr == nil {
		url := fmt.Sprintf("%s%s/%d", base, SSHCredentialEndpoint, sshCred.ID)
		if dErr := restDelete(ctx, url, token); dErr != nil && !is404(dErr) {
			// 409 = FK conflict from project referencing this credential: soft warning.
			errs = append(errs, fmt.Sprintf("delete ssh-credential %d (%s): %v", sshCred.ID, exactName, dErr))
		}
	} else if !strings.Contains(sshErr.Error(), "not found") {
		errs = append(errs, fmt.Sprintf("find ssh-credential %s: %v", exactName, sshErr))
	}
	// "not found" = credential already gone or never created → treat as success.

	if len(errs) > 0 {
		return fmt.Errorf("forge benchmark cleanup: %s", strings.Join(errs, "; "))
	}
	return nil
}

// DeleteAllClusterBenchmarkArtifacts is the full purge used by the explicit
// `gkebnkctl forge cleanup` subcommand. It deletes:
//
//  1. Proxy deployments per target
//  2. Targets (cluster-scoped)
//  3. All agents whose name starts with "gkebnkctl-"
//  4. All SSH credentials whose name starts with "gkebnkctl-"
//  5. All configs whose name starts with "gkebnkctl-"
//
// The broader name-match for agents, ssh credentials, and configs is acceptable
// here because this is an explicit operator action, not an automatic down-path
// side-effect.
//
// Run/result rows are never deleted. Forge nulls their FKs via SET NULL.
// All operations are idempotent (404 = already gone = success).
// 409 on ssh-credential delete (FK conflict from a project) is a soft warning.
//
// Returns an error on login failure or delete failure; callers should surface
// errors to the operator.
func DeleteAllClusterBenchmarkArtifacts(ctx context.Context, restURL string, creds RestCreds, clusterID int) error {
	var errs []string

	if err := deleteTargetsAndProxies(ctx, restURL, creds, clusterID); err != nil {
		errs = append(errs, err.Error())
	}

	// ── Agents + ssh-credentials + configs: fresh login for direct token use ──
	base := strings.TrimRight(restURL, "/")
	token, loginErr := restLogin(ctx, base, creds.restUsername(), creds.restPassword())
	if loginErr != nil {
		errs = append(errs, fmt.Sprintf("login for agents/ssh-credentials/configs: %v", loginErr))
		return fmt.Errorf("forge benchmark full cleanup: %s", strings.Join(errs, "; "))
	}

	// ── Step 3: agents created by gkebnkctl (name prefix "gkebnkctl-") ────────
	var agents []BenchmarkAgentResponse
	if gErr := restGet(ctx, base+BenchmarkAgentEndpoint, token, &agents); gErr != nil {
		errs = append(errs, fmt.Sprintf("list agents: %v", gErr))
	} else {
		for _, a := range agents {
			if !strings.HasPrefix(a.Name, benchmarkArtifactPrefix) {
				continue
			}
			url := fmt.Sprintf("%s%s/%d", base, BenchmarkAgentEndpoint, a.ID)
			if dErr := restDelete(ctx, url, token); dErr != nil && !is404(dErr) {
				errs = append(errs, fmt.Sprintf("delete agent %d (%s): %v", a.ID, a.Name, dErr))
			}
		}
	}

	// ── Step 4: SSH credentials created by gkebnkctl (name prefix "gkebnkctl-") ─
	var sshCreds []AccessMethodResponse
	if gErr := restGet(ctx, base+SSHCredentialEndpoint, token, &sshCreds); gErr != nil {
		errs = append(errs, fmt.Sprintf("list ssh-credentials: %v", gErr))
	} else {
		for _, sc := range sshCreds {
			if !strings.HasPrefix(sc.Name, benchmarkArtifactPrefix) {
				continue
			}
			url := fmt.Sprintf("%s%s/%d", base, SSHCredentialEndpoint, sc.ID)
			if dErr := restDelete(ctx, url, token); dErr != nil && !is404(dErr) {
				// 409 = FK conflict; treat as soft warning (collected, not fatal).
				errs = append(errs, fmt.Sprintf("delete ssh-credential %d (%s): %v", sc.ID, sc.Name, dErr))
			}
		}
	}

	// ── Step 5: configs created by gkebnkctl (name prefix "gkebnkctl-") ───────
	var configs []BenchmarkConfigResponse
	if gErr := restGet(ctx, base+BenchmarkConfigEndpoint, token, &configs); gErr != nil {
		errs = append(errs, fmt.Sprintf("list configs: %v", gErr))
	} else {
		for _, c := range configs {
			if !strings.HasPrefix(c.Name, benchmarkArtifactPrefix) {
				continue
			}
			url := fmt.Sprintf("%s%s/%d", base, BenchmarkConfigEndpoint, c.ID)
			if dErr := restDelete(ctx, url, token); dErr != nil && !is404(dErr) {
				errs = append(errs, fmt.Sprintf("delete config %d (%s): %v", c.ID, c.Name, dErr))
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("forge benchmark full cleanup: %s", strings.Join(errs, "; "))
	}
	return nil
}

// deleteTargetsAndProxies deletes all targets for clusterID (and their proxy
// deployments) in FK-safe order. It is the shared inner function for both
// DeleteClusterBenchmarkArtifacts and DeleteAllClusterBenchmarkArtifacts.
// Partial failures are collected and returned as a single error after all
// items are attempted.
func deleteTargetsAndProxies(ctx context.Context, restURL string, creds RestCreds, clusterID int) error {
	var errs []string

	targets, err := ListTargetsByClusterID(ctx, restURL, creds, clusterID)
	if err != nil {
		return fmt.Errorf("forge benchmark cleanup: list targets: %w", err)
	}

	for _, t := range targets {
		proxies, pErr := ListProxiesForTarget(ctx, restURL, creds, t.ID)
		if pErr != nil {
			errs = append(errs, fmt.Sprintf("list proxies for target %d: %v", t.ID, pErr))
		} else {
			for _, p := range proxies {
				if dErr := DeleteProxyDeployment(ctx, restURL, creds, t.ID, p.ID); dErr != nil {
					errs = append(errs, fmt.Sprintf("delete proxy %d (target %d): %v", p.ID, t.ID, dErr))
				}
			}
		}
		if dErr := DeleteBenchmarkTarget(ctx, restURL, creds, t.ID); dErr != nil {
			errs = append(errs, fmt.Sprintf("delete target %d: %v", t.ID, dErr))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("forge benchmark cleanup: %s", strings.Join(errs, "; "))
	}
	return nil
}
