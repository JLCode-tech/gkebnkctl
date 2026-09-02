package manifest

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

const sampleManifest = `
f5_helm_repo: "https://repo.f5.com/helm"
f5_docker_repo: "repo.f5.com/f5-bnk"
releases:
  - version: "2.4.0-3.2882.0"
    helm_charts:
      - name: "f5-bigip-k8s-manifest"
        version: "2.4.0-3.2882.0"
      - name: "f5-spk-ingress"
        version: "0.0.392"
    docker_images:
      - name: "f5-cne-controller"
        version: "2.4.0-3.2882.0"
`

func TestParseReleaseManifest(t *testing.T) {
	m, err := ParseReleaseManifest([]byte(sampleManifest))
	if err != nil {
		t.Fatalf("ParseReleaseManifest failed: %v", err)
	}

	if m.Version != "2.4.0-3.2882.0" {
		t.Errorf("expected version 2.4.0-3.2882.0, got %s", m.Version)
	}
	if m.HelmCharts["f5-spk-ingress"] != "0.0.392" {
		t.Errorf("expected f5-spk-ingress version 0.0.392, got %s", m.HelmCharts["f5-spk-ingress"])
	}

	var buf bytes.Buffer
	m.PrintSummary(&buf)
	if !strings.Contains(buf.String(), "f5-spk-ingress") {
		t.Errorf("expected summary to contain f5-spk-ingress, got:\n%s", buf.String())
	}
}

func TestProbeEmbedded(t *testing.T) {
	ctx := context.Background()
	m, err := Probe(ctx, "2.4.0-3.2882.0", "")
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	if m.Version != "2.4.0-3.2882.0" {
		t.Errorf("version mismatch: %s", m.Version)
	}
}
