package kubeconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerate(t *testing.T) {
	data, err := Generate("test-cluster", "test-project", "us-central1-a", "35.200.10.1", "LS0tLS1CRUdJTi...")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "gke_test-project_us-central1-a_test-cluster") {
		t.Errorf("expected context name in kubeconfig, got:\n%s", content)
	}
	if !strings.Contains(content, "https://35.200.10.1") {
		t.Errorf("expected server endpoint in kubeconfig, got:\n%s", content)
	}
	if !strings.Contains(content, "client.authentication.k8s.io/v1beta1") {
		t.Errorf("expected exec auth plugin in kubeconfig, got:\n%s", content)
	}
}

func TestWriteAndSync(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gkebnkctl-kc-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	testPath := filepath.Join(tmpDir, "kubeconfig")
	data := []byte("test-kubeconfig-data")

	if err := WriteFile(testPath, data); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	read, err := os.ReadFile(testPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(read) != string(data) {
		t.Errorf("content mismatch: got %q, want %q", string(read), string(data))
	}
}
