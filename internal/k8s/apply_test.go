// Unit tests for the pure YAML-parsing helpers in apply.go
// (ParseYAMLStream, loadObjects, applyPatchOptions) plus Applier.Apply's
// pre-Patch validation branches (missing kind, unmapped GVK).
package k8s

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
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

func TestApplyOptions_RequiresFilename(t *testing.T) {
	o := &ApplyOptions{}
	err := o.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for empty Filename; got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "filename") &&
		!strings.Contains(strings.ToLower(err.Error()), "-f") {
		t.Errorf("expected 'filename' / '-f' in err; got: %v", err)
	}
}

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

func TestSplitYAML_LeadingSeparator(t *testing.T) {
	in := []byte("---\nfoo: bar\n---\nbaz: qux\n")
	parts := splitYAML(in)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts; got %d: %q", len(parts), parts)
	}
	if !bytes.Contains(parts[0], []byte("foo: bar")) {
		t.Errorf("first part lost content: %q", parts[0])
	}
	if !bytes.Contains(parts[1], []byte("baz: qux")) {
		t.Errorf("second part lost content: %q", parts[1])
	}
}

func TestApplyPatchOptions_DefaultFieldManager(t *testing.T) {
	po := applyPatchOptions(false)
	if po.FieldManager != FieldManager {
		t.Errorf("FieldManager: got %q, want %q", po.FieldManager, FieldManager)
	}
	if FieldManager != "gkebnkctl" {
		t.Errorf("constant FieldManager value: got %q, want \"gkebnkctl\"", FieldManager)
	}
	if po.Force != nil {
		t.Errorf("Force should be nil when force=false; got %v", *po.Force)
	}
}

func TestApplyPatchOptions_Force(t *testing.T) {
	po := applyPatchOptions(true)
	if po.Force == nil || !*po.Force {
		t.Errorf("Force: expected non-nil true; got %v", po.Force)
	}
}

func TestLoadObjects_MissingFile(t *testing.T) {
	o := &ApplyOptions{Filename: filepath.Join(t.TempDir(), "nope.yaml")}
	_, err := o.loadObjects()
	if err == nil {
		t.Fatal("expected error for missing file; got nil")
	}
}

func TestLoadObjects_DirectoryRecursesYAMLOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.yaml"), []byte(sampleConfigMapYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	o := &ApplyOptions{Filename: dir}
	objs, err := o.loadObjects()
	if err != nil {
		t.Fatalf("loadObjects err: %v", err)
	}
	if len(objs) != 1 {
		t.Errorf("expected 1 object (README.md skipped); got %d", len(objs))
	}
}

func TestLoadObjects_SingleFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.yaml")
	if err := os.WriteFile(p, []byte(sampleConfigMapYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	o := &ApplyOptions{Filename: p}
	objs, err := o.loadObjects()
	if err != nil {
		t.Fatalf("loadObjects err: %v", err)
	}
	if len(objs) != 1 {
		t.Errorf("single file: expected 1 object; got %d", len(objs))
	}
}

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
