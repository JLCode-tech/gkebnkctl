package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCLIJournal(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cli-journal-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	flagJournalFile = filepath.Join(tmpDir, "journal.jsonl")
	flagJournalAction = "TEST"
	flagJournalActor = "test-agent"
	flagJournalTags = []string{"unit-test"}

	if err := runJournalAdd(journalAddCmd, []string{"Test", "journal", "entry"}); err != nil {
		t.Fatalf("runJournalAdd failed: %v", err)
	}

	flagJournalJSON = false
	if err := runJournalList(journalListCmd, nil); err != nil {
		t.Fatalf("runJournalList failed: %v", err)
	}

	flagJournalJSON = true
	if err := runJournalList(journalListCmd, nil); err != nil {
		t.Fatalf("runJournalList --json failed: %v", err)
	}
}

func TestCLIAgent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cli-agent-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	origWd, _ := os.Getwd()
	_ = os.Chdir(tmpDir)
	defer func() { _ = os.Chdir(origWd) }()

	flagAgentFile = ""
	flagAgentForce = true

	if err := runAgent(agentCmd, []string{"all"}); err != nil {
		t.Fatalf("runAgent failed: %v", err)
	}
}
