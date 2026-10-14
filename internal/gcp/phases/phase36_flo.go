package phases

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/manifests"
)

const (
	floManifestPath = "flo/flo-manifest.yaml"
	floCRDWait      = 3 * time.Minute
)

// floCRDs are the CRDs the FLO operator manifest itself ships, so they are the
// only ones Phase36 can meaningfully wait on.
//
// The k8s.f5net.com CRDs (licenses, f5spkvlans, f5spkstaticroutes,
// f5spkegresses) are NOT in that manifest: FLO creates them from a
// `crd-installer` Job that it only spawns once Phase38 has applied the
// CNEInstance. Waiting for them here therefore burned 3 minutes each, timed out
// four times, and then Phase40 died anyway with
// `no matches for kind "License"` (live-hit 2026-07-21 on cluster
// bnk-scratch). They are waited for in Phase38 instead — see floInstalledCRDs.
var floCRDs = []string{
	"cneinstances.k8s.f5.com",
}

// floInstalledCRDs are created by FLO's crd-installer Job after the CNEInstance
// exists. Phase38 waits for them so the phases that apply CRs against them —
// Phase40 (License), Phase41 (F5SPKVlan/F5SPKStaticRoute), Phase50
// (F5SPKEgress), Phase51 (F5SPKStaticRoute) — cannot run before the kinds are
// registered.
// A CRD's metadata.name is "<plural>.<group>", and F5's SPK plurals are
// HYPHENATED — f5-spk-vlans, not f5spkvlans (verified live 2026-07-21:
// `kubectl get crd` returns f5-spk-vlans.k8s.f5net.com etc.). The
// un-hyphenated spellings previously used here matched nothing, so every run
// burned 3 minutes per CRD timing out and the wait guarded nothing. Only
// licenses.k8s.f5net.com was ever correct.
var floInstalledCRDs = []string{
	"licenses.k8s.f5net.com",
	"f5-spk-vlans.k8s.f5net.com",
	"f5-spk-staticroutes.k8s.f5net.com",
	"f5-spk-egresses.k8s.f5net.com",
}

// Phase36FLO applies the embedded FLO operator manifest (~3 MB, 23 CRDs)
// via SERVER-SIDE dynamic SSA — client-side apply exceeds the CRD annotation
// size limit, per docs/SPEC-go-tracer.md §3 Phase36 / §9.8 — then waits for
// the F5 CRDs to establish.
func Phase36FLO(ctx context.Context, cl *intent.Cluster, _ *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 36] FLO: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 36] would server-side-apply FLO operator manifest (~3 MB, 23 CRDs)")
		return nil
	}

	// Force SSA (Force: true) instead of plain kubectl apply --server-side.
	// Client-side apply would exceed CRD annotation size limits (23 CRDs, ~3 MB manifest).
	// Force ensures idempotent application even if the manifest has been partially applied.
	data, err := manifests.FS.ReadFile(floManifestPath)
	if err != nil {
		return fmt.Errorf("phase36: reading embedded FLO manifest: %w", err)
	}
	objs, err := k8s.ParseYAMLStream(data)
	if err != nil {
		return fmt.Errorf("phase36: parsing FLO manifest: %w", err)
	}
	objs = dropRedactedSecrets(objs)
	updateFloDeploymentImage(objs, cl.Bnk.ImageRegistry)
	if err := applyObjects(ctx, clients, objs, true); err != nil {
		return fmt.Errorf("phase36: applying %s: %w", floManifestPath, err)
	}

	if clients.Dynamic == nil {
		fmt.Fprintln(os.Stderr, "[phase 36] warn: Clients.Dynamic is nil — skipping CRD-established wait")
		return nil
	}
	for _, crd := range floCRDs {
		fmt.Fprintf(os.Stderr, "[phase 36] waiting for CRD %s to establish (up to %s)\n", crd, floCRDWait)
		if err := k8s.WaitForCRDEstablished(ctx, clients.Dynamic, crd, floCRDWait); err != nil {
			fmt.Fprintf(os.Stderr, "[phase 36] warn: CRD %s did not establish within %s (%v) — check FLO operator pods (kubectl get pods -n f5-cne-core); continuing (wait is best-effort)\n",
				crd, floCRDWait, err)
		}
	}

	fmt.Fprintln(os.Stderr, "[phase 36] FLO OK")
	return nil
}

// redactionSentinel marks values scrubbed out of the vendored FLO manifest
// before it was committed — see reference/RECON-REDACTIONS.md.
const redactionSentinel = "REDACTED-BY-GKEBNKCTL"

// dropRedactedSecrets removes Secret objects whose data still carries the
// redaction sentinel.
//
// The vendored FLO manifest is a real capture that had live secret material
// scrubbed from it before commit. A Secret's `data` values must be base64, and
// the sentinel is not, so applying it verbatim is rejected by the API server:
//
//	failed to convert new object (f5-utils/external-otelsvr-secret) ...
//	error decoding from json: illegal base64 data at input byte 8
//
// which aborted Phase36 and every phase after it (live-hit 2026-07-21 on
// cluster bnk-scratch). Per reference/RECON-REDACTIONS.md the only affected
// Secret is f5-utils/external-otelsvr-secret, which is self-managed by the
// FLO/CNE operator for its otel endpoint and off this seed's critical path
// (telemetry is disabled in the CNEInstance template); the doc's explicit
// guidance is to let the operator regenerate it rather than restore the
// scrubbed values.
//
// Only Secrets are dropped. ConfigMap data is plain strings, so the sentinel
// in cpcl-config-cm's deprecated `jwt` key applies cleanly and is harmless —
// it is superseded by the License CR that Phase40 applies.
func dropRedactedSecrets(objs []*unstructured.Unstructured) []*unstructured.Unstructured {
	kept := make([]*unstructured.Unstructured, 0, len(objs))
	for _, obj := range objs {
		if obj.GetKind() == "Secret" && secretHasRedactedData(obj) {
			fmt.Fprintf(os.Stderr,
				"[phase 36] skipping Secret %s/%s — its data was redacted before commit (see reference/RECON-REDACTIONS.md); the operator regenerates it\n",
				obj.GetNamespace(), obj.GetName())
			continue
		}
		kept = append(kept, obj)
	}
	return kept
}

func secretHasRedactedData(obj *unstructured.Unstructured) bool {
	data, found, err := unstructured.NestedStringMap(obj.Object, "data")
	if err != nil || !found {
		return false
	}
	for _, v := range data {
		if strings.Contains(v, redactionSentinel) {
			return true
		}
	}
	return false
}

func updateFloDeploymentImage(objs []*unstructured.Unstructured, registry string) {
	if registry == "" {
		return
	}
	for _, obj := range objs {
		if obj.GetKind() == "Deployment" && obj.GetName() == "flo-f5-lifecycle-operator" {
			containers, found, err := unstructured.NestedSlice(obj.Object, "spec", "template", "spec", "containers")
			if err == nil && found {
				for _, c := range containers {
					if cMap, ok := c.(map[string]interface{}); ok {
						if img, ok := cMap["image"].(string); ok {
							parts := strings.Split(img, "/images/")
							if len(parts) == 2 {
								cMap["image"] = fmt.Sprintf("%s/images/%s", registry, parts[1])
							}
						}
					}
				}
				_ = unstructured.SetNestedSlice(obj.Object, containers, "spec", "template", "spec", "containers")
			}
		}
	}
}
