package journal

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJournalAppendAndList(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "journal-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	journalPath := filepath.Join(tmpDir, "journal.jsonl")

	e1 := Entry{
		Action:  "DEPLOY",
		Cluster: "test-cluster",
		Message: "Started Phase 00-51 deploy",
		Actor:   "test-user",
	}
	if err := Append(journalPath, e1); err != nil {
		t.Fatalf("Append e1 failed: %v", err)
	}

	e2 := Entry{
		Action:  "TEST",
		Cluster: "test-cluster",
		Message: "DNS probe test passed",
		Actor:   "test-user",
	}
	if err := Append(journalPath, e2); err != nil {
		t.Fatalf("Append e2 failed: %v", err)
	}

	entries, err := List(journalPath)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Message != "Started Phase 00-51 deploy" {
		t.Errorf("entry 0 message mismatch: %s", entries[0].Message)
	}
	if entries[1].Action != "TEST" {
		t.Errorf("entry 1 action mismatch: %s", entries[1].Action)
	}

	var buf bytes.Buffer
	RenderText(&buf, entries)
	if !strings.Contains(buf.String(), "Started Phase 00-51 deploy") {
		t.Errorf("RenderText missing message:\n%s", buf.String())
	}
}
