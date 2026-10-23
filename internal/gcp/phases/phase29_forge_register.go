package phases

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/JLCode-tech/gkebnkctl/internal/forge"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/kubeconfig"
)

// forgeRegisterBackoffs is the exponential backoff schedule used by Phase29ForgeRegister.
var forgeRegisterBackoffs = []time.Duration{1 * time.Second, 3 * time.Second, 9 * time.Second, 27 * time.Second}

// Phase29ForgeRegister registers the GKE cluster with BNK-Forge during gkebnkctl up.
func Phase29ForgeRegister(ctx context.Context, cl *intent.Cluster, st *state.State, _ *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 29] forge-register: cluster=%s\n", cl.Metadata.Name)

	if cl.Forge == nil || !cl.Forge.Enabled {
		fmt.Fprintln(os.Stderr, "[phase 29] skipped: forge disabled")
		return nil
	}

	restURL := cl.Forge.ResolveURL()
	mcpURL := cl.Forge.MCPURL
	if mcpURL == "" {
		mcpURL = forge.DefaultMCPURL
	}

	if dryRun {
		fmt.Fprintf(os.Stderr, "[phase 29] [dry-run] would register cluster %s with Forge (%s / %s)\n",
			cl.Metadata.Name, mcpURL, restURL)
		return nil
	}

	// 1) Check existing link
	workspaceDir := cl.StateDir()
	if existing, err := forge.ReadLink(workspaceDir); err == nil && existing.IsRegistered() {
		fmt.Fprintf(os.Stderr, "[phase 29] ✓ already registered with forge: project=%s (id=%d) cluster=%s (id=%d)\n",
			existing.ProjectName, existing.ProjectID, existing.ClusterName, existing.ClusterID)
		if st != nil {
			st.Set("FORGE_REGISTERED", "true")
		}
		return nil
	}

	// 2) Read or generate kubeconfig
	var kubeconfigBytes []byte
	if st != nil {
		kubeconfigPath := st.KubeconfigPath()
		if kubeconfigPath != "" {
			// #nosec G304 -- workspace managed path
			if b, err := os.ReadFile(kubeconfigPath); err == nil {
				kubeconfigBytes = b
			}
		}
	}
	if len(kubeconfigBytes) == 0 {
		b, err := kubeconfig.FetchAndGenerate(ctx, cl)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[phase 29] warning: generating kubeconfig for Forge registration: %v\n", err)
			return nil
		}
		kubeconfigBytes = b
	}

	region := cl.Region
	if region == "" && cl.Metadata.Zone != "" {
		region = cl.Metadata.Zone
	}

	req := forge.RegisterRequest{
		WorkspaceName:        cl.Metadata.Name,
		WorkspaceDir:         workspaceDir,
		ProjectName:          cl.Forge.ResolveProjectName(cl.Metadata.Name),
		ClusterName:          cl.Metadata.Name,
		Region:               region,
		GCPProject:           cl.Metadata.Project,
		Environment:          cl.Forge.ResolveEnvironment(),
		ProjectType:          cl.Forge.ResolveProjectType(),
		CloudProvider:        cl.Forge.ResolveCloudProvider(),
		Kubeconfig:           kubeconfigBytes,
		CredentialTemplateID: cl.Forge.CredentialTemplateID,
		PostRegisterScan:     true,
	}

	username := cl.Forge.ResolveUsername()
	password, usingDefault := cl.Forge.ResolvePassword()
	if usingDefault {
		fmt.Fprintf(os.Stderr, "[phase 29] warning: using default Forge password ('changeme'); configure forge.password or set GKEBNKCTL_FORGE_PASSWORD\n")
	}
	creds := forge.RestCreds{
		Username: username,
		Password: password,
	}

	// 3) Retry loop with exponential backoff
	var lastErr error
	for attempt := 0; attempt < len(forgeRegisterBackoffs); attempt++ {
		if attempt > 0 {
			time.Sleep(forgeRegisterBackoffs[attempt-1])
		}

		// Try MCP first
		mcpClient := forge.NewClient(mcpURL)
		res, mcpErr := forge.Register(ctx, mcpClient, req)
		if mcpErr == nil {
			fmt.Fprintf(os.Stderr, "[phase 29] ✓ registered with forge via MCP: project=%s (id=%d) cluster=%s (id=%d)\n",
				res.Link.ProjectName, res.Link.ProjectID, res.Link.ClusterName, res.Link.ClusterID)
			if st != nil {
				st.Set("FORGE_REGISTERED", "true")
			}
			return nil
		}

		// If MCP failed with catalog gap or connection failure, try REST fallback
		lastErr = mcpErr
		restRes, restErr := forge.RegisterREST(ctx, restURL, req, creds)
		if restErr == nil {
			fmt.Fprintf(os.Stderr, "[phase 29] ✓ registered with forge via REST: project=%s (id=%d) cluster=%s (id=%d)\n",
				restRes.Link.ProjectName, restRes.Link.ProjectID, restRes.Link.ClusterName, restRes.Link.ClusterID)
			if st != nil {
				st.Set("FORGE_REGISTERED", "true")
			}
			return nil
		}
		lastErr = fmt.Errorf("mcp: %v; rest: %w", mcpErr, restErr)
	}

	// Soft-fail: write pending link so GKE cluster up finishes successfully
	pendingLink := &forge.Link{
		ForgeURL:     restURL,
		ForgeMCPURL:  mcpURL,
		Workspace:    cl.Metadata.Name,
		ProjectName:  cl.Forge.ResolveProjectName(cl.Metadata.Name),
		ClusterName:  cl.Metadata.Name,
		RegisteredAt: time.Now().UTC(),
		Status:       "pending",
	}
	_ = forge.WriteLink(workspaceDir, pendingLink)

	fmt.Fprintf(os.Stderr, "[phase 29] ⚠️ could not register with forge: %v (marked pending; run 'gkebnkctl forge register' when forge is online)\n", lastErr)
	return nil
}

// Phase29ForgeRegisterDown unregisters the cluster from BNK-Forge during teardown.
func Phase29ForgeRegisterDown(ctx context.Context, cl *intent.Cluster, st *state.State, dryRun bool) error {
	workspaceDir := cl.StateDir()
	link, linkErr := forge.ReadLink(workspaceDir)
	forgeEnabled := cl.Forge != nil && cl.Forge.Enabled
	forgeStateRegistered := st != nil && st.Get("FORGE_REGISTERED") == "true"

	if (linkErr != nil || link == nil) && !forgeEnabled && !forgeStateRegistered {
		return nil
	}

	restURL := intent.DefaultForgeRESTURL
	mcpURL := forge.DefaultMCPURL
	var creds forge.RestCreds

	if cl.Forge != nil {
		restURL = cl.Forge.ResolveURL()
		if cl.Forge.MCPURL != "" {
			mcpURL = cl.Forge.MCPURL
		}
		username := cl.Forge.ResolveUsername()
		password, _ := cl.Forge.ResolvePassword()
		creds = forge.RestCreds{Username: username, Password: password}
	}

	fmt.Fprintf(os.Stderr, "[down] forge: unregistering cluster %s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintf(os.Stderr, "[down] [dry-run] would unregister cluster %s from Forge (%s)\n", cl.Metadata.Name, restURL)
		return nil
	}

	if linkErr == nil && link != nil {
		// Clean up benchmark artifacts first if we have cluster ID
		if link.ClusterID > 0 {
			_ = forge.DeleteAllClusterBenchmarkArtifacts(ctx, restURL, creds, link.ClusterID)
		}

		// Try unregister via MCP when link has registered IDs
		var unregErr error
		if link.IsRegistered() {
			mcpClient := forge.NewClient(mcpURL)
			unregErr = forge.Unregister(ctx, mcpClient, workspaceDir, true)
		}
		if !link.IsRegistered() || unregErr != nil {
			// Fallback to REST (handles both registered IDs and pending links by name)
			_ = forge.UnregisterREST(ctx, restURL, link, creds)
			_ = forge.RemoveLink(workspaceDir)
		}
		if st != nil {
			st.Delete("FORGE_REGISTERED")
			_ = st.Save()
		}
		fmt.Fprintf(os.Stderr, "[down] ✓ cluster %s unregistered from forge\n", cl.Metadata.Name)
		return nil
	}

	// Link file absent: attempt by-name lookup and purge via REST
	if unregErr := forge.UnregisterRESTByName(ctx, restURL, cl.Metadata.Name, creds); unregErr != nil {
		if !errors.Is(unregErr, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "[down] forge unregister by name: %v\n", unregErr)
		}
	} else {
		fmt.Fprintf(os.Stderr, "[down] ✓ cluster %s purged from forge by name\n", cl.Metadata.Name)
	}
	if st != nil {
		st.Delete("FORGE_REGISTERED")
		_ = st.Save()
	}

	return nil
}
