package phases

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func newObj(kind, name string, data map[string]interface{}) *unstructured.Unstructured {
	o := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       kind,
		"metadata":   map[string]interface{}{"name": name, "namespace": "f5-utils"},
	}
	if data != nil {
		o["data"] = data
	}
	return &unstructured.Unstructured{Object: o}
}

// The redacted Secret must be dropped: its sentinel data is not valid base64,
// so applying it aborts Phase36 and every phase after it.
func TestDropRedactedSecrets_DropsRedactedSecret(t *testing.T) {
	objs := []*unstructured.Unstructured{
		newObj("Secret", "external-otelsvr-secret", map[string]interface{}{
			"tls.key": "REDACTED-BY-GKEBNKCTL-see-reference/RECON-REDACTIONS.md",
		}),
	}
	if got := dropRedactedSecrets(objs); len(got) != 0 {
		t.Errorf("redacted Secret was kept: %v", got[0].GetName())
	}
}

// A ConfigMap carrying the sentinel is plain string data — it applies fine and
// must NOT be dropped (cpcl-config-cm's deprecated jwt key).
func TestDropRedactedSecrets_KeepsRedactedConfigMap(t *testing.T) {
	objs := []*unstructured.Unstructured{
		newObj("ConfigMap", "cpcl-config-cm", map[string]interface{}{
			"jwt": "REDACTED-BY-GKEBNKCTL-see-reference/RECON-REDACTIONS.md",
		}),
	}
	if got := dropRedactedSecrets(objs); len(got) != 1 {
		t.Error("ConfigMap with sentinel must be kept — ConfigMap data is not base64")
	}
}

// Real secrets must survive untouched.
func TestDropRedactedSecrets_KeepsRealSecret(t *testing.T) {
	objs := []*unstructured.Unstructured{
		newObj("Secret", "far-secret", map[string]interface{}{"tls.key": "dGVzdA=="}),
		newObj("Deployment", "flo", nil),
	}
	if got := dropRedactedSecrets(objs); len(got) != 2 {
		t.Errorf("non-redacted objects must be kept, got %d of 2", len(got))
	}
}

// Guards the real vendored manifest: exactly the documented Secret is dropped.
func TestDropRedactedSecrets_AgainstVendoredFLOManifest(t *testing.T) {
	data, err := manifestsFSReadFile(floManifestPath)
	if err != nil {
		t.Fatalf("reading vendored FLO manifest: %v", err)
	}
	objs, err := parseYAMLStreamForTest(data)
	if err != nil {
		t.Fatalf("parsing vendored FLO manifest: %v", err)
	}
	kept := dropRedactedSecrets(objs)
	dropped := len(objs) - len(kept)
	if dropped != 1 {
		t.Errorf("dropped %d objects from the vendored manifest, want exactly 1 (external-otelsvr-secret)", dropped)
	}
	for _, o := range kept {
		if o.GetKind() == "Secret" && secretHasRedactedData(o) {
			t.Errorf("a redacted Secret survived: %s/%s", o.GetNamespace(), o.GetName())
		}
	}
}
