// Unit tests for ExecCapture (internal/k8s/exec.go).
//
// SPDY exec needs a real upgradeable connection — not fakeable (matches
// awsbnkctl's internal/k8s/exec_test.go boundary note). The testable surface
// here is input validation; the actual stream is exercised live, not in
// this suite.
package k8s

import (
	"context"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

func TestExecCapture_RequiresConfig(t *testing.T) {
	_, _, err := ExecCapture(context.Background(), nil, fake.NewClientset(), "ns", "pod", "", []string{"ls"})
	if err == nil || !strings.Contains(err.Error(), "rest.Config") {
		t.Fatalf("expected a rest.Config error, got: %v", err)
	}
}

func TestExecCapture_RequiresClientset(t *testing.T) {
	_, _, err := ExecCapture(context.Background(), &rest.Config{}, nil, "ns", "pod", "", []string{"ls"})
	if err == nil || !strings.Contains(err.Error(), "clientset") {
		t.Fatalf("expected a clientset error, got: %v", err)
	}
}

func TestExecCapture_RequiresPod(t *testing.T) {
	_, _, err := ExecCapture(context.Background(), &rest.Config{}, fake.NewClientset(), "ns", "", "", []string{"ls"})
	if err == nil || !strings.Contains(err.Error(), "pod") {
		t.Fatalf("expected a pod-name error, got: %v", err)
	}
}

func TestExecCapture_RequiresCommand(t *testing.T) {
	_, _, err := ExecCapture(context.Background(), &rest.Config{}, fake.NewClientset(), "ns", "pod", "", nil)
	if err == nil || !strings.Contains(err.Error(), "command") {
		t.Fatalf("expected a command error, got: %v", err)
	}
}
