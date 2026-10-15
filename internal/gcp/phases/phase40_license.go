package phases

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/manifests"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/render"
)

// licenseCRTemplatePath is the embedded template Phase40License renders.
const licenseCRTemplatePath = "templates/license-cr.yaml.tmpl"

var licenseGVR = schema.GroupVersionResource{
	Group:    "k8s.f5net.com",
	Version:  "v1",
	Resource: "licenses",
}

// Phase40License reads the JWT at cl.Bnk.JwtPath, trims whitespace, renders
// the License/bnk-license CR (connected mode, raw un-encoded JWT — live-
// proven, do NOT base64-encode it) entirely in memory, and applies it via
// dynamic SSA. The JWT content is never written to disk — it is read at
// phase entry and inlined directly into the render call
// (docs/SPEC-go-tracer.md §2 "Secrets constraint").
func Phase40License(ctx context.Context, cl *intent.Cluster, _ *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 40] License: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 40] would apply License/bnk-license in f5-cne-core (connected mode, JWT never written to disk)")
		return nil
	}

	jwtRaw, err := os.ReadFile(cl.Bnk.JwtPath) // #nosec G304 -- path is operator-supplied via cluster.yaml, existence-validated at intent.Load
	if err != nil {
		return fmt.Errorf("phase40: reading JWT %s: %w", cl.Bnk.JwtPath, err)
	}
	jwt := strings.TrimSpace(string(jwtRaw))

	tmplBytes, err := manifests.FS.ReadFile(licenseCRTemplatePath)
	if err != nil {
		return fmt.Errorf("phase40: reading embedded license-cr template: %w", err)
	}
	rendered, err := render.RenderLicenseCR(tmplBytes, render.LicenseCRVars{JWT: jwt})
	if err != nil {
		return fmt.Errorf("phase40: rendering license-cr: %w", err)
	}

	// Always sync cpcl-key-secret so JWKS verification matches current key-cm.
	syncCPCLKeySecret(ctx, clients)

	// Check whether License/bnk-license in f5-utils is already Active.
	if isLicenseActive(ctx, clients, "f5-utils", "bnk-license") {
		fmt.Fprintln(os.Stderr, "[phase 40] License/bnk-license is already Active in f5-utils, skipping CPCL purge")
	} else {
		fmt.Fprintln(os.Stderr, "[phase 40] License not Active; purging stale CPCL secrets and resetting CWC pod")
		// Delete existing License CRs to allow clean recreation
		deleteLicenseCR(ctx, clients, "f5-utils", "bnk-license")
		deleteLicenseCR(ctx, clients, "f5-cne-core", "bnk-license")
		purgeStaleCPCLSecrets(ctx, clients)
		bounceCWCPod(ctx, clients)
	}

	if err := applyLicenseWithQuotaRetry(ctx, clients, rendered); err != nil {
		return fmt.Errorf("phase40: applying License CR: %w", err)
	}

	// Also apply License CR to f5-utils where f5-spk-cwc runs, ensuring CWC immediately detects and activates it.
	utilsRendered := strings.Replace(string(rendered), "namespace: f5-cne-core", "namespace: f5-utils", 1)
	if uErr := applyLicenseWithQuotaRetry(ctx, clients, []byte(utilsRendered)); uErr != nil {
		fmt.Fprintf(os.Stderr, "[phase 40] warn: applying License CR in f5-utils: %v\n", uErr)
	}

	fmt.Fprintln(os.Stderr, "[phase 40] License OK")
	return nil
}

func isLicenseActive(ctx context.Context, clients *gcp.Clients, ns, name string) bool {
	if clients == nil || clients.Dynamic == nil {
		return false
	}
	obj, err := clients.Dynamic.Resource(licenseGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil || obj == nil {
		return false
	}
	state, found, err := unstructured.NestedString(obj.Object, "status", "state")
	if err == nil && found && strings.EqualFold(state, "Active") {
		return true
	}
	return false
}

func deleteLicenseCR(ctx context.Context, clients *gcp.Clients, ns, name string) {
	if clients == nil || clients.Dynamic == nil {
		return
	}
	_ = clients.Dynamic.Resource(licenseGVR).Namespace(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

func syncCPCLKeySecret(ctx context.Context, clients *gcp.Clients) {
	if clients == nil || clients.K8s == nil {
		return
	}

	var fallbackJWTKey string
	if cm, err := clients.K8s.CoreV1().ConfigMaps("f5-utils").Get(ctx, "cpcl-key-cm", metav1.GetOptions{}); err == nil && cm != nil {
		fallbackJWTKey = cm.Data["jwt.key"]
	}

	for _, ns := range []string{"f5-utils", "f5-cne-core"} {
		jwtKey := fallbackJWTKey
		if cm, err := clients.K8s.CoreV1().ConfigMaps(ns).Get(ctx, "cpcl-key-cm", metav1.GetOptions{}); err == nil && cm != nil && cm.Data["jwt.key"] != "" {
			jwtKey = cm.Data["jwt.key"]
		}
		if jwtKey == "" {
			continue
		}
		sec, err := clients.K8s.CoreV1().Secrets(ns).Get(ctx, "cpcl-key-secret", metav1.GetOptions{})
		if err != nil || sec == nil {
			if apierrors.IsNotFound(err) {
				newSec := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "cpcl-key-secret",
						Namespace: ns,
					},
					Data: map[string][]byte{
						"jwt.key": []byte(jwtKey),
					},
				}
				if _, createErr := clients.K8s.CoreV1().Secrets(ns).Create(ctx, newSec, metav1.CreateOptions{}); createErr == nil {
					fmt.Fprintf(os.Stderr, "[phase 40] created cpcl-key-secret from cpcl-key-cm in %s\n", ns)
				}
			}
			continue
		}
		if sec.Data == nil {
			sec.Data = make(map[string][]byte)
		}
		sec.Data["jwt.key"] = []byte(jwtKey)
		if _, updErr := clients.K8s.CoreV1().Secrets(ns).Update(ctx, sec, metav1.UpdateOptions{}); updErr == nil {
			fmt.Fprintf(os.Stderr, "[phase 40] synced cpcl-key-secret from cpcl-key-cm in %s\n", ns)
		}
	}
}

// purgeStaleCPCLSecrets deletes stale CPCL/CWC state secrets in f5-utils, f5-cne-core, and f5-bnk.
func purgeStaleCPCLSecrets(ctx context.Context, clients *gcp.Clients) {
	if clients == nil || clients.K8s == nil {
		return
	}

	staleSecrets := []string{
		"context",
		"initialregistrationstatus",
		"licensestatus",
		"activationmessage",
		"activationstatus",
		"statehistory",
		"switchlicensestatus",
		"telemetrystatus",
		"telemetryreports",
		"f5-Flo-lic-msg",
		"f5-cwc-state",
		"cpcl-Dynamic-state",
		"cpcl-config-secret",
	}
	for _, ns := range []string{"f5-utils", "f5-cne-core", "f5-bnk"} {
		for _, name := range staleSecrets {
			err := clients.K8s.CoreV1().Secrets(ns).Delete(ctx, name, metav1.DeleteOptions{})
			if err != nil && !apierrors.IsNotFound(err) {
				continue
			}
			if err == nil {
				fmt.Fprintf(os.Stderr, "[phase 40] purged stale CPCL secret %s/%s\n", ns, name)
			}
		}
	}
}

const (
	licenseQuotaRetryAttempts = 12
	licenseQuotaRetryInterval = 5 * time.Second
	quotaStatusUnknownMarker  = "status unknown for quota"
)

func applyLicenseWithQuotaRetry(ctx context.Context, clients *gcp.Clients, rendered []byte) error {
	return retryOnQuotaStatusUnknown(ctx, licenseQuotaRetryInterval, func() error {
		return applyManifestBytes(ctx, clients, rendered, false)
	})
}

func retryOnQuotaStatusUnknown(ctx context.Context, interval time.Duration, apply func() error) error {
	attempt := 0
	var lastErr error
	err := gcp.PollUntil(ctx, interval, time.Duration(licenseQuotaRetryAttempts)*interval+time.Second, func(ctx context.Context) (bool, error) {
		attempt++
		lastErr = apply()
		if lastErr == nil {
			return true, nil
		}
		if !strings.Contains(lastErr.Error(), quotaStatusUnknownMarker) {
			return false, lastErr
		}
		if attempt >= licenseQuotaRetryAttempts {
			return false, lastErr
		}
		fmt.Fprintf(os.Stderr, "[phase 40] License rejected while the ResourceQuota controller catches up (attempt %d/%d) — retrying in %s\n",
			attempt, licenseQuotaRetryAttempts, interval)
		return false, nil
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return lastErr
		}
		return err
	}
	return nil
}

func bounceCWCPod(ctx context.Context, clients *gcp.Clients) {
	if clients == nil || clients.K8s == nil {
		return
	}
	pods, err := clients.K8s.CoreV1().Pods("f5-utils").List(ctx, metav1.ListOptions{
		LabelSelector: "app=cwc",
	})
	if err != nil {
		return
	}
	for _, p := range pods.Items {
		fmt.Fprintf(os.Stderr, "[phase 40] deleting CWC pod %s/%s for clean CPCL restart\n", "f5-utils", p.Name)
		_ = clients.K8s.CoreV1().Pods("f5-utils").Delete(ctx, p.Name, metav1.DeleteOptions{})
	}

	// Poll until a CWC pod is running and ready. Because CWC mounts an RWO PVC,
	// we allow sufficient time for the old pod to terminate and release the volume.
	_ = gcp.PollUntil(ctx, 3*time.Second, 90*time.Second, func(c context.Context) (bool, error) {
		pList, lErr := clients.K8s.CoreV1().Pods("f5-utils").List(c, metav1.ListOptions{
			LabelSelector: "app=cwc",
		})
		if lErr != nil || len(pList.Items) == 0 {
			return false, nil
		}
		for _, pod := range pList.Items {
			if pod.DeletionTimestamp != nil {
				continue
			}
			for _, cond := range pod.Status.Conditions {
				if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
					fmt.Fprintf(os.Stderr, "[phase 40] CWC pod %s is ready\n", pod.Name)
					return true, nil
				}
			}
		}
		return false, nil
	})
}
