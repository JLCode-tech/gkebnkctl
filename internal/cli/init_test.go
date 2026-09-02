package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"my-cluster", "my-cluster"},
		{"My_Cluster.01", "my-cluster-01"},
		{"---foo---", "foo"},
		{"", "bnk-hostdev"},
	}
	for _, tc := range cases {
		got := sanitizeName(tc.in)
		if got != tc.want {
			t.Errorf("sanitizeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRunInit(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gkebnkctl-init-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	flagInitProject = "test-project-123"
	flagInitName = "test-cluster"
	flagInitRegion = "us-west1"
	flagInitZone = "us-west1-a"
	flagInitForce = false

	err = runInit(nil, []string{tmpDir})
	if err != nil {
		t.Fatalf("runInit failed: %v", err)
	}

	// Verify generated files
	clusterYaml := filepath.Join(tmpDir, "cluster.yaml")
	if !fileExists(clusterYaml) {
		t.Errorf("expected %s to exist", clusterYaml)
	}

	keysDir := filepath.Join(tmpDir, "keys")
	if !fileExists(keysDir) {
		t.Errorf("expected %s to exist", keysDir)
	}

	agentsMd := filepath.Join(tmpDir, "AGENTS.md")
	if !fileExists(agentsMd) {
		t.Errorf("expected %s to exist", agentsMd)
	}

	// Test overwriting without force fails
	err = runInit(nil, []string{tmpDir})
	if err == nil {
		t.Error("expected runInit without force to fail on existing files")
	}

	// Test with force succeeds
	flagInitForce = true
	err = runInit(nil, []string{tmpDir})
	if err != nil {
		t.Errorf("expected runInit with force to succeed, got: %v", err)
	}
}
