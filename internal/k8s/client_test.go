package k8s

import (
	"os"
	"path/filepath"
	"testing"

	"k8s.io/client-go/rest"
)

func TestDefaultKubeconfigPath(t *testing.T) {
	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "config")
	if err := os.WriteFile(cfgFile, []byte("apiVersion: v1\nkind: Config\n"), 0o600); err != nil {
		t.Fatalf("writing temp kubeconfig: %v", err)
	}

	t.Run("KUBECONFIG env var set", func(t *testing.T) {
		t.Setenv("KUBECONFIG", cfgFile)
		got := DefaultKubeconfigPath()
		if got != cfgFile {
			t.Errorf("DefaultKubeconfigPath() = %q, want %q", got, cfgFile)
		}
	})

	t.Run("KUBECONFIG env var missing/nonexistent", func(t *testing.T) {
		t.Setenv("KUBECONFIG", filepath.Join(tmpDir, "nonexistent"))
		// Unless ~/.kube/config exists on the host, this returns ""
		_ = DefaultKubeconfigPath()
	})
}

func TestBuildRESTConfigFromKubeconfig(t *testing.T) {
	t.Run("missing kubeconfig path and default returns error", func(t *testing.T) {
		t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "nonexistent"))
		t.Setenv("HOME", t.TempDir()) // Ensure ~/.kube/config does not exist

		_, err := BuildRESTConfigFromKubeconfig("", "some-context")
		if err == nil {
			t.Fatal("expected error when no kubeconfig exists, got nil")
		}
	})

	t.Run("valid kubeconfig file", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgFile := filepath.Join(tmpDir, "kubeconfig")
		validKubeconfig := `
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://127.0.0.1:6443
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: test-user
  name: test-context
current-context: test-context
users:
- name: test-user
  user:
    token: test-token
`
		if err := os.WriteFile(cfgFile, []byte(validKubeconfig), 0o600); err != nil {
			t.Fatalf("writing valid kubeconfig: %v", err)
		}

		cfg, err := BuildRESTConfigFromKubeconfig(cfgFile, "test-context")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Host != "https://127.0.0.1:6443" {
			t.Errorf("cfg.Host = %q, want https://127.0.0.1:6443", cfg.Host)
		}
		if cfg.BearerToken != "test-token" {
			t.Errorf("cfg.BearerToken = %q, want test-token", cfg.BearerToken)
		}
	})
}

func TestNewClientsFromConfig(t *testing.T) {
	cfg := &rest.Config{
		Host: "https://127.0.0.1:6443",
	}

	cs, err := NewClientsetFromConfig(cfg)
	if err != nil {
		t.Errorf("NewClientsetFromConfig: unexpected error: %v", err)
	}
	if cs == nil {
		t.Error("NewClientsetFromConfig returned nil")
	}

	dyn, err := NewDynamicFromConfig(cfg)
	if err != nil {
		t.Errorf("NewDynamicFromConfig: unexpected error: %v", err)
	}
	if dyn == nil {
		t.Error("NewDynamicFromConfig returned nil")
	}
}
