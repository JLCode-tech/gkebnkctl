package doctor

import (
	"bytes"
	"strings"
	"testing"
)

func TestSymbolFor(t *testing.T) {
	if symbolFor(StatusOK) != "✓" {
		t.Errorf("expected ✓ for StatusOK, got %s", symbolFor(StatusOK))
	}
	if symbolFor(StatusWarning) != "!" {
		t.Errorf("expected ! for StatusWarning, got %s", symbolFor(StatusWarning))
	}
	if symbolFor(StatusError) != "✗" {
		t.Errorf("expected ✗ for StatusError, got %s", symbolFor(StatusError))
	}
}

func TestHasFailures(t *testing.T) {
	checks := []Check{
		{Name: "c1", Status: StatusOK},
		{Name: "c2", Status: StatusWarning},
	}
	if HasFailures(checks) {
		t.Error("expected no failures")
	}

	checks = append(checks, Check{Name: "c3", Status: StatusError, Optional: false})
	if !HasFailures(checks) {
		t.Error("expected failure")
	}
}

func TestPrintResults(t *testing.T) {
	checks := []Check{
		{Name: "gcloud", Status: StatusOK, Detail: "/usr/bin/gcloud", Why: "cli tool"},
		{Name: "compute.googleapis.com", Status: StatusError, Detail: "disabled", Why: "api", Hint: "       → run: gcloud services enable\n"},
	}

	var buf bytes.Buffer
	err := PrintResults(&buf, checks)
	if err != nil {
		t.Fatalf("PrintResults failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "gcloud") || !strings.Contains(out, "compute.googleapis.com") {
		t.Errorf("unexpected output: %s", out)
	}
}
