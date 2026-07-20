package manifests

import "testing"

// TestFS_PlaceholdersEmbedded is a sanity check that the go:embed directive
// compiles and picks up files from every declared directory — a regression
// guard for the Task A "empty-but-present" seam Tasks C/D fill in.
func TestFS_PlaceholdersEmbedded(t *testing.T) {
	for _, path := range []string{
		"static/README.md",
		"multus/README.md",
		"cert-manager/README.md",
		"flo/README.md",
		"templates/README.md",
	} {
		if _, err := FS.ReadFile(path); err != nil {
			t.Errorf("FS.ReadFile(%q): %v", path, err)
		}
	}
}
