package k8s

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// A non-deferred mapper (what phase tests inject) must pass its error through
// untouched rather than panicking on the type assertion.
func TestRESTMappingWithRefresh_NonDeferredMapperPassesErrorThrough(t *testing.T) {
	_, err := RESTMappingWithRefresh(meta.MultiRESTMapper{},
		schema.GroupVersionKind{Group: "cert-manager.io", Version: "v1", Kind: "ClusterIssuer"})
	if err == nil {
		t.Fatal("expected a NoMatch error from an empty MultiRESTMapper")
	}
	if !meta.IsNoMatchError(err) {
		t.Errorf("err = %v, want a NoMatch error", err)
	}
}

// A mapper that resolves the kind must return the mapping without any refresh.
func TestRESTMappingWithRefresh_ResolvesKnownKind(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "k8s.f5net.com", Version: "v1", Kind: "F5SPKStaticRoute"}
	m := meta.NewDefaultRESTMapper([]schema.GroupVersion{gvk.GroupVersion()})
	m.AddSpecific(gvk,
		schema.GroupVersionResource{Group: gvk.Group, Version: gvk.Version, Resource: "f5-spk-staticroutes"},
		schema.GroupVersionResource{Group: gvk.Group, Version: gvk.Version, Resource: "f5-spk-staticroute"},
		meta.RESTScopeNamespace)

	got, err := RESTMappingWithRefresh(m, gvk)
	if err != nil {
		t.Fatalf("RESTMappingWithRefresh: %v", err)
	}
	if got.Resource.Resource != "f5-spk-staticroutes" {
		t.Errorf("Resource = %q, want f5-spk-staticroutes", got.Resource.Resource)
	}
}
