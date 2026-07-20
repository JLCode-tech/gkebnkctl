package manifests

import "testing"

// TestFS_ManifestsEmbedded is a sanity check that the go:embed directive
// compiles and picks up files from every declared directory. static/,
// multus/, cert-manager/, and flo/ now carry Task C's real content (the
// Task A placeholder README.md files were removed once real files landed);
// templates/README.md is still a placeholder — egress.yaml.tmpl and
// agent-route-setter.yaml.tmpl remain Task D scope.
func TestFS_ManifestsEmbedded(t *testing.T) {
	for _, path := range []string{
		"static/01-namespaces.yaml",
		"static/04-issuers.yaml",
		"static/07-node-sysctls.json",
		"multus/02-multus-rbac.yaml",
		"multus/02-multus-crd.yaml",
		"multus/02-multus-configmap.yaml",
		"multus/02-multus-ds.yaml",
		"cert-manager/cert-manager-v1.16.1.yaml",
		"flo/flo-manifest.yaml",
		"templates/nads-hostdevice.yaml.tmpl",
		"templates/cneinstance.yaml.tmpl",
		"templates/vlans-routes.yaml.tmpl",
		"templates/license-cr.yaml.tmpl",
		"templates/README.md",
	} {
		if _, err := FS.ReadFile(path); err != nil {
			t.Errorf("FS.ReadFile(%q): %v", path, err)
		}
	}
}
