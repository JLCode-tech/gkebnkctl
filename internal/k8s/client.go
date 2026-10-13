// Package k8s wires Kubernetes access for gkebnkctl: server-side apply
// (apply.go), typed/dynamic client construction from a rest.Config
// (client.go), and readiness polling (wait.go).
//
// The primary GKE cluster's rest.Config is built by gcp.BuildRESTConfig
// (internal/gcp/auth.go) directly from the container API + Application
// Default Credentials — no kubeconfig file, no exec-credential plugin. This
// package's kubeconfig-file helpers exist ONLY for the
// bnk.farSecret.source == "kubecontext" path (Phase33, Task C), which
// legitimately needs to read the OPERATOR's local kubeconfig to copy a
// Secret from another cluster they already have kubectl access to.
package k8s

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// DefaultKubeconfigPath returns the first existing path in $KUBECONFIG
// (colon-separated, matching kubectl's own resolution), falling back to
// ~/.kube/config. Empty if neither exists.
func DefaultKubeconfigPath() string {
	if v := os.Getenv("KUBECONFIG"); v != "" {
		for _, p := range filepath.SplitList(v) {
			if _, err := os.Stat(p); err == nil { // #nosec G703 -- $KUBECONFIG is operator-set, follows kubectl's own resolution rules
				return p
			}
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	def := filepath.Join(home, ".kube", "config")
	if _, err := os.Stat(def); err == nil {
		return def
	}
	return ""
}

// BuildRESTConfigFromKubeconfig builds a rest.Config from a local kubeconfig
// file + context name, for the far-secret "kubecontext" source (Phase33).
// kubeconfigPath "" resolves via DefaultKubeconfigPath; contextName ""
// selects the kubeconfig's current-context.
func BuildRESTConfigFromKubeconfig(kubeconfigPath, contextName string) (*rest.Config, error) {
	if kubeconfigPath == "" {
		kubeconfigPath = DefaultKubeconfigPath()
	}
	if kubeconfigPath == "" {
		return nil, errors.New("no kubeconfig found: set $KUBECONFIG, or point bnk.farSecret.sourceContext at an existing ~/.kube/config entry")
	}
	loader := clientcmd.NewDefaultClientConfigLoadingRules()
	loader.ExplicitPath = kubeconfigPath
	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		overrides.CurrentContext = contextName
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loader, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig %s (context %q): %w", kubeconfigPath, contextName, err)
	}
	return cfg, nil
}

// NewClientsetFromConfig builds a typed kubernetes.Interface from cfg.
// Shared by both the primary-cluster path (gcp.Clients.AttachK8s) and the
// far-secret kubecontext path (Phase33).
func NewClientsetFromConfig(cfg *rest.Config) (kubernetes.Interface, error) {
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating clientset: %w", err)
	}
	return cs, nil
}

// NewDynamicFromConfig builds a dynamic.Interface from cfg — needed for CRDs
// and other resources outside the typed client-go scheme (F5SPKVlan,
// CNEInstance, License, cert-manager Certificate, ...).
func NewDynamicFromConfig(cfg *rest.Config) (dynamic.Interface, error) {
	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating dynamic client: %w", err)
	}
	return dc, nil
}

// BuildClientset returns a typed client for core + apps + batch + etc.
// kubeconfigPath: empty string → DefaultKubeconfigPath().
func BuildClientset(kubeconfigPath string) (kubernetes.Interface, error) {
	cfg, err := BuildRESTConfigFromKubeconfig(kubeconfigPath, "")
	if err != nil {
		return nil, err
	}
	return NewClientsetFromConfig(cfg)
}

// BuildDynamicClient builds a dynamic.Interface from kubeconfigPath.
// kubeconfigPath: empty string → DefaultKubeconfigPath().
func BuildDynamicClient(kubeconfigPath string) (dynamic.Interface, error) {
	cfg, err := BuildRESTConfigFromKubeconfig(kubeconfigPath, "")
	if err != nil {
		return nil, err
	}
	return NewDynamicFromConfig(cfg)
}
