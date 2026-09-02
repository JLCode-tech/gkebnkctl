package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGenerate(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	origWd, _ := os.Getwd()
	_ = os.Chdir(tmpDir)
	defer func() { _ = os.Chdir(origWd) }()

	generated, err := Generate(HarnessAll, "", true)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if len(generated) < 3 {
		t.Errorf("expected at least 3 generated files, got %d", len(generated))
	}

	for _, f := range generated {
		if _, err := os.Stat(filepath.Join(tmpDir, f)); err != nil {
			t.Errorf("file %s was not created: %v", f, err)
		}
	}
}
