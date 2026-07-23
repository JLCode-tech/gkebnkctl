// Unit tests for the pure YAML-parsing helpers in apply.go
// (ParseYAMLStream) plus Applier.Apply's pre-Patch
// validation branches (missing kind, unmapped GVK). Server-side apply's
// Patch(ApplyPatchType) call needs a real API server — client-go's
// dynamicfake does not support it (docs/SPEC-go-tracer.md §7) — so the SSA
// round-trip itself is exercised by the phase-level dry-run tests plus
// internal/k8s/render's golden tests, not here.
package k8s

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const sampleConfigMapYAML = `apiVersion: v1
kind: ConfigMap
metadata:
  name: cm1
  namespace: default
data:
  key: value
`

const sampleMultiDocYAML = `apiVersion: v1
kind: ConfigMap
metadata:
  name: a
  namespace: default
data:
  k: v1
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: b
  namespace: default
data:
  k: v2
`

func TestParseYAMLStream_Single(t *testing.T) {
	objs, err := ParseYAMLStream([]byte(sampleConfigMapYAML))
	if err != nil {
		t.Fatalf("ParseYAMLStream: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	if objs[0].GetKind() != "ConfigMap" || objs[0].GetName() != "cm1" {
		t.Errorf("got kind=%q name=%q, want ConfigMap/cm1", objs[0].GetKind(), objs[0].GetName())
	}
}

func TestParseYAMLStream_Multi(t *testing.T) {
	objs, err := ParseYAMLStream([]byte(sampleMultiDocYAML))
	if err != nil {
		t.Fatalf("ParseYAMLStream: %v", err)
	}
	if len(objs) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(objs))
	}
	if objs[0].GetName() != "a" || objs[1].GetName() != "b" {
		t.Errorf("got names %q/%q, want a/b", objs[0].GetName(), objs[1].GetName())
	}
}

func TestParseYAMLStream_EmptyDocsSkipped(t *testing.T) {
	in := "---\n" + sampleConfigMapYAML + "\n---\n\n---\n"
	objs, err := ParseYAMLStream([]byte(in))
	if err != nil {
		t.Fatalf("ParseYAMLStream: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected 1 non-empty object, got %d", len(objs))
	}
}

func TestParseYAMLStream_BadYAML(t *testing.T) {
	_, err := ParseYAMLStream([]byte("not: yaml: : :"))
	if err == nil {
		t.Fatal("expected a parse error, got nil")
	}
}


// TestApplier_Apply_MissingKind proves Apply fails fast (before ever
// touching the RESTMapper or the dynamic client) on an object with no
// kind.
func TestApplier_Apply_MissingKind(t *testing.T) {
	a := NewApplierFromInterfaces(nil, nil)
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"metadata":   map[string]interface{}{"name": "x"},
	}}
	_, err := a.Apply(context.Background(), obj, false)
	if err == nil || !strings.Contains(err.Error(), "missing kind") {
		t.Fatalf("expected a missing-kind error, got: %v", err)
	}
}

// TestApplier_Apply_UnmappedKind proves Apply wraps a RESTMapping failure
// in a descriptive error, using an empty meta.MultiRESTMapper (a real
// meta.RESTMapper with zero delegate mappers, so RESTMapping always
// returns a NoMatchError) rather than a live discovery client.
func TestApplier_Apply_UnmappedKind(t *testing.T) {
	a := NewApplierFromInterfaces(nil, meta.MultiRESTMapper{})
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "totally.unknown/v1",
		"kind":       "TotallyUnknown",
		"metadata":   map[string]interface{}{"name": "x"},
	}}
	_, err := a.Apply(context.Background(), obj, false)
	if err == nil || !strings.Contains(err.Error(), "REST mapping") {
		t.Fatalf("expected a REST-mapping error, got: %v", err)
	}
}
