package kubeconfig

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	containerpb "cloud.google.com/go/container/apiv1/containerpb"
	"golang.org/x/oauth2/google"
	"gopkg.in/yaml.v3"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
)

// BackupSuffix is the extension appended to the original ~/.kube/config during sync.
const BackupSuffix = ".gkebnkctl-bak"

// Config represents a standard Kubernetes kubeconfig structure.
type Config struct {
	APIVersion     string         `yaml:"apiVersion"`
	Kind           string         `yaml:"kind"`
	CurrentContext string         `yaml:"current-context"`
	Clusters       []NamedCluster `yaml:"clusters"`
	Contexts       []NamedContext `yaml:"contexts"`
	Users          []NamedUser    `yaml:"users"`
	Preferences    map[string]any `yaml:"preferences,omitempty"`
}

type NamedCluster struct {
	Name    string         `yaml:"name"`
	Cluster ClusterDetails `yaml:"cluster"`
}

type ClusterDetails struct {
	Server                   string `yaml:"server"`
	CertificateAuthorityData string `yaml:"certificate-authority-data"`
}

type NamedContext struct {
	Name    string         `yaml:"name"`
	Context ContextDetails `yaml:"context"`
}

type ContextDetails struct {
	Cluster string `yaml:"cluster"`
	User    string `yaml:"user"`
}

type NamedUser struct {
	Name string      `yaml:"name"`
	User UserDetails `yaml:"user"`
}

type UserDetails struct {
	Token string      `yaml:"token,omitempty"`
	Exec  *ExecConfig `yaml:"exec,omitempty"`
}

type ExecConfig struct {
	APIVersion         string   `yaml:"apiVersion"`
	Command            string   `yaml:"command"`
	Args               []string `yaml:"args,omitempty"`
	InteractiveMode    string   `yaml:"interactiveMode,omitempty"`
	ProvideClusterInfo bool     `yaml:"provideClusterInfo,omitempty"`
}

// MakePortable replaces a non-portable exec plugin (such as gcloud) with a
// bearer token from Application Default Credentials so remote consumers (such
// as BNK-Forge running in a container) can authenticate to the GKE API server.
func MakePortable(ctx context.Context, kcBytes []byte) []byte {
	if len(kcBytes) == 0 {
		return kcBytes
	}
	var cfg Config
	if err := yaml.Unmarshal(kcBytes, &cfg); err != nil || len(cfg.Users) == 0 {
		return kcBytes
	}
	var bearerToken string
	if ts, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform"); err == nil && ts != nil {
		if tok, tErr := ts.Token(); tErr == nil && tok != nil {
			bearerToken = tok.AccessToken
		}
	}
	for i := range cfg.Users {
		if bearerToken != "" {
			cfg.Users[i].User.Token = bearerToken
			cfg.Users[i].User.Exec = nil
		} else if cfg.Users[i].User.Exec != nil && cfg.Users[i].User.Exec.Command == "gcloud" {
			cfg.Users[i].User.Exec.Command = "gke-gcloud-auth-plugin"
			cfg.Users[i].User.Exec.Args = nil
		}
	}
	out, err := yaml.Marshal(&cfg)
	if err != nil {
		return kcBytes
	}
	return out
}

// Generate builds a standard GKE kubeconfig YAML string.
func Generate(clusterName, project, zone, endpoint, caData string) ([]byte, error) {
	contextName := fmt.Sprintf("gke_%s_%s_%s", project, zone, clusterName)
	server := endpoint
	if server != "" && server[:4] != "http" {
		server = "https://" + server
	}

	cfg := Config{
		APIVersion:     "v1",
		Kind:           "Config",
		CurrentContext: contextName,
		Clusters: []NamedCluster{
			{
				Name: contextName,
				Cluster: ClusterDetails{
					Server:                   server,
					CertificateAuthorityData: caData,
				},
			},
		},
		Contexts: []NamedContext{
			{
				Name: contextName,
				Context: ContextDetails{
					Cluster: contextName,
					User:    contextName,
				},
			},
		},
		Users: []NamedUser{
			{
				Name: contextName,
				User: UserDetails{
					Exec: &ExecConfig{
						APIVersion:         "client.authentication.k8s.io/v1beta1",
						Command:            "gcloud",
						Args:               []string{"config", "config-helper", "--format=json"},
						InteractiveMode:    "IfAvailable",
						ProvideClusterInfo: true,
					},
				},
			},
		},
	}

	return yaml.Marshal(&cfg)
}

// FetchAndGenerate queries GKE for the cluster descriptor and generates its Kubeconfig.
func FetchAndGenerate(ctx context.Context, cl *intent.Cluster) ([]byte, error) {
	clients, err := gcp.NewClients(ctx, cl.Metadata.Project)
	if err != nil {
		return nil, fmt.Errorf("init GCP clients: %w", err)
	}

	clusterName := fmt.Sprintf("projects/%s/locations/%s/clusters/%s",
		cl.Metadata.Project, cl.Metadata.Zone, cl.Metadata.Name)

	resp, err := clients.GKE.GetCluster(ctx, &containerpb.GetClusterRequest{Name: clusterName})
	if err != nil {
		return nil, fmt.Errorf("fetch cluster %s: %w", cl.Metadata.Name, err)
	}

	caCert := resp.GetMasterAuth().GetClusterCaCertificate()
	endpoint := resp.GetEndpoint()

	kcBytes, err := Generate(cl.Metadata.Name, cl.Metadata.Project, cl.Metadata.Zone, endpoint, caCert)
	if err != nil {
		return nil, err
	}
	return MakePortable(ctx, kcBytes), nil
}

// WriteFile writes the kubeconfig bytes to the target path.
func WriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return os.WriteFile(path, data, 0o600)
}

// GlobalKubeconfigPath returns the path to ~/.kube/config.
func GlobalKubeconfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".kube", "config"), nil
}

// SyncGlobal safely writes data to ~/.kube/config, backing up any existing file.
func SyncGlobal(data []byte) (string, error) {
	globalPath, err := GlobalKubeconfigPath()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}

	backupPath := globalPath + BackupSuffix

	// If global kubeconfig exists, create a backup if not already present
	if _, err := os.Stat(globalPath); err == nil {
		if _, err := os.Stat(backupPath); os.IsNotExist(err) {
			orig, err := os.ReadFile(globalPath) // #nosec G304 -- path is standard user home kubeconfig
			if err != nil {
				return "", fmt.Errorf("read existing kubeconfig: %w", err)
			}
			if err := os.WriteFile(backupPath, orig, 0o600); err != nil { // #nosec G304 G703 -- backup path in user home
				return "", fmt.Errorf("write backup %s: %w", backupPath, err)
			}
		}
	}

	if err := WriteFile(globalPath, data); err != nil {
		return "", fmt.Errorf("write global kubeconfig: %w", err)
	}

	return backupPath, nil
}

// RestoreGlobal restores ~/.kube/config from ~/.kube/config.gkebnkctl-bak if it exists.
func RestoreGlobal() (bool, error) {
	globalPath, err := GlobalKubeconfigPath()
	if err != nil {
		return false, fmt.Errorf("get home dir: %w", err)
	}

	backupPath := globalPath + BackupSuffix
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		return false, nil
	}

	backupData, err := os.ReadFile(backupPath) // #nosec G304 -- path is standard user home kubeconfig backup
	if err != nil {
		return false, fmt.Errorf("read backup: %w", err)
	}

	if err := os.WriteFile(globalPath, backupData, 0o600); err != nil { // #nosec G304 G703 -- global kubeconfig path in user home
		return false, fmt.Errorf("restore kubeconfig: %w", err)
	}

	_ = os.Remove(backupPath)
	return true, nil
}
