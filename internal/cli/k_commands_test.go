package cli

import (
	"bytes"
	"testing"
)

func TestKPortForwardCommandRegistered(t *testing.T) {
	root := RootCommand()
	kCmd, _, err := root.Find([]string{"k", "port-forward"})
	if err != nil || kCmd == nil {
		t.Fatalf("k port-forward command not found: %v", err)
	}
	if kCmd.Name() != "port-forward" {
		t.Errorf("expected port-forward command name, got %s", kCmd.Name())
	}

	buf := new(bytes.Buffer)
	kCmd.SetOut(buf)
	kCmd.SetErr(buf)
	kCmd.SetArgs([]string{"--help"})
	if err := kCmd.Execute(); err != nil {
		t.Fatalf("k port-forward --help failed: %v", err)
	}
}

func TestTopLevelAliasesRegistered(t *testing.T) {
	root := RootCommand()

	getCmd, _, err := root.Find([]string{"get"})
	if err != nil || getCmd == nil {
		t.Fatalf("top-level get command not found: %v", err)
	}
	if getCmd.Name() != "get" {
		t.Errorf("expected get command name, got %s", getCmd.Name())
	}

	logsCmd, _, err := root.Find([]string{"logs"})
	if err != nil || logsCmd == nil {
		t.Fatalf("top-level logs command not found: %v", err)
	}
	if logsCmd.Name() != "logs" {
		t.Errorf("expected logs command name, got %s", logsCmd.Name())
	}
}
