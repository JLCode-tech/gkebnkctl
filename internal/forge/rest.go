package forge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// restHTTPErr lets callers switch on the HTTP status code (e.g. POST /api/projects
// 409 → fall back to GET-and-reuse) without parsing the formatted error string.
type restHTTPErr struct {
	StatusCode int
	URL        string
	Body       string
}

func (e *restHTTPErr) Error() string {
	return fmt.Sprintf("http %d from %s: %s", e.StatusCode, e.URL, e.Body)
}

// RestCreds carries the forge REST login credentials. Both fields are resolved
// by the caller (intent.ForgeSpec.ResolveUsername / ResolvePassword) before
// this package is invoked — rest.go does not read env or cluster.yaml directly.
//
// When Username is empty, "admin" is used. When Password is empty, "changeme"
// is used (the caller should have already warned the user if the default is
// in use). Zero value = all defaults, matching pre-credential-feature behaviour.
type RestCreds struct {
	Username string
	Password string
}

// restUsername returns the effective username — "admin" when unset.
func (c RestCreds) restUsername() string {
	if c.Username != "" {
		return c.Username
	}
	return "admin"
}

// restPassword returns the effective password — "changeme" when unset.
func (c RestCreds) restPassword() string {
	if c.Password != "" {
		return c.Password
	}
	return "changeme"
}

// benchmarkHTTPDoFn is the single injectable HTTP transport seam used by all
// forge REST operations (lifecycle, agents, targets, proxies, configs, results).
// Default: http.DefaultClient.Do. Tests replace it via the BenchmarkHTTPDoFn
// export in benchmark_export_test.go.
var benchmarkHTTPDoFn func(*http.Request) (*http.Response, error) = http.DefaultClient.Do

// isConflictHTTP returns true if err represents an HTTP 409 Conflict
// or HTTP 400 Bad Request with "already exists" in the response body.
func isConflictHTTP(err error) bool {
	var herr *restHTTPErr
	if !errors.As(err, &herr) {
		return false
	}
	return herr.StatusCode == http.StatusConflict ||
		(herr.StatusCode == http.StatusBadRequest && strings.Contains(herr.Body, "already exists"))
}

// RegisterREST mirrors Register's shape but uses forge's REST API instead of
// MCP. Used as a fallback when the MCP catalog does not expose create_project
// or create_cluster.
//
// Credentials are resolved by the caller via intent.ForgeSpec.ResolveUsername /
// ResolvePassword and passed in as creds. Pass a zero RestCreds for
// back-compat default behaviour (admin/changeme).
func RegisterREST(ctx context.Context, restURL string, req RegisterRequest, creds RestCreds) (RegisterResult, error) {
	if req.WorkspaceName == "" {
		return RegisterResult{}, fmt.Errorf("forge.RegisterREST: workspace name is required")
	}
	if req.WorkspaceDir == "" {
		return RegisterResult{}, fmt.Errorf("forge.RegisterREST: workspace dir is required")
	}
	if req.ClusterName == "" {
		return RegisterResult{}, fmt.Errorf("forge.RegisterREST: cluster name is required")
	}
	if len(req.Kubeconfig) == 0 {
		return RegisterResult{}, fmt.Errorf("forge.RegisterREST: kubeconfig is empty")
	}
	if req.ProjectName == "" {
		req.ProjectName = defaultProjectName(req.WorkspaceName)
	}

	base := strings.TrimRight(restURL, "/")

	token, err := restLogin(ctx, base, creds.restUsername(), creds.restPassword())
	if err != nil {
		return RegisterResult{}, fmt.Errorf("forge REST login: %w", err)
	}

	proj, err := restCreateProject(ctx, base, token, req)
	if err != nil {
		return RegisterResult{}, fmt.Errorf("forge REST create project: %w", err)
	}

	cluster, err := restCreateCluster(ctx, base, token, proj.ID, req)
	if err != nil {
		// Best-effort rollback.
		_ = restDeleteProject(ctx, base, token, proj.ID)
		return RegisterResult{}, fmt.Errorf("forge REST create cluster: %w", err)
	}

	link := &Link{
		ForgeURL:     restURL,
		ProjectID:    proj.ID,
		ProjectName:  proj.Name,
		ClusterID:    cluster.ID,
		ClusterName:  cluster.Name,
		RegisteredAt: time.Now().UTC(),
		Workspace:    req.WorkspaceName,
		Status:       "registered",
	}
	if err := WriteLink(req.WorkspaceDir, link); err != nil {
		return RegisterResult{Link: link, ForgeURL: restURL},
			fmt.Errorf("registration succeeded but writing forge_link.json failed: %w", err)
	}
	return RegisterResult{Link: link, ForgeURL: restURL}, nil
}

// UnregisterREST tears down the forge-side registration via REST: it deletes
// the cluster record AND the project shell (the project is created by
// registration and named for the cluster — after down nothing should remain).
// Tolerates 404 responses (operator may have cleaned up via forge UI).
// creds carries the forge login credentials; pass a zero RestCreds for
// back-compat default behaviour (admin/changeme).
func UnregisterREST(ctx context.Context, restURL string, link *Link, creds RestCreds) error {
	if link == nil {
		return fmt.Errorf("forge.UnregisterREST: link is nil")
	}
	base := strings.TrimRight(restURL, "/")
	token, err := restLogin(ctx, base, creds.restUsername(), creds.restPassword())
	if err != nil {
		return fmt.Errorf("forge REST login: %w", err)
	}

	if err := restDeleteCluster(ctx, base, token, link.ProjectID, link.ClusterID); err != nil && !Is404(err) {
		return fmt.Errorf("forge REST delete cluster: %w", err)
	}
	if err := restDeleteProject(ctx, base, token, link.ProjectID); err != nil && !Is404(err) {
		return fmt.Errorf("forge REST delete project: %w", err)
	}
	return nil
}

// UnregisterRESTByName removes a cluster's forge registration when no local
// forge_link.json exists (e.g. the workspace state directory was lost):
// it logs in over REST, finds the project by the canonical registration name
// ("gkebnkctl-<clusterName>"), deletes the cluster record by name within it
// (tolerating absence), and deletes the project. Returns an error wrapping
// os.ErrNotExist when no such project exists forge-side.
func UnregisterRESTByName(ctx context.Context, restURL, clusterName string, creds RestCreds) error {
	base := strings.TrimRight(restURL, "/")
	token, err := restLogin(ctx, base, creds.restUsername(), creds.restPassword())
	if err != nil {
		return fmt.Errorf("forge REST login: %w", err)
	}

	proj, err := restFindProjectByName(ctx, base, token, defaultProjectName(clusterName))
	if err != nil {
		// Propagates the os.ErrNotExist wrap so callers can distinguish
		// "never registered" from "lookup failed".
		return err
	}

	cluster, err := restFindClusterByName(ctx, base, token, proj.ID, clusterName)
	switch {
	case err == nil:
		if derr := restDeleteCluster(ctx, base, token, proj.ID, cluster.ID); derr != nil && !Is404(derr) {
			return fmt.Errorf("forge REST delete cluster: %w", derr)
		}
	case errors.Is(err, os.ErrNotExist):
		// Cluster record already gone — still purge the project shell below.
	default:
		return err
	}

	if err := restDeleteProject(ctx, base, token, proj.ID); err != nil && !Is404(err) {
		return fmt.Errorf("forge REST delete project: %w", err)
	}
	return nil
}

// defaultProjectName returns the canonical forge project name gkebnkctl
// registers a workspace under. Shared by RegisterREST, Register and
// UnregisterRESTByName so the naming convention cannot drift.
func defaultProjectName(workspace string) string {
	return "gkebnkctl-" + workspace
}

// IsMCPCatalogGapErr returns true when err indicates the MCP catalog does not
// expose the requested tool — i.e., the forge MCP server is running an older
// version that pre-dates create_project / create_cluster tools.
func IsMCPCatalogGapErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "tool not found") ||
		strings.Contains(s, "unknown tool") ||
		strings.Contains(s, "method not found") ||
		strings.Contains(s, "no tool named") ||
		strings.Contains(s, "tool_not_found")
}

// ── REST helpers ──────────────────────────────────────────────────────────────

func restLogin(ctx context.Context, base, username, password string) (string, error) {
	body := map[string]string{"username": username, "password": password}
	var resp struct {
		Token string `json:"token"`
	}
	if err := restPost(ctx, base+"/api/auth/login", "", body, &resp); err != nil {
		return "", err
	}
	if resp.Token == "" {
		return "", fmt.Errorf("forge REST login: empty token in response")
	}
	return resp.Token, nil
}

type restProject struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func restCreateProject(ctx context.Context, base, token string, req RegisterRequest) (restProject, error) {
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

	body := map[string]any{
		"name":           req.ProjectName,
		"project_type":   projType,
		"cloud_provider": cloudProv,
		"region":         req.Region,
		"environment":    env,
		"description":    fmt.Sprintf("Created by gkebnkctl for workspace %q", req.WorkspaceName),
	}
	// Probe all three response shapes in the wild for POST /api/projects:
	//   A. wrapped:     {project: {id, name, ...}, success: true}
	//   B. flat-id:     {id, name, ...}
	//   C. flat-prefix: {success, project_id, name, message}
	var resp struct {
		ID        int         `json:"id"`
		ProjectID int         `json:"project_id"`
		Name      string      `json:"name"`
		Project   restProject `json:"project"`
		Success   bool        `json:"success"`
	}
	err := restPost(ctx, base+"/api/projects", token, body, &resp)
	if err != nil {
		if isConflictHTTP(err) {
			fmt.Fprintf(os.Stderr, "[forge] project %q already exists (conflict) — reusing existing record\n", req.ProjectName)
			existing, lookupErr := restFindProjectByName(ctx, base, token, req.ProjectName)
			if lookupErr != nil {
				return restProject{}, fmt.Errorf("forge REST: conflict on create + lookup failed: %w (original: %v)", lookupErr, err)
			}
			fmt.Fprintf(os.Stderr, "[forge] reusing project id=%d name=%q\n", existing.ID, existing.Name)
			return existing, nil
		}
		return restProject{}, err
	}
	if resp.Project.ID != 0 {
		return resp.Project, nil
	}
	if resp.ID != 0 {
		return restProject{ID: resp.ID, Name: resp.Name}, nil
	}
	if resp.ProjectID != 0 {
		return restProject{ID: resp.ProjectID, Name: resp.Name}, nil
	}
	return restProject{}, fmt.Errorf("forge REST create project: no project ID in response (tried wrapped, flat-id, project_id shapes)")
}

// restFindProjectByName GETs /api/projects and returns the project whose name
// matches exactly. Used by the 409 upsert path.
func restFindProjectByName(ctx context.Context, base, token, name string) (restProject, error) {
	var resp struct {
		Projects []restProject `json:"projects"`
	}
	if err := restGet(ctx, base+"/api/projects", token, &resp); err != nil {
		return restProject{}, fmt.Errorf("list projects: %w", err)
	}
	for _, p := range resp.Projects {
		if p.Name == name {
			return p, nil
		}
	}
	return restProject{}, fmt.Errorf("project %q not found in forge: %w", name, os.ErrNotExist)
}

type restCluster struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func restCreateCluster(ctx context.Context, base, token string, projectID int, req RegisterRequest) (restCluster, error) {
	encodedKubeconfig := base64.StdEncoding.EncodeToString(req.Kubeconfig)
	cloudProv := req.CloudProvider
	if cloudProv == "" {
		cloudProv = "gcp"
	}
	body := map[string]any{
		"name":           req.ClusterName,
		"cloud_provider": cloudProv,
		"region":         req.Region,
		"kubeconfig":     encodedKubeconfig,
	}
	var resp struct {
		ID        int         `json:"id"`
		ClusterID int         `json:"cluster_id"`
		Name      string      `json:"name"`
		Cluster   restCluster `json:"cluster"`
		Success   bool        `json:"success"`
	}
	url := fmt.Sprintf("%s/api/projects/%d/k8s/clusters", base, projectID)
	err := restPost(ctx, url, token, body, &resp)
	if err != nil {
		if isConflictHTTP(err) {
			fmt.Fprintf(os.Stderr, "[forge] cluster %q already exists in project %d (conflict) — refreshing kubeconfig\n",
				req.ClusterName, projectID)
			existing, lookupErr := restFindClusterByName(ctx, base, token, projectID, req.ClusterName)
			if lookupErr != nil {
				return restCluster{}, fmt.Errorf("forge REST: conflict on cluster create + lookup failed: %w (original: %v)", lookupErr, err)
			}
			if updateErr := restUpdateClusterKubeconfig(ctx, base, token, existing.ID, encodedKubeconfig); updateErr != nil {
				return restCluster{}, fmt.Errorf("forge REST: conflict on cluster create + kubeconfig refresh failed: %w", updateErr)
			}
			fmt.Fprintf(os.Stderr, "[forge] cluster id=%d kubeconfig refreshed\n", existing.ID)
			return existing, nil
		}
		return restCluster{}, err
	}
	if resp.Cluster.ID != 0 {
		return resp.Cluster, nil
	}
	if resp.ID != 0 {
		return restCluster{ID: resp.ID, Name: resp.Name}, nil
	}
	if resp.ClusterID != 0 {
		return restCluster{ID: resp.ClusterID, Name: resp.Name}, nil
	}
	return restCluster{}, fmt.Errorf("forge REST create cluster: no cluster ID in response (tried wrapped, flat-id, cluster_id shapes)")
}

// restFindClusterByName GETs /api/projects/{id}/k8s/clusters and returns the
// cluster whose name matches exactly.
func restFindClusterByName(ctx context.Context, base, token string, projectID int, name string) (restCluster, error) {
	var resp struct {
		Clusters []restCluster `json:"clusters"`
	}
	url := fmt.Sprintf("%s/api/projects/%d/k8s/clusters", base, projectID)
	if err := restGet(ctx, url, token, &resp); err != nil {
		return restCluster{}, fmt.Errorf("list clusters: %w", err)
	}
	for _, c := range resp.Clusters {
		if c.Name == name {
			return c, nil
		}
	}
	return restCluster{}, fmt.Errorf("cluster %q not found in project %d: %w", name, projectID, os.ErrNotExist)
}

// restUpdateClusterKubeconfig PUTs a fresh kubeconfig onto an existing cluster record.
func restUpdateClusterKubeconfig(ctx context.Context, base, token string, clusterID int, encodedKubeconfig string) error {
	body := map[string]any{"kubeconfig": encodedKubeconfig}
	url := fmt.Sprintf("%s/api/k8s/clusters/%d", base, clusterID)
	return restPut(ctx, url, token, body, nil)
}

func restDeleteCluster(ctx context.Context, base, token string, projectID, clusterID int) error {
	url := fmt.Sprintf("%s/api/projects/%d/k8s/clusters/%d", base, projectID, clusterID)
	return restDelete(ctx, url, token)
}

func restDeleteProject(ctx context.Context, base, token string, projectID int) error {
	url := fmt.Sprintf("%s/api/projects/%d", base, projectID)
	return restDelete(ctx, url, token)
}

// restPost sends a POST request with JSON body and decodes the JSON response into out.
func restPost(ctx context.Context, url, token string, body, out any) error {
	req, err := newRESTRequest(ctx, http.MethodPost, url, token, body)
	if err != nil {
		return err
	}
	return doRESTRequest(req, url, out)
}

// restPut mirrors restPost but uses HTTP PUT.
func restPut(ctx context.Context, url, token string, body, out any) error {
	req, err := newRESTRequest(ctx, http.MethodPut, url, token, body)
	if err != nil {
		return err
	}
	return doRESTRequest(req, url, out)
}

// restGet sends a GET request and decodes the JSON response into out.
func restGet(ctx context.Context, url, token string, out any) error {
	req, err := newRESTRequest(ctx, http.MethodGet, url, token, nil)
	if err != nil {
		return err
	}
	return doRESTRequest(req, url, out)
}

// newRESTRequest builds an *http.Request with JSON body (when body != nil) and
// Authorization header.
func newRESTRequest(ctx context.Context, method, url, token string, body any) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

// doRESTRequest executes req via benchmarkHTTPDoFn, checks for HTTP errors, and
// decodes the JSON response into out (when out != nil and body is non-empty).
func doRESTRequest(req *http.Request, url string, out any) error {
	resp, err := benchmarkHTTPDoFn(req)
	if err != nil {
		return fmt.Errorf("http %s %s: %w", req.Method, url, err)
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return &restHTTPErr{StatusCode: resp.StatusCode, URL: url, Body: truncateREST(string(respBytes), 400)}
	}
	if out != nil && len(respBytes) > 0 {
		if err := json.Unmarshal(respBytes, out); err != nil {
			return fmt.Errorf("decode response from %s: %w", url, err)
		}
	}
	return nil
}

// restDelete sends a DELETE request and returns *restHTTPErr on status >= 400.
func restDelete(ctx context.Context, url, token string) error {
	req, err := newRESTRequest(ctx, http.MethodDelete, url, token, nil)
	if err != nil {
		return err
	}
	return doRESTRequest(req, url, nil)
}

func truncateREST(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
