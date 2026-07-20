package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()

	s1, err := Load(dir)
	if err != nil {
		t.Fatalf("Load on empty dir: %v", err)
	}
	s1.Set("TMM_EXT_IFACE", "ens5")
	s1.Set("TMM_EXT_PCI", "0000:00:05.0")
	if err := s1.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	s2, err := Load(dir)
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if s2.Get("TMM_EXT_IFACE") != "ens5" {
		t.Errorf("TMM_EXT_IFACE: got %q, want %q", s2.Get("TMM_EXT_IFACE"), "ens5")
	}
	if s2.Get("TMM_EXT_PCI") != "0000:00:05.0" {
		t.Errorf("TMM_EXT_PCI: got %q, want %q", s2.Get("TMM_EXT_PCI"), "0000:00:05.0")
	}
}

func TestLoad_MissingFileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	// No state.env written yet.
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil State")
	}
	if got := s.Get("ANYTHING"); got != "" {
		t.Errorf("expected empty value, got %q", got)
	}
}

func TestLoad_CorruptFileReturnsError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.env")
	if err := os.WriteFile(p, []byte("not-kv-line\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for corrupt file, got nil")
	}
}

func TestLoad_IgnoresComments(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.env")
	content := "# this is a comment\nTMM_EXT_IFACE=ens5\n# another comment\nTMM_INT_IFACE=ens6\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Get("TMM_EXT_IFACE") != "ens5" {
		t.Errorf("TMM_EXT_IFACE: got %q", s.Get("TMM_EXT_IFACE"))
	}
	if s.Get("TMM_INT_IFACE") != "ens6" {
		t.Errorf("TMM_INT_IFACE: got %q", s.Get("TMM_INT_IFACE"))
	}
}

func TestSave_CreatesDir(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "nested", "dir")

	s, _ := Load(dir) // dir doesn't exist yet, Load returns empty State
	s.Set("KEY", "val")
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	s2, err := Load(dir)
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if s2.Get("KEY") != "val" {
		t.Errorf("KEY: got %q, want val", s2.Get("KEY"))
	}
}

func TestGet_MissingKeyReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	s, _ := Load(dir)
	if got := s.Get("NO_SUCH_KEY"); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

// TestMarkReadOnly_SaveIsNoOp is the regression guard for the up --dry-run
// state-pollution bug: a dry-run sets placeholder discovered values in
// memory, but Save must never write them to the real state.env on disk.
func TestMarkReadOnly_SaveIsNoOp(t *testing.T) {
	dir := t.TempDir()

	// Pre-existing real state on disk (as if a real `up` had run).
	real, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	real.Set("TMM_EXT_IFACE", "ens5")
	if err := real.Save(); err != nil {
		t.Fatalf("Save real state: %v", err)
	}

	// Now simulate a dry-run: load, mark read-only, overlay a placeholder, save.
	dry, err := Load(dir)
	if err != nil {
		t.Fatalf("Load for dry-run: %v", err)
	}
	dry.MarkReadOnly()
	if !dry.ReadOnly() {
		t.Fatal("ReadOnly() = false after MarkReadOnly()")
	}
	dry.Set("TMM_EXT_IFACE", "dry-run-eth1") // the polluting placeholder
	if dry.Get("TMM_EXT_IFACE") != "dry-run-eth1" {
		t.Error("Set should still update the in-memory map in read-only mode")
	}
	if err := dry.Save(); err != nil {
		t.Fatalf("Save in read-only mode should be a no-op, got error: %v", err)
	}

	// Reload from disk — the real value must survive, the placeholder must NOT.
	after, err := Load(dir)
	if err != nil {
		t.Fatalf("Load after dry-run save: %v", err)
	}
	if got := after.Get("TMM_EXT_IFACE"); got != "ens5" {
		t.Errorf("dry-run polluted disk: TMM_EXT_IFACE = %q, want ens5", got)
	}
}

// TestMarkReadOnly_NoFileCreatedOnFreshDir verifies a dry-run against a dir
// with no prior state.env leaves the disk untouched (no file created).
func TestMarkReadOnly_NoFileCreatedOnFreshDir(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	s.MarkReadOnly()
	s.Set("TMM_EXT_IFACE", "dry-run-eth1")
	if err := s.Save(); err != nil {
		t.Fatalf("Save no-op: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.env")); !os.IsNotExist(err) {
		t.Errorf("read-only Save created state.env on disk (stat err = %v); expected none", err)
	}
}
