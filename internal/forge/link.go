package forge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// LinkFileName is the on-disk file inside a workspace dir that records
// which forge project + cluster gkebnkctl registered the workspace as.
// Re-running `forge register` updates this file in place.
const LinkFileName = "forge_link.json"

// Link is the persisted association between a gkebnkctl workspace and
// a forge project + cluster. It survives across sessions so
// `forge status` and `forge unregister` know what to act on.
type Link struct {
	ForgeURL     string    `json:"forge_url"`
	ForgeMCPURL  string    `json:"forge_mcp_url"`
	ProjectID    int       `json:"project_id"`
	ProjectName  string    `json:"project_name"`
	ClusterID    int       `json:"cluster_id"`
	ClusterName  string    `json:"cluster_name"`
	RegisteredAt time.Time `json:"registered_at"`
	Workspace    string    `json:"workspace"`
	// Status records the registration state. Empty string is equivalent to
	// "registered" for backward compatibility. "pending" means the forge handoff failed.
	Status string `json:"status,omitempty"`
}

// IsRegistered reports whether the link represents a completed registration.
// An empty Status is treated as "registered" for backward compatibility.
func (l *Link) IsRegistered() bool {
	return l.Status == "" || l.Status == "registered"
}

// LinkPath returns the absolute path to the forge_link.json inside a
// workspace directory.
func LinkPath(workspaceDir string) string {
	return filepath.Join(workspaceDir, LinkFileName)
}

// ReadLink loads forge_link.json. Returns (nil, os.ErrNotExist) when
// the workspace has never been registered.
func ReadLink(workspaceDir string) (*Link, error) {
	p := LinkPath(workspaceDir)
	b, err := os.ReadFile(p) // #nosec G304 -- path is derived from the workspace dir (config-managed), not user-tainted input
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	var l Link
	if err := json.Unmarshal(b, &l); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return &l, nil
}

// WriteLink atomically replaces forge_link.json in the workspace dir.
// The write goes to a sibling temp file first to avoid leaving a
// half-written link visible to a concurrent `forge status`.
func WriteLink(workspaceDir string, l *Link) error {
	if err := os.MkdirAll(workspaceDir, 0o750); err != nil {
		return fmt.Errorf("ensure workspace dir: %w", err)
	}
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	final := LinkPath(workspaceDir)
	tmp, err := os.CreateTemp(workspaceDir, "forge_link.*.json.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()        // best-effort cleanup; the write error is the real one
		_ = os.Remove(tmpPath) // best-effort cleanup
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath) // best-effort cleanup
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, final); err != nil {
		_ = os.Remove(tmpPath) // best-effort cleanup
		return fmt.Errorf("rename %s -> %s: %w", tmpPath, final, err)
	}
	return nil
}

// RemoveLink deletes forge_link.json. Returns nil if it was already absent.
func RemoveLink(workspaceDir string) error {
	err := os.Remove(LinkPath(workspaceDir))
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
