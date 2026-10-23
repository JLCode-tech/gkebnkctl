package forge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/JLCode-tech/gkebnkctl/internal/forge/mcp"
)

// DefaultMCPURL is the localhost endpoint for the forge MCP server's
// Streamable HTTP transport, matching FastMCP's default.
const DefaultMCPURL = "http://localhost:8081/mcp/"

// DefaultForgeRESTURL is the fallback REST base when forge.url is not set.
const DefaultForgeRESTURL = "http://localhost:8000"

// DefaultTimeout is the per-MCP-call timeout. Tool calls proxy to forge's
// REST backend so 30s leaves plenty of headroom for GCP API roundtrips.
const DefaultTimeout = 30 * time.Second

// Client is the high-level forge API gkebnkctl uses. It wraps the
// low-level MCP client with typed Go signatures for each tool we touch.
type Client struct {
	mcp *mcp.Client
	url string
}

// NewClient constructs a Client targeting the given MCP endpoint.
// Pass "" to use [DefaultMCPURL], or the GKEBNKCTL_FORGE_MCP_URL / BNKCTL_FORGE_MCP_URL env var
// when set.
func NewClient(endpoint string) *Client {
	if endpoint == "" {
		for _, k := range []string{
			"BNKCTL_FORGE_MCP_URL",
			"GKEBNKCTL_FORGE_MCP_URL",
			"AWSBNKCTL_FORGE_MCP_URL",
			"AZRBNKCTL_FORGE_MCP_URL",
		} {
			if v := os.Getenv(k); v != "" {
				endpoint = v
				break
			}
		}
	}
	if endpoint == "" {
		endpoint = DefaultMCPURL
	}
	return &Client{
		mcp: mcp.New(endpoint, DefaultTimeout),
		url: endpoint,
	}
}

// URL reports the MCP endpoint the client targets.
func (c *Client) URL() string { return c.url }

// ----------------------------------------------------------------------
// System / health
// ----------------------------------------------------------------------

// SystemHealth pings forge's system_health MCP tool. Returns the raw JSON
// payload as a string for caller display.
func (c *Client) SystemHealth(ctx context.Context) (string, error) {
	r, err := c.mcp.CallTool(ctx, "system_health", nil)
	if err != nil {
		return "", err
	}
	return r.Text(), nil
}

// SystemVersion returns the forge version string.
func (c *Client) SystemVersion(ctx context.Context) (string, error) {
	r, err := c.mcp.CallTool(ctx, "system_version", nil)
	if err != nil {
		return "", err
	}
	return r.Text(), nil
}

// ----------------------------------------------------------------------
// Projects
// ----------------------------------------------------------------------

// CreateProjectRequest mirrors the create_project MCP tool's argument
// shape. Zero-valued fields are dropped before the call.
type CreateProjectRequest struct {
	Name                  string
	Description           string
	ProjectType           string // e.g. "cloud-gcp"
	CloudProvider         string // e.g. "gcp"
	Environment           string // default "dev"
	Region                string
	GCPProject            string
	BackendType           string // default "local"
	Color                 string
	Icon                  string
	CredentialTemplateID  int
	TargetPlatformProfile string
}

// CreateProjectResponse captures the fields gkebnkctl actually reads
// from forge's project-mutation response.
type CreateProjectResponse struct {
	Project struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"project"`
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// UnmarshalJSON implements json.Unmarshaler. It parses both the flat shape
// forge currently emits and the legacy nested shape.
func (r *CreateProjectResponse) UnmarshalJSON(b []byte) error {
	type alias CreateProjectResponse
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*r = CreateProjectResponse(a)

	var flat struct {
		ProjectID int    `json:"project_id"`
		ID        int    `json:"id"`
		Name      string `json:"name"`
	}
	if err := json.Unmarshal(b, &flat); err != nil {
		return err
	}
	if flat.ProjectID != 0 {
		r.Project.ID = flat.ProjectID
	} else if flat.ID != 0 {
		r.Project.ID = flat.ID
	}
	if r.Project.Name == "" && flat.Name != "" {
		r.Project.Name = flat.Name
	}
	return nil
}

// CreateProject creates a forge project. Returns the ID forge assigned.
func (c *Client) CreateProject(ctx context.Context, req CreateProjectRequest) (CreateProjectResponse, error) {
	args := map[string]any{"name": req.Name}
	addStr := func(k, v string) {
		if v != "" {
			args[k] = v
		}
	}
	addStr("description", req.Description)
	addStr("project_type", req.ProjectType)
	addStr("cloud_provider", req.CloudProvider)
	addStr("environment", req.Environment)
	addStr("region", req.Region)
	addStr("gcp_project", req.GCPProject)
	addStr("backend_type", req.BackendType)
	addStr("color", req.Color)
	addStr("icon", req.Icon)
	addStr("target_platform_profile", req.TargetPlatformProfile)
	if req.CredentialTemplateID != 0 {
		args["credential_template_id"] = req.CredentialTemplateID
	}

	r, err := c.mcp.CallTool(ctx, "create_project", args)
	if err != nil {
		return CreateProjectResponse{}, err
	}
	var out CreateProjectResponse
	if err := r.UnmarshalToolJSON(&out); err != nil {
		if strings.Contains(r.Text(), "already exists") || strings.Contains(r.Text(), "PROJECT_CONFLICT") {
			if existing, findErr := c.FindProjectByName(ctx, req.Name); findErr == nil {
				return existing, nil
			}
		}
		return CreateProjectResponse{}, err
	}
	if out.Project.ID == 0 {
		if strings.Contains(r.Text(), "already exists") || strings.Contains(r.Text(), "PROJECT_CONFLICT") {
			if existing, findErr := c.FindProjectByName(ctx, req.Name); findErr == nil {
				return existing, nil
			}
		}
		return out, fmt.Errorf("create_project succeeded but no project ID in response: %s", r.Text())
	}
	return out, nil
}

// ListProjectsResponse captures the list_projects MCP tool response.
type ListProjectsResponse struct {
	Projects []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"projects"`
}

// UnmarshalJSON handles both {"projects": [...]} envelopes and bare [...] arrays.
func (r *ListProjectsResponse) UnmarshalJSON(b []byte) error {
	type projectItem struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	var env struct {
		Projects []projectItem `json:"projects"`
	}
	if err := json.Unmarshal(b, &env); err == nil && env.Projects != nil {
		r.Projects = make([]struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}, len(env.Projects))
		for i, p := range env.Projects {
			r.Projects[i].ID = p.ID
			r.Projects[i].Name = p.Name
		}
		return nil
	}
	var arr []projectItem
	if err := json.Unmarshal(b, &arr); err == nil {
		r.Projects = make([]struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}, len(arr))
		for i, p := range arr {
			r.Projects[i].ID = p.ID
			r.Projects[i].Name = p.Name
		}
		return nil
	}
	return fmt.Errorf("decode list_projects: %s", string(b))
}

// ListProjects returns all projects visible in forge.
func (c *Client) ListProjects(ctx context.Context) (ListProjectsResponse, error) {
	res, err := c.mcp.CallTool(ctx, "list_projects", map[string]any{})
	if err != nil {
		return ListProjectsResponse{}, err
	}
	var out ListProjectsResponse
	if err := res.UnmarshalToolJSON(&out); err != nil {
		return ListProjectsResponse{}, err
	}
	return out, nil
}

// FindProjectByName queries list_projects MCP tool and matches by name.
func (c *Client) FindProjectByName(ctx context.Context, name string) (CreateProjectResponse, error) {
	data, err := c.ListProjects(ctx)
	if err != nil {
		return CreateProjectResponse{}, err
	}
	for _, p := range data.Projects {
		if p.Name == name || strings.EqualFold(p.Name, name) {
			var resp CreateProjectResponse
			resp.Success = true
			resp.Project.ID = p.ID
			resp.Project.Name = p.Name
			return resp, nil
		}
	}
	return CreateProjectResponse{}, fmt.Errorf("project %q not found", name)
}

// DeleteProject removes a project. force bypasses the active/only-project
// safety check on forge's side.
func (c *Client) DeleteProject(ctx context.Context, projectID int, force bool) error {
	args := map[string]any{"project_id": projectID, "force": force, "confirm": true}
	_, err := c.mcp.CallTool(ctx, "delete_project", args)
	return err
}

// ----------------------------------------------------------------------
// Clusters
// ----------------------------------------------------------------------

// CreateClusterRequest mirrors the create_cluster MCP tool's argument shape.
type CreateClusterRequest struct {
	ProjectID        int
	Name             string
	Kubeconfig       string // base64-encoded YAML
	CloudProvider    string // "gcp" for GKE
	Region           string
	Context          string
	DefaultNamespace string
}

// CreateClusterResponse captures the fields gkebnkctl reads.
type CreateClusterResponse struct {
	Cluster struct {
		ID        int    `json:"id"`
		Name      string `json:"name"`
		ProjectID int    `json:"project_id"`
	} `json:"cluster"`
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// UnmarshalJSON implements json.Unmarshaler.
func (r *CreateClusterResponse) UnmarshalJSON(b []byte) error {
	type alias CreateClusterResponse
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*r = CreateClusterResponse(a)

	var flat struct {
		ClusterID int    `json:"cluster_id"`
		ID        int    `json:"id"`
		Name      string `json:"name"`
		ProjectID int    `json:"project_id"`
	}
	if err := json.Unmarshal(b, &flat); err != nil {
		return err
	}
	if flat.ClusterID != 0 {
		r.Cluster.ID = flat.ClusterID
	} else if flat.ID != 0 {
		r.Cluster.ID = flat.ID
	}
	if r.Cluster.Name == "" && flat.Name != "" {
		r.Cluster.Name = flat.Name
	}
	if flat.ProjectID != 0 {
		r.Cluster.ProjectID = flat.ProjectID
	}
	return nil
}

// CreateCluster registers a Kubernetes cluster under a project.
func (c *Client) CreateCluster(ctx context.Context, req CreateClusterRequest, kubeconfigYAML []byte) (CreateClusterResponse, error) {
	if req.ProjectID == 0 {
		return CreateClusterResponse{}, fmt.Errorf("CreateCluster: project_id is required")
	}
	if req.Name == "" {
		return CreateClusterResponse{}, fmt.Errorf("CreateCluster: cluster name is required")
	}
	if len(kubeconfigYAML) == 0 {
		return CreateClusterResponse{}, fmt.Errorf("CreateCluster: kubeconfig is empty")
	}
	encoded := base64.StdEncoding.EncodeToString(kubeconfigYAML)

	args := map[string]any{
		"project_id": req.ProjectID,
		"name":       req.Name,
		"kubeconfig": encoded,
	}
	if req.CloudProvider != "" {
		args["cloud_provider"] = req.CloudProvider
	}
	if req.Region != "" {
		args["region"] = req.Region
	}
	if req.Context != "" {
		args["context"] = req.Context
	}
	if req.DefaultNamespace != "" {
		args["default_namespace"] = req.DefaultNamespace
	}

	r, err := c.mcp.CallTool(ctx, "create_cluster", args)
	if err != nil {
		return CreateClusterResponse{}, err
	}
	var out CreateClusterResponse
	if err := r.UnmarshalToolJSON(&out); err != nil {
		return CreateClusterResponse{}, err
	}
	if out.Cluster.ID == 0 {
		return out, fmt.Errorf("create_cluster succeeded but no cluster ID in response: %s", r.Text())
	}
	return out, nil
}

// GetCluster fetches a cluster's current state from forge.
func (c *Client) GetCluster(ctx context.Context, clusterID int) (string, error) {
	args := map[string]any{"cluster_id": clusterID}
	r, err := c.mcp.CallTool(ctx, "get_cluster", args)
	if err != nil {
		return "", err
	}
	return r.Text(), nil
}

// ScanCluster triggers forge's post-registration scan.
func (c *Client) ScanCluster(ctx context.Context, clusterID int) (string, error) {
	args := map[string]any{"cluster_id": clusterID}
	r, err := c.mcp.CallTool(ctx, "scan_cluster", args)
	if err != nil {
		return "", err
	}
	return r.Text(), nil
}

// ListClustersResponse captures clusters returned by the list_clusters MCP tool.
type ListClustersResponse struct {
	Clusters []struct {
		ID        int    `json:"id"`
		Name      string `json:"name"`
		ProjectID int    `json:"project_id"`
	} `json:"clusters"`
}

// UnmarshalJSON handles both {"clusters": [...]} envelopes and bare [...] arrays.
func (r *ListClustersResponse) UnmarshalJSON(b []byte) error {
	type clusterItem struct {
		ID        int    `json:"id"`
		Name      string `json:"name"`
		ProjectID int    `json:"project_id"`
	}
	var env struct {
		Clusters []clusterItem `json:"clusters"`
	}
	if err := json.Unmarshal(b, &env); err == nil && env.Clusters != nil {
		r.Clusters = make([]struct {
			ID        int    `json:"id"`
			Name      string `json:"name"`
			ProjectID int    `json:"project_id"`
		}, len(env.Clusters))
		for i, cl := range env.Clusters {
			r.Clusters[i].ID = cl.ID
			r.Clusters[i].Name = cl.Name
			r.Clusters[i].ProjectID = cl.ProjectID
		}
		return nil
	}
	var arr []clusterItem
	if err := json.Unmarshal(b, &arr); err == nil {
		r.Clusters = make([]struct {
			ID        int    `json:"id"`
			Name      string `json:"name"`
			ProjectID int    `json:"project_id"`
		}, len(arr))
		for i, cl := range arr {
			r.Clusters[i].ID = cl.ID
			r.Clusters[i].Name = cl.Name
			r.Clusters[i].ProjectID = cl.ProjectID
		}
		return nil
	}
	return fmt.Errorf("decode list_clusters: %s", string(b))
}

// ListClusters queries forge's list_clusters MCP tool for a project.
func (c *Client) ListClusters(ctx context.Context, projectID int) (ListClustersResponse, error) {
	args := map[string]any{}
	if projectID > 0 {
		args["project_id"] = projectID
	}
	r, err := c.mcp.CallTool(ctx, "list_clusters", args)
	if err != nil {
		return ListClustersResponse{}, err
	}
	var out ListClustersResponse
	if err := r.UnmarshalToolJSON(&out); err != nil {
		return ListClustersResponse{}, err
	}
	return out, nil
}

// DeleteCluster removes the cluster registration from forge.
func (c *Client) DeleteCluster(ctx context.Context, clusterID int) error {
	args := map[string]any{"cluster_id": clusterID, "confirm": true}
	_, err := c.mcp.CallTool(ctx, "delete_cluster", args)
	return err
}

// BNKHealth queries the BNK install on the registered cluster.
func (c *Client) BNKHealth(ctx context.Context, clusterID int) (string, error) {
	args := map[string]any{"cluster_id": clusterID}
	r, err := c.mcp.CallTool(ctx, "bnk_health", args)
	if err != nil {
		return "", err
	}
	return r.Text(), nil
}
