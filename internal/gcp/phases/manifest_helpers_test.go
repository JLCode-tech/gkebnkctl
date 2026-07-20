package phases

import (
	"testing"

	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/manifests"
)

// parseManifestNamesForTest reads an embedded manifest and returns the
// metadata.name of every object whose kind matches wantKind, in document
// order. Shared by the phase30/31/32/34/36/37 tests that assert an embedded
// static manifest parses to the expected set of objects — this is the
// dynamicfake-independent half of the coverage split called out in
// docs/SPEC-go-tracer.md §7 (SSA apply itself is a known dynamicfake
// boundary; parse/render correctness is fully testable).
func parseManifestNamesForTest(t *testing.T, path, wantKind string) []string {
	t.Helper()
	data, err := manifests.FS.ReadFile(path)
	if err != nil {
		t.Fatalf("reading embedded manifest %s: %v", path, err)
	}
	objs, err := k8s.ParseYAMLStream(data)
	if err != nil {
		t.Fatalf("parsing manifest %s: %v", path, err)
	}
	var names []string
	for _, o := range objs {
		if o.GetKind() == wantKind {
			names = append(names, o.GetName())
		}
	}
	return names
}

// readManifestForTest reads an embedded manifest file's raw bytes, failing
// the test on error.
func readManifestForTest(t *testing.T, path string) []byte {
	t.Helper()
	data, err := manifests.FS.ReadFile(path)
	if err != nil {
		t.Fatalf("reading embedded manifest %s: %v", path, err)
	}
	return data
}
