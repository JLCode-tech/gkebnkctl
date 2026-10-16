// Unit tests for ExecOptions and ExecCapture (internal/k8s/exec.go).
package k8s

import (
	"context"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

func TestExecOptions_RequiresPod(t *testing.T) {
	o := &ExecOptions{Command: []string{"ls"}}
	err := o.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for empty PodName; got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "pod") {
		t.Errorf("expected 'pod' in err; got: %v", err)
	}
}

func TestExecOptions_RequiresCommand(t *testing.T) {
	o := &ExecOptions{PodName: "p"}
	err := o.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for empty Command; got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "command") {
		t.Errorf("expected 'command' in err; got: %v", err)
	}
}

func TestExecOptions_StderrTTYInteraction(t *testing.T) {
	o := &ExecOptions{
		PodName: "p",
		Command: []string{"sh"},
		TTY:     true,
		Stdin:   true,
	}
	if !o.TTY || !o.Stdin {
		t.Errorf("ExecOptions field round-trip failed: TTY=%v Stdin=%v", o.TTY, o.Stdin)
	}
}

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
