package cli

import (
	"strings"
	"testing"
)

func TestBnkHealRegistered(t *testing.T) {
	found := false
	for _, sub := range bnkCmd.Commands() {
		if sub.Name() == "heal" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("bnk heal command is not registered under bnkCmd")
	}

	for _, flag := range []string{"kubeconfig", "config", "dry-run", "only"} {
		if bnkHealCmd.Flags().Lookup(flag) == nil {
			t.Errorf("bnk heal lacks --%s flag", flag)
		}
	}
	if f := bnkHealCmd.Flags().ShorthandLookup("f"); f == nil || f.Name != "config" {
		t.Error("bnk heal -f shorthand is not --config")
	}
}

func TestBnkHeal_UnknownRepair(t *testing.T) {
	flagBnkHealOnly = []string{"non-existent-repair"}
	defer func() { flagBnkHealOnly = nil }()

	err := runBnkHeal(bnkHealCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "unknown repair") {
		t.Errorf("expected unknown repair error, got: %v", err)
	}
}
