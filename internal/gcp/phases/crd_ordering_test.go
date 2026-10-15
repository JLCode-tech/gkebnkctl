package phases

import (
	"context"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakek8s "k8s.io/client-go/kubernetes/fake"
)

// The k8s.f5net.com CRDs are registered by FLO's crd-installer Job, which only
// runs once Phase38 has applied the CNEInstance. Phase36 must not wait on them:
// doing so burned 3 minutes each, timed out, and Phase40 then failed anyway
// with `no matches for kind "License"`.
func TestFLOCRDSplit_Phase36WaitsOnlyForManifestShippedCRDs(t *testing.T) {
	for _, crd := range floCRDs {
		if strings.HasSuffix(crd, "k8s.f5net.com") {
			t.Errorf("floCRDs contains %q, which FLO's crd-installer only creates after Phase38 — it belongs in floInstalledCRDs", crd)
		}
	}
	if len(floCRDs) == 0 {
		t.Error("floCRDs is empty — Phase36 should still confirm the CRDs its own manifest ships")
	}
}

// Phase39 must wait for FLO to create the Deployment rather than skipping it:
// skipping left f5-crdconversion permanently in CrashLoopBackOff.
func TestWaitForDeploymentExists_ReturnsOnceCreated(t *testing.T) {
	client := fakek8s.NewClientset()
	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = client.AppsV1().Deployments("f5-cne-system").Create(context.Background(),
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "f5-crdconversion", Namespace: "f5-cne-system"}},
			metav1.CreateOptions{})
	}()
	if err := waitForDeploymentExists(context.Background(), client, "f5-cne-system", "f5-crdconversion", 10*time.Second); err != nil {
		t.Errorf("waitForDeploymentExists: %v", err)
	}
}

func TestWaitForDeploymentExists_TimesOutWhenAbsent(t *testing.T) {
	client := fakek8s.NewClientset()
	err := waitForDeploymentExists(context.Background(), client, "f5-cne-system", "never-appears", 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected a timeout error when the deployment is never created")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("err = %v, want a timeout error", err)
	}
}

// A CRD's metadata.name is "<plural>.<group>" and F5's SPK plurals are
// hyphenated (f5-spk-vlans.k8s.f5net.com). The un-hyphenated spellings match no
// CRD, so the wait silently times out — 3 minutes each — and guards nothing.
// Verified live 2026-07-21 against `kubectl get crd`.
func TestFLOInstalledCRDs_UseHyphenatedSPKPlurals(t *testing.T) {
	for _, crd := range floInstalledCRDs {
		if strings.HasPrefix(crd, "f5spk") {
			t.Errorf("%q uses the un-hyphenated plural; the real CRD is f5-spk-... — this wait would never match", crd)
		}
	}
	want := map[string]bool{
		"f5-spk-vlans.k8s.f5net.com":        false,
		"f5-spk-staticroutes.k8s.f5net.com": false,
		"f5-spk-egresses.k8s.f5net.com":     false,
		"licenses.k8s.f5net.com":            false,
	}
	for _, c := range floInstalledCRDs {
		if _, ok := want[c]; ok {
			want[c] = true
		}
	}
	for crd, found := range want {
		if !found {
			t.Errorf("floInstalledCRDs is missing the live CRD name %q, got %v", crd, floInstalledCRDs)
		}
	}
}
