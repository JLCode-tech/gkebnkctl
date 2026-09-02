package cli

import (
	"testing"
)

func TestRunVersion(t *testing.T) {
	flagVersionJSON = false
	if err := runVersion(nil, nil); err != nil {
		t.Fatalf("runVersion failed: %v", err)
	}

	flagVersionJSON = true
	if err := runVersion(nil, nil); err != nil {
		t.Fatalf("runVersion --json failed: %v", err)
	}
}
