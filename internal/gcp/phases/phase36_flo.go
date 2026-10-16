package phases

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/manifests"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/render"
	"github.com/JLCode-tech/gkebnkctl/internal/manifest"
)

const (
	floManifestPath        = "flo/flo-manifest.yaml"
	gatewayAPICRDsPath     = "static/00-gateway-api-crds.yaml"
	bnkGatewayCRDsPath     = "static/02-bnk-gateway-crds.yaml"
	gieCRDsPath            = "gie/gie-crds.yaml"
	gatewayClassController = "f5.com/f5-cne-system-f5-cne-controller"
	floCRDWait             = 3 * time.Minute
)

var supplementalCRDManifests = []string{
	gatewayAPICRDsPath,
	gieCRDsPath,
	bnkGatewayCRDsPath,
}

var floCRDs = []string{
	"cneinstances.k8s.f5.com",
	"infras.gateway.k8s.f5.com",
	"l2networks.gateway.k8s.f5.com",
	"l3networks.gateway.k8s.f5.com",
	"gatewaysettings.gateway.k8s.f5.com",
	"egressgateways.gateway.k8s.f5.com",
	"backends.gateway.k8s.f5.com",
}

var floInstalledCRDs = []string{
	"licenses.k8s.f5net.com",
	"f5-spk-vlans.k8s.f5net.com",
	"f5-spk-staticroutes.k8s.f5net.com",
	"f5-spk-egresses.k8s.f5net.com",
}

func renderClusterGatewayClass(clusterName string) []byte {
	return []byte(fmt.Sprintf(`apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: f5-gateway-class
spec:
  controllerName: %s
  description: "F5 BIG-IP Kubernetes Gateway"
---
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: %s-gatewayclass
spec:
  controllerName: %s
  description: "F5 BIG-IP Kubernetes Gateway"
`, gatewayClassController, clusterName, gatewayClassController))
}

// Phase36FLO dynamically pulls and renders the FLO Helm chart from
// oci://repo.f5.com/charts/f5-lifecycle-operator when a FAR archive is configured,
// falling back to the embedded FLO operator manifest (~3 MB, 23 CRDs) otherwise,
// plus Gateway API / GIE / BNK 2.4 supplemental CRDs and GatewayClasses
// (f5-gateway-class, <cluster>-gatewayclass) via SERVER-SIDE dynamic SSA.
func Phase36FLO(ctx context.Context, cl *intent.Cluster, st *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 36] FLO: cluster=%s\n", cl.Metadata.Name)

	floVersion := cl.FLOVersion()
	if dryRun {
		fmt.Fprintf(os.Stderr, "[phase 36] would server-side-apply FLO operator manifest (%s) + Gateway API/GIE/BNK CRDs + GatewayClasses (f5-gateway-class, %s-gatewayclass)\n", floVersion, cl.Metadata.Name)
		if st != nil {
			st.Set("FLO_RELEASE_NAME", FLOReleaseName)
			st.Set("FLO_VERSION", floVersion)
			st.Set("FLO_NAMESPACE", FLONamespace)
		}
		return nil
	}

	if cl != nil && cl.Metadata.Name != "" {
		_ = applyManifestBytes(ctx, clients, renderClusterCAIssuer(cl.Metadata.Name), true)
	}

	var data []byte
	var err error

	farPath := cl.Bnk.FARArchive
	if farPath == "" {
		farPath = cl.Bnk.FarSecret.DockerConfigFile
	}
	if farPath == "" {
		farPath = os.Getenv("FAR_AUTH_KEY_PATH")
	}
	if farPath != "" {
		if _, farKeyB64, authErr := manifest.ExtractFARAuth(farPath); authErr == nil && farKeyB64 != "" {
			if m, pErr := manifest.Probe(ctx, cl.Bnk.ManifestVersion, farPath); pErr == nil {
				if v, ok := m.HelmCharts["f5-lifecycle-operator"]; ok && v != "" {
					floVersion = v
				}
			}
			var jwtStr string
			if cl.Bnk.JwtPath != "" {
				if jwtBytes, jErr := os.ReadFile(cl.Bnk.JwtPath); jErr == nil {
					jwtStr = strings.TrimSpace(string(jwtBytes))
				}
			}
			if valsTmpl, rErr := manifests.FS.ReadFile(FLOValuesTemplatePath); rErr == nil {
				if renderedVals, rErr := render.RenderFLOValues(valsTmpl, cl, jwtStr); rErr == nil {
					var valuesMap map[string]interface{}
					if yaml.Unmarshal(renderedVals, &valuesMap) == nil {
						if helmBytes, hErr := k8s.PullAndRenderFLOChart(FLOChartRef, floVersion, FLOReleaseName, FLONamespace, farKeyB64, valuesMap); hErr == nil && len(helmBytes) > 0 {
							data = helmBytes
						} else if hErr != nil {
							fmt.Fprintf(os.Stderr, "[phase 36] warn: OCI Helm chart pull failed (%v); falling back to embedded FLO manifest\n", hErr)
						}
					}
				}
			}
		}
	}

	if len(data) == 0 {
		data, err = manifests.FS.ReadFile(floManifestPath)
		if err != nil {
			return fmt.Errorf("phase36: reading embedded FLO manifest: %w", err)
		}
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

	if st != nil {
		st.Set("FLO_RELEASE_NAME", FLOReleaseName)
		st.Set("FLO_VERSION", floVersion)
		st.Set("FLO_NAMESPACE", FLONamespace)
		st.Set("FLO_INSTALLED_AT", time.Now().UTC().Format(time.RFC3339))
	}

	for _, crdManifest := range supplementalCRDManifests {
		crdBytes, err := manifests.FS.ReadFile(crdManifest)
		if err != nil {
			return fmt.Errorf("phase36: reading embedded %s: %w", crdManifest, err)
		}
		if err := applyManifestBytes(ctx, clients, crdBytes, true); err != nil {
			return fmt.Errorf("phase36: applying %s: %w", crdManifest, err)
		}
	}

	gwClassBytes := renderClusterGatewayClass(cl.Metadata.Name)
	if err := applyManifestBytes(ctx, clients, gwClassBytes, true); err != nil {
		return fmt.Errorf("phase36: applying GatewayClasses (f5-gateway-class, %s-gatewayclass): %w", cl.Metadata.Name, err)
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

const redactionSentinel = "REDACTED-BY-GKEBNKCTL"

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
