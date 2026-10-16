package forge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// RegisterRequest carries everything `forge register` needs from the
// caller. The caller is responsible for resolving the workspace dir and
// providing the kubeconfig — this package doesn't reach into config/.
type RegisterRequest struct {
	// ForgeRESTURL is the Forge REST base URL recorded in the link so the
	// REST-side commands (forge scan --register-targets, benchmark) find Forge
	// without flags. Optional.
	ForgeRESTURL  string
	WorkspaceName string // logical workspace label (used in project name)
	WorkspaceDir  string // filesystem dir where forge_link.json lives
	ProjectName   string // forge project name; empty → "gkebnkctl-<workspace>"
	ClusterName   string // GKE cluster name registered into forge
	Region        string // GCP region
	Kubeconfig    []byte // raw YAML for the GKE cluster

	// GCPProject is the GCP Project ID if provided.
	GCPProject string

	// Environment is the forge project environment (e.g. "dev", "staging", "prod").
	// Empty defaults to "dev".
	Environment string

	// ProjectType is the forge project type (e.g. "cloud-gcp").
	// Empty defaults to "cloud-gcp".
	ProjectType string

	// CloudProvider is the cloud provider label (e.g. "gcp").
	// Empty defaults to "gcp".
	CloudProvider string

	// CredentialTemplateID is the forge credential template ID to attach to
	// the newly-created project.
	CredentialTemplateID int

	// If true, after CreateCluster gkebnkctl calls scan_cluster +
	// bnk_health to seed forge's view and smoke-test the link.
	PostRegisterScan bool
}

// RegisterResult is what forge register returns to the CLI for display.
type RegisterResult struct {
	Link *Link
	// LinkRefreshed is true when an existing link was kept but its Forge
	// URLs were rewritten to the ones this run used.
	LinkRefreshed bool
	ScanOutput    string // raw JSON from scan_cluster (empty if skipped)
	HealthCheck   string // raw JSON from bnk_health (empty if skipped)
	ForgeURL      string
}

// Register executes the gkebnkctl→forge handoff:
//
//  1. Reuse an existing link if one exists (idempotent).
//  2. Otherwise: create_project → create_cluster.
//  3. Optionally: scan_cluster + bnk_health smoke check.
//  4. Write forge_link.json so `forge status` / `forge unregister` work.
func Register(ctx context.Context, c *Client, req RegisterRequest) (RegisterResult, error) {
	if req.WorkspaceName == "" {
		return RegisterResult{}, errors.New("forge.Register: workspace name is required")
	}
	if req.WorkspaceDir == "" {
		return RegisterResult{}, errors.New("forge.Register: workspace dir is required")
	}
	if req.ClusterName == "" {
		return RegisterResult{}, errors.New("forge.Register: cluster name is required")
	}
	if len(req.Kubeconfig) == 0 {
		return RegisterResult{}, errors.New("forge.Register: kubeconfig is empty")
	}

	if req.ProjectName == "" {
		req.ProjectName = defaultProjectName(req.WorkspaceName)
	}

	if existing, err := ReadLink(req.WorkspaceDir); err == nil {
		if existing.IsRegistered() {
			if _, gerr := c.GetCluster(ctx, existing.ClusterID); gerr != nil {
				return RegisterResult{Link: existing, ForgeURL: c.URL()},
					fmt.Errorf("workspace already linked to forge cluster_id=%d but get_cluster failed: %w",
						existing.ClusterID, gerr)
			}
			out := RegisterResult{Link: existing, ForgeURL: c.URL()}
			if refreshLinkURLs(existing, c.URL(), req.ForgeRESTURL) {
				if err := WriteLink(req.WorkspaceDir, existing); err != nil {
					return out, fmt.Errorf("refresh link URLs: %w", err)
				}
				out.LinkRefreshed = true
			}
			return out, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return RegisterResult{}, fmt.Errorf("read existing link: %w", err)
	}

	env := req.Environment
	if env == "" {
		env = "dev"
	}
	projType := req.ProjectType
	if projType == "" {
		projType = "cloud-gcp"
	}
	cloudProv := req.CloudProvider
	if cloudProv == "" {
		cloudProv = "gcp"
	}

	var projectID int
	var projectName string
	createdProject := false

	proj, err := c.CreateProject(ctx, CreateProjectRequest{
		Name:                 req.ProjectName,
		ProjectType:          projType,
		CloudProvider:        cloudProv,
		Region:               req.Region,
		GCPProject:           req.GCPProject,
		Environment:          env,
		Description:          fmt.Sprintf("Created by gkebnkctl for workspace %q", req.WorkspaceName),
		CredentialTemplateID: req.CredentialTemplateID,
	})
	if err != nil {
		errLower := strings.ToLower(err.Error())
		if strings.Contains(errLower, "already exists") || strings.Contains(errLower, "conflict") || strings.Contains(errLower, "409") {
			if plist, lerr := c.ListProjects(ctx); lerr == nil {
				for _, p := range plist.Projects {
					if strings.EqualFold(p.Name, req.ProjectName) {
						projectID = p.ID
						projectName = p.Name
						err = nil
						break
					}
				}
			}
		}
		if err != nil {
			return RegisterResult{}, fmt.Errorf("create_project: %w", err)
		}
	} else {
		projectID = proj.Project.ID
		projectName = proj.Project.Name
		createdProject = true
	}

	cluster, err := c.CreateCluster(ctx, CreateClusterRequest{
		ProjectID:     projectID,
		Name:          req.ClusterName,
		CloudProvider: cloudProv,
		Region:        req.Region,
	}, req.Kubeconfig)
	if err != nil {
		if createdProject {
			_ = c.DeleteProject(ctx, projectID, true)
		}
		return RegisterResult{}, fmt.Errorf("create_cluster: %w", err)
	}

	link := &Link{
		ForgeMCPURL:  c.URL(),
		ForgeURL:     req.ForgeRESTURL,
		ProjectID:    projectID,
		ProjectName:  projectName,
		ClusterID:    cluster.Cluster.ID,
		ClusterName:  cluster.Cluster.Name,
		RegisteredAt: time.Now().UTC(),
		Workspace:    req.WorkspaceName,
	}
	if err := WriteLink(req.WorkspaceDir, link); err != nil {
		return RegisterResult{Link: link, ForgeURL: c.URL()},
			fmt.Errorf("registration succeeded but writing forge_link.json failed: %w", err)
	}

	out := RegisterResult{Link: link, ForgeURL: c.URL()}

	if req.PostRegisterScan {
		if s, err := c.ScanCluster(ctx, cluster.Cluster.ID); err != nil {
			out.ScanOutput = "<scan_cluster failed: " + err.Error() + ">"
		} else {
			out.ScanOutput = s
		}
		if h, err := c.BNKHealth(ctx, cluster.Cluster.ID); err != nil {
			out.HealthCheck = "<bnk_health failed: " + err.Error() + ">"
		} else {
			out.HealthCheck = h
		}
	}

	return out, nil
}

// Unregister tears down the forge-side registration for a workspace.
func Unregister(ctx context.Context, c *Client, workspaceDir string, purge bool) error {
	link, err := ReadLink(workspaceDir)
	if err != nil {
		return err
	}

	if err := c.DeleteCluster(ctx, link.ClusterID); err != nil {
		if !is404(err) {
			return fmt.Errorf("delete_cluster: %w", err)
		}
	}
	if purge {
		if err := c.DeleteProject(ctx, link.ProjectID, true); err != nil && !is404(err) {
			return fmt.Errorf("delete_project: %w", err)
		}
	}
	if err := RemoveLink(workspaceDir); err != nil {
		return fmt.Errorf("remove forge_link.json: %w", err)
	}
	return nil
}

// StatusResult is what `forge status` returns.
type StatusResult struct {
	Link          *Link
	Reachable     bool
	ForgeVersion  string
	ClusterDetail string // raw get_cluster JSON
	Err           error
}

// Status returns the current state of the workspace's forge link.
func Status(ctx context.Context, c *Client, workspaceDir string) (StatusResult, error) {
	link, err := ReadLink(workspaceDir)
	if err != nil {
		return StatusResult{}, err
	}
	out := StatusResult{Link: link}

	if v, err := c.SystemVersion(ctx); err == nil {
		out.Reachable = true
		out.ForgeVersion = v
	} else {
		out.Err = fmt.Errorf("forge unreachable at %s: %w", c.URL(), err)
		return out, nil
	}
	if d, err := c.GetCluster(ctx, link.ClusterID); err == nil {
		out.ClusterDetail = d
	} else {
		out.Err = fmt.Errorf("get_cluster(%d): %w", link.ClusterID, err)
	}
	return out, nil
}

// Is404 is a heuristic for the not-found case.
func Is404(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "not_found") ||
		strings.Contains(s, "not found") ||
		strings.Contains(s, "http 404") ||
		strings.Contains(s, "\"status_code\": 404")
}

func is404(err error) bool {
	return Is404(err)
}

// refreshLinkURLs points l at the Forge endpoints in use and reports whether
// anything changed. An empty restURL leaves the recorded REST URL alone.
func refreshLinkURLs(l *Link, mcpURL, restURL string) bool {
	changed := false
	if mcpURL != "" && l.ForgeMCPURL != mcpURL {
		l.ForgeMCPURL = mcpURL
		changed = true
	}
	if restURL != "" && l.ForgeURL != restURL {
		l.ForgeURL = restURL
		changed = true
	}
	return changed
}
