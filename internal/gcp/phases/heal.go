package phases

// heal.go -- the one table of cluster-side repairs. Every entry has a Detect
// (read-only; doctor prints it) and a Fix (idempotent; bnk heal runs it).
// The up phases and bnk upgrade call the same functions, so a repair is
// written once and reaches all three surfaces.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
	k8smanifests "github.com/JLCode-tech/gkebnkctl/internal/k8s/manifests"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/render"
)

const (
	K8sTestNamespace       = "gkebnkctl-test"
	InstanceNamespace      = "f5-bnk"
	OperatorNamespace      = "f5-cne-core"
	cneInstanceResource    = "cneinstances"
	cwcNamespace           = OperatorNamespace
	cwcLabelSelector       = "app=cwc"
	cwcContainerName       = "f5-spk-cwc"
	cwcRestartThreshold    = int32(3)
	h4DeploymentName       = "f5-cne-controller"
	h4ContainerName        = "f5-tmm-pod-manager"
	h4RestartThreshold     = int32(2)
	dssmConfigMapName      = "f5-dssm"
	dssmLabelSelector      = "app in (f5-dssm-db,f5-dssm-sentinel)"
	dssmReadinessSelector  = "app in (f5-dssm-db,f5-dssm-sentinel)"
	dssmInsecureMarker     = "--tls --insecure"
	dssmTLSReplace         = " --tls "
	dssmTLSInsecureReplace = " --tls --insecure "
	healWait               = 3 * time.Minute
	healPoll               = 5 * time.Second
	dssmCMWaitHeal         = 30 * time.Second
)

// Clients holds the API clients needed by heal repairs.
type Clients struct {
	GCP     *gcp.Clients
	K8s     kubernetes.Interface
	Dynamic dynamic.Interface
	Project string
	Region  string
}

// AttachK8s initializes the K8s and Dynamic clients from the given kubeconfig.
func (c *Clients) AttachK8s(kubeconfigPath string) error {
	cfg, err := k8s.BuildRESTConfig(kubeconfigPath)
	if err != nil {
		return err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return err
	}
	c.K8s = cs
	c.Dynamic = dyn
	return nil
}

// NewClients creates a Clients struct with GCP clients.
func NewClients(ctx context.Context, project, region string) (*Clients, error) {
	gcpClients, err := gcp.NewClients(ctx, project)
	if err != nil {
		return &Clients{Project: project, Region: region}, nil
	}
	return &Clients{
		GCP:     gcpClients,
		Project: project,
		Region:  region,
	}, nil
}

// HealDeps is what a repair may use. Clients.K8s is required; Cluster, State
// and the GCP clients are present only when the caller passed -f.
type HealDeps struct {
	Cluster *intent.Cluster
	State   *state.State
	Clients *Clients
	DryRun  bool
	Log     io.Writer
}

func (d *HealDeps) hasIntent() bool { return d.Cluster != nil && d.State != nil }

// Repair is one detect/fix pair.
type Repair struct {
	// Name is the CLI key (bnk heal --only <name>).
	Name string
	// Title is the doctor row name.
	Title string
	// NeedsIntent marks a Fix that needs cluster.yaml, state.env and the cloud
	// clients (bnk heal -f). Detect always works with the kubeconfig alone.
	NeedsIntent bool
	// Detect returns healthy=true with a detail when nothing needs doing;
	// healthy=false with a detail when Fix should run. err means it could not
	// tell.
	Detect func(ctx context.Context, d *HealDeps) (healthy bool, detail string, err error)
	// Fix repairs and returns what it did.
	Fix func(ctx context.Context, d *HealDeps) (detail string, err error)
}

// HealStatus is the outcome of one repair in a bnk heal run.
type HealStatus struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Healthy bool   `json:"healthy"`
	// Changed is true when Fix ran and wrote to the cluster.
	Changed bool `json:"changed"`
	// Skipped is true when the repair needed -f and heal ran without it.
	Skipped bool   `json:"skipped"`
	Detail  string `json:"detail"`
	Error   string `json:"error,omitempty"`
}

var healCNEInstanceGVR = schema.GroupVersionResource{Group: "k8s.f5.com", Version: "v1", Resource: cneInstanceResource}

// Repairs is the registry, in the order bnk heal runs them.
var Repairs = []Repair{
	{
		Name: "multus-token-watch", Title: "multus kubeconfig token",
		Detect: func(ctx context.Context, d *HealDeps) (bool, string, error) {
			st, _, err := k8s.InspectMultus(ctx, d.Clients.K8s)
			switch {
			case err != nil:
				return false, "", err
			case !st.Installed:
				return true, "Multus not installed", nil
			case !st.WatchEnabled && st.Unauthorized != "":
				return false, fmt.Sprintf("pod %s failed its network sandbox: Multus Unauthorized (expired kubeconfig token); the DaemonSet lacks %s", st.Unauthorized, k8s.MultusWatchFlag), nil
			case !st.WatchEnabled:
				return false, fmt.Sprintf("DaemonSet %s/%s writes its kubeconfig once at pod start (no %s): the token expires and new pods fail with Multus Unauthorized", k8s.MultusNamespace, k8s.MultusDaemonSet, k8s.MultusWatchFlag), nil
			case st.Unauthorized != "":
				return false, fmt.Sprintf("pod %s failed its network sandbox: Multus Unauthorized although the token watch is on", st.Unauthorized), nil
			}
			return true, "kubeconfig follows the service-account token (" + k8s.MultusWatchFlag + "), no Unauthorized sandbox events", nil
		},
		Fix: func(ctx context.Context, d *HealDeps) (string, error) {
			res, err := k8s.EnsureMultusTokenWatch(ctx, d.Clients.K8s, d.DryRun, d.Log)
			switch {
			case err != nil:
				return "", err
			case res.Patched:
				return k8s.MultusWatchFlag + " added to " + k8s.MultusNamespace + "/" + k8s.MultusDaemonSet + "; DaemonSet rolled", nil
			case res.Restarted:
				return k8s.MultusNamespace + "/" + k8s.MultusDaemonSet + " rolled", nil
			}
			return res.Reason, nil
		},
	},
	{
		Name: "tmm-log-stream", Title: "bnk tmm log stream",
		Detect: func(ctx context.Context, d *HealDeps) (bool, string, error) {
			on, err := k8s.TMMLogStreamEnabled(ctx, d.Clients.K8s)
			if err != nil {
				if apierrors.IsNotFound(err) {
					return true, k8s.TMMLogNamespace + "/" + k8s.TMMLogCustomConfigMap + " absent (BNK not installed yet)", nil
				}
				return false, "", err
			}
			if !on {
				return false, "stdout store off in " + k8s.TMMLogNamespace + "/" + k8s.TMMLogCustomConfigMap + ": `gkebnkctl logs tmm` and the governance collector see nothing", nil
			}
			return true, "f5-toda-fluentd prints the TMM lines (`gkebnkctl logs tmm --governance`)", nil
		},
		Fix: func(ctx context.Context, d *HealDeps) (string, error) {
			changed, err := k8s.EnableTMMLogStream(ctx, d.Clients.K8s, healWait)
			if err != nil {
				return "", err
			}
			if changed {
				return "stdout store enabled in " + k8s.TMMLogNamespace + "/" + k8s.TMMLogCustomConfigMap + "; fluentd pod bounced", nil
			}
			return "stdout store already on", nil
		},
	},
	{
		Name: "pod-manager", Title: "bnk tmm pod-manager",
		Detect: func(ctx context.Context, d *HealDeps) (bool, string, error) {
			return detectPodManager(ctx, d.Clients)
		},
		Fix: func(ctx context.Context, d *HealDeps) (string, error) {
			return fixPodManager(ctx, d.Clients, d.Log)
		},
	},
	{
		Name: "cwc", Title: "bnk cwc",
		Detect: func(ctx context.Context, d *HealDeps) (bool, string, error) {
			pod, ready, restarts, err := cwcPod(ctx, d.Clients)
			switch {
			case err != nil:
				return false, "", err
			case pod == nil:
				return true, "no cwc pod (BNK not installed yet)", nil
			case ready:
				return true, fmt.Sprintf("cwc Ready (restarts=%d)", restarts), nil
			case restarts >= cwcRestartThreshold:
				return false, fmt.Sprintf("cwc pod %s not Ready after %d restarts (DNS warm-up crash loop)", pod.Name, restarts), nil
			}
			return true, fmt.Sprintf("cwc pod %s starting (restarts=%d)", pod.Name, restarts), nil
		},
		Fix: func(ctx context.Context, d *HealDeps) (string, error) {
			pod, _, _, err := cwcPod(ctx, d.Clients)
			if err != nil || pod == nil {
				return "", err
			}
			if err := d.Clients.K8s.CoreV1().Pods(cwcNamespace).Delete(ctx, pod.Name, metav1.DeleteOptions{}); err != nil {
				return "", fmt.Errorf("delete cwc pod %s: %w", pod.Name, err)
			}
			if err := waitPodsReady(ctx, d.Clients, cwcNamespace, cwcLabelSelector, healWait); err != nil {
				return "", err
			}
			return "cwc pod " + pod.Name + " deleted; replacement Ready", nil
		},
	},
	{
		Name: "dssm-probe", Title: "bnk dssm readiness probe",
		Detect: func(ctx context.Context, d *HealDeps) (bool, string, error) {
			cm, err := d.Clients.K8s.CoreV1().ConfigMaps(InstanceNamespace).Get(ctx, dssmConfigMapName, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return true, InstanceNamespace + "/" + dssmConfigMapName + " absent (BNK not installed yet)", nil
			}
			if err != nil {
				return false, "", err
			}
			toPatch, done := dssmOverlayKeys(cm)
			if len(toPatch) == 0 {
				if done > 0 {
					return true, fmt.Sprintf("redis-cli --tls --insecure in %d probe scripts", done), nil
				}
				return true, "no redis-cli --tls probe scripts in " + dssmConfigMapName, nil
			}
			if allDSSMPodsReady(ctx, d.Clients) {
				return true, "dssm pods Ready; probe overlay not needed", nil
			}
			return false, fmt.Sprintf("dssm pods not Ready and %d probe scripts still verify the TLS hostname (redis-cli --tls without --insecure)", len(toPatch)), nil
		},
		Fix: func(ctx context.Context, d *HealDeps) (string, error) {
			patched, err := applyDSSMOverlay(ctx, d.Clients, d.Log)
			if err != nil {
				return "", err
			}
			if len(patched) == 0 {
				return "probe scripts already patched", nil
			}
			if d.State != nil {
				d.State.Set("DSSM_INSECURE_OVERLAY_APPLIED_AT", time.Now().UTC().Format(time.RFC3339))
				_ = d.State.Save()
			}
			return fmt.Sprintf("patched %s in %s; dssm pods bounced", strings.Join(patched, ", "), dssmConfigMapName), nil
		},
	},
	{
		Name: "controller-endpointslices", Title: "bnk controller endpointslices rbac", NeedsIntent: true,
		Detect: func(ctx context.Context, d *HealDeps) (bool, string, error) {
			return detectControllerRBAC(ctx, d.Clients)
		},
		Fix: func(ctx context.Context, d *HealDeps) (string, error) {
			tmpl, err := k8smanifests.FS.ReadFile(CNEControllerRBACYAMLPath)
			if err != nil {
				return "", err
			}
			rendered, err := render.RenderCNEControllerRBAC(tmpl, d.Cluster)
			if err != nil {
				return "", err
			}
			if err := applyRawYAML(ctx, d.Clients, rendered); err != nil {
				return "", err
			}
			return "ClusterRole/Binding " + render.CNEControllerRBACName(d.Cluster) + " applied", nil
		},
	},
	{
		Name: "tmm-k8s-routes", Title: "bnk tmm k8s routes", NeedsIntent: true,
		Detect: func(ctx context.Context, d *HealDeps) (bool, string, error) {
			return detectTMMK8sRoutes(ctx, d)
		},
		Fix: func(ctx context.Context, d *HealDeps) (string, error) {
			return fixTMMK8sRoutes(ctx, d)
		},
	},
	{
		Name: "epp-namespaces", Title: "bnk f5 endpoint picker namespaces",
		Detect: func(ctx context.Context, d *HealDeps) (bool, string, error) {
			return detectEPPNamespaces(ctx, d.Clients)
		},
		Fix: func(ctx context.Context, d *HealDeps) (string, error) {
			return fixEPPNamespaces(ctx, d.Clients)
		},
	},
	{
		Name: "test-namespace", Title: "gkebnkctl test namespace",
		Detect: func(ctx context.Context, d *HealDeps) (bool, string, error) {
			_, err := d.Clients.K8s.CoreV1().Namespaces().Get(ctx, K8sTestNamespace, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return false, "namespace " + K8sTestNamespace + " missing: test --backend k8s cannot create its probe Jobs", nil
			}
			if err != nil {
				return false, "", err
			}
			return true, "namespace " + K8sTestNamespace + " present for the test --backend k8s probe Jobs", nil
		},
		Fix: func(ctx context.Context, d *HealDeps) (string, error) {
			if _, err := ensureTestNamespace(ctx, d.Clients.K8s); err != nil {
				return "", err
			}
			return "namespace " + K8sTestNamespace + " created", nil
		},
	},
}

// RunHeal detects and, when unhealthy, fixes each repair. only limits the run
// to the named repairs. Repairs whose Fix needs -f are detected but skipped
// without intent. Detect errors and Fix errors are recorded, not fatal.
func RunHeal(ctx context.Context, d *HealDeps, only []string) []HealStatus {
	if d.Log == nil {
		d.Log = io.Discard
	}
	want := map[string]bool{}
	for _, n := range only {
		want[n] = true
	}
	var out []HealStatus
	for _, r := range Repairs {
		if len(want) > 0 && !want[r.Name] {
			continue
		}
		s := HealStatus{Name: r.Name, Title: r.Title}
		healthy, detail, err := r.Detect(ctx, d)
		if err != nil {
			s.Error = err.Error()
			out = append(out, s)
			continue
		}
		s.Healthy, s.Detail = healthy, detail
		if healthy {
			out = append(out, s)
			continue
		}
		if r.NeedsIntent && !d.hasIntent() {
			s.Skipped = true
			s.Detail = detail + " -- run bnk heal -f <cluster.yaml> to repair"
			out = append(out, s)
			continue
		}
		fmt.Fprintf(d.Log, "[heal] %s: %s\n", r.Name, detail)
		if d.DryRun {
			s.Detail = "would repair: " + detail
			out = append(out, s)
			continue
		}
		fixed, ferr := r.Fix(ctx, d)
		if ferr != nil {
			s.Error = ferr.Error()
			out = append(out, s)
			continue
		}
		s.Changed, s.Healthy, s.Detail = true, true, fixed
		fmt.Fprintf(d.Log, "[heal] %s: %s\n", r.Name, fixed)
		out = append(out, s)
	}
	return out
}

// DetectAll runs only the Detect side of every repair (doctor).
func DetectAll(ctx context.Context, d *HealDeps) []HealStatus {
	var out []HealStatus
	for _, r := range Repairs {
		s := HealStatus{Name: r.Name, Title: r.Title}
		healthy, detail, err := r.Detect(ctx, d)
		if err != nil {
			s.Error = err.Error()
		} else {
			s.Healthy, s.Detail = healthy, detail
			if !healthy {
				s.Detail += " -- `gkebnkctl bnk heal -f <cluster.yaml>` repairs it"
			}
		}
		out = append(out, s)
	}
	return out
}

// --- pod-manager ---------------------------------------------------------

func detectPodManager(ctx context.Context, clients *Clients) (bool, string, error) {
	deploy, err := clients.K8s.AppsV1().Deployments(InstanceNamespace).Get(ctx, h4DeploymentName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return true, "deployment " + InstanceNamespace + "/" + h4DeploymentName + " absent (BNK not installed yet)", nil
	}
	if err != nil {
		return false, "", err
	}
	mounted := false
	present := false
	for _, c := range deploy.Spec.Template.Spec.Containers {
		if c.Name != h4ContainerName {
			continue
		}
		present = true
		for _, vm := range c.VolumeMounts {
			if vm.MountPath == "/tls/f5ingress/grpc/clt" {
				mounted = true
			}
		}
	}
	if !present {
		return true, "no " + h4ContainerName + " container in " + h4DeploymentName, nil
	}
	if !mounted {
		return false, h4ContainerName + " lacks the /tls/f5ingress/grpc/clt mount (gRPC client cert)", nil
	}
	pods, err := clients.K8s.CoreV1().Pods(InstanceNamespace).List(ctx, metav1.ListOptions{LabelSelector: metav1.FormatLabelSelector(deploy.Spec.Selector)})
	if err != nil {
		return false, "", err
	}
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.DeletionTimestamp != nil {
			continue
		}
		found, ready, restarts, reason := podManagerStatus(p)
		if !found {
			continue
		}
		if !ready && (reason == "CrashLoopBackOff" || restarts >= h4RestartThreshold) {
			return false, fmt.Sprintf("%s in pod %s not Ready (restarts=%d%s): cold-start race against kube-proxy", h4ContainerName, p.Name, restarts, reasonSuffix(reason)), nil
		}
	}
	return true, h4ContainerName + " Ready with its gRPC client cert mounted", nil
}

func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return ", " + reason
}

func fixPodManager(ctx context.Context, clients *Clients, log io.Writer) (string, error) {
	ensurePodManagerGrpcCertMount(ctx, clients)
	if err := restartDeployment(ctx, clients, InstanceNamespace, h4DeploymentName); err != nil {
		return "", err
	}
	fmt.Fprintf(log, "[heal] pod-manager: rollout-restarted %s/%s\n", InstanceNamespace, h4DeploymentName)
	deadline := time.Now().Add(healWait)
	for {
		healthy, detail, err := detectPodManager(ctx, clients)
		if err == nil && healthy {
			return "gRPC client cert mount ensured; " + h4DeploymentName + " restarted; " + detail, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("after %s: %s", healWait, detail)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(healPoll):
		}
	}
}

// --- cwc -----------------------------------------------------------------

func cwcPod(ctx context.Context, clients *Clients) (*corev1.Pod, bool, int32, error) {
	pods, err := clients.K8s.CoreV1().Pods(cwcNamespace).List(ctx, metav1.ListOptions{LabelSelector: cwcLabelSelector})
	if err != nil {
		return nil, false, 0, err
	}
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.DeletionTimestamp != nil {
			continue
		}
		ready, restarts := cwcStatus(p)
		return p, ready, restarts, nil
	}
	return nil, false, 0, nil
}

func cwcStatus(pod *corev1.Pod) (ready bool, restartCount int32) {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == cwcContainerName {
			return cs.Ready, cs.RestartCount
		}
	}
	return false, 0
}

func waitPodsReady(ctx context.Context, clients *Clients, ns, selector string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, healPoll, timeout, true, func(ctx context.Context) (bool, error) {
		pods, err := clients.K8s.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil || len(pods.Items) == 0 {
			return false, nil
		}
		for i := range pods.Items {
			if pods.Items[i].DeletionTimestamp != nil || !isPodReady(&pods.Items[i]) {
				return false, nil
			}
		}
		return true, nil
	})
}

// --- dssm ----------------------------------------------------------------

func dssmOverlayKeys(cm *corev1.ConfigMap) (toPatch []string, done int) {
	for k, v := range cm.Data {
		if !strings.Contains(v, dssmTLSReplace) {
			continue
		}
		if strings.Contains(v, dssmInsecureMarker) {
			done++
			continue
		}
		toPatch = append(toPatch, k)
	}
	return toPatch, done
}

func applyDSSMOverlay(ctx context.Context, clients *Clients, log io.Writer) ([]string, error) {
	var cm *corev1.ConfigMap
	err := wait.PollUntilContextTimeout(ctx, healPoll, dssmCMWaitHeal, true, func(ctx context.Context) (bool, error) {
		got, err := clients.K8s.CoreV1().ConfigMaps(InstanceNamespace).Get(ctx, dssmConfigMapName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		cm = got
		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("ConfigMap %s/%s: %w", InstanceNamespace, dssmConfigMapName, err)
	}
	toPatch, _ := dssmOverlayKeys(cm)
	if len(toPatch) == 0 {
		return nil, nil
	}
	for _, k := range toPatch {
		cm.Data[k] = strings.ReplaceAll(cm.Data[k], dssmTLSReplace, dssmTLSInsecureReplace)
	}
	if _, err := clients.K8s.CoreV1().ConfigMaps(InstanceNamespace).Update(ctx, cm, metav1.UpdateOptions{}); err != nil {
		return nil, fmt.Errorf("update ConfigMap %s/%s: %w", InstanceNamespace, dssmConfigMapName, err)
	}
	fmt.Fprintf(log, "[heal] dssm: patched %s (redis-cli --tls --insecure)\n", strings.Join(toPatch, ", "))
	if err := clients.K8s.CoreV1().Pods(InstanceNamespace).DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: dssmLabelSelector}); err != nil {
		fmt.Fprintf(log, "[heal] dssm: warning: bounce dssm pods: %v\n", err)
	}
	return toPatch, nil
}

func allDSSMPodsReady(ctx context.Context, clients *Clients) bool {
	pods, err := clients.K8s.CoreV1().Pods(InstanceNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: dssmReadinessSelector,
	})
	if err != nil || len(pods.Items) == 0 {
		return false
	}
	for i := range pods.Items {
		if !isPodReady(&pods.Items[i]) {
			return false
		}
	}
	return true
}

// --- controller RBAC -----------------------------------------------------

func detectControllerRBAC(ctx context.Context, clients *Clients) (bool, string, error) {
	deploy, err := clients.K8s.AppsV1().Deployments(InstanceNamespace).Get(ctx, h4DeploymentName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return true, "deployment " + InstanceNamespace + "/" + h4DeploymentName + " absent (BNK not installed yet)", nil
	}
	if err != nil {
		return false, "", err
	}
	sa := deploy.Spec.Template.Spec.ServiceAccountName
	if sa != render.CNEControllerServiceAccount {
		return true, "controller runs as " + sa + " (2.3 RBAC covers EndpointSlices)", nil
	}
	crbs, err := clients.K8s.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, "", err
	}
	for i := range crbs.Items {
		crb := &crbs.Items[i]
		bound := false
		for _, s := range crb.Subjects {
			if s.Kind == "ServiceAccount" && s.Namespace == InstanceNamespace && s.Name == sa {
				bound = true
			}
		}
		if !bound {
			continue
		}
		cr, err := clients.K8s.RbacV1().ClusterRoles().Get(ctx, crb.RoleRef.Name, metav1.GetOptions{})
		if err != nil {
			continue
		}
		for _, rule := range cr.Rules {
			if containsAny(rule.Resources, "endpointslices", "*") && containsAny(rule.Verbs, "get", "*") {
				return true, "ClusterRole " + cr.Name + " grants EndpointSlice get to " + InstanceNamespace + "/" + sa, nil
			}
		}
	}
	return false, "no ClusterRole bound to " + InstanceNamespace + "/" + sa + " grants EndpointSlice get (FLO 2.30 omits it): the controller cannot resolve pool members", nil
}

func containsAny(list []string, want ...string) bool {
	for _, l := range list {
		for _, w := range want {
			if l == w {
				return true
			}
		}
	}
	return false
}

// --- TMM_K8S_ROUTES ------------------------------------------------------

func findCNEInstanceHeal(ctx context.Context, clients *Clients) (*unstructured.Unstructured, error) {
	if clients.Dynamic == nil {
		return nil, fmt.Errorf("no dynamic client")
	}
	list, err := clients.Dynamic.Resource(healCNEInstanceGVR).Namespace(InstanceNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(list.Items) == 0 {
		return nil, nil
	}
	return &list.Items[0], nil
}

func tmmEnvValues(cne *unstructured.Unstructured, name string) []string {
	var vals []string
	env, _, _ := unstructured.NestedSlice(cne.Object, "spec", "advanced", "tmm", "env")
	for _, e := range env {
		m, ok := e.(map[string]any)
		if ok && m["name"] == name {
			if v, ok := m["value"].(string); ok {
				vals = append(vals, v)
			}
		}
	}
	return vals
}

func tmmEnvValue(cne *unstructured.Unstructured, name string) (string, bool) {
	vals := tmmEnvValues(cne, name)
	if len(vals) > 0 {
		return vals[0], true
	}
	return "", false
}

func detectTMMK8sRoutes(ctx context.Context, d *HealDeps) (bool, string, error) {
	if d == nil || d.Clients == nil {
		return false, "", fmt.Errorf("no clients")
	}
	cne, err := findCNEInstanceHeal(ctx, d.Clients)
	if err != nil {
		return false, "", err
	}
	if cne == nil {
		return true, "no CNEInstance (BNK not installed yet)", nil
	}

	expectedCIDR := "10.96.0.0/16"
	if d.Cluster != nil && d.Cluster.Network.Main.SvcsCIDR != "" {
		expectedCIDR = d.Cluster.Network.Main.SvcsCIDR
	} else if d.State != nil {
		if svcCIDR := d.State.Get(ServiceCIDRStateKey); svcCIDR != "" {
			expectedCIDR = svcCIDR
		}
	}

	vals := tmmEnvValues(cne, render.TMMK8sRoutesEnv)
	if len(vals) > 1 {
		return false, "CNEInstance " + cne.GetName() + " has duplicate " + render.TMMK8sRoutesEnv + " entries in tmm.env", nil
	}
	if len(vals) == 1 {
		v := vals[0]
		if v != "" {
			return true, render.TMMK8sRoutesEnv + "=" + v + " on CNEInstance " + cne.GetName(), nil
		}
	}

	mv, _, _ := unstructured.NestedString(cne.Object, "spec", "manifestVersion")
	if strings.HasPrefix(mv, "2.3") {
		if evidence := tmmSidecarServiceNetworkBroken(ctx, d.Clients); evidence != "" {
			return false, "CNEInstance " + cne.GetName() + " (manifestVersion " + mv + ") has no " + render.TMMK8sRoutesEnv + " and the TMM pod cannot reach the service network: " + evidence, nil
		}
		return true, "manifestVersion " + mv + ": no " + render.TMMK8sRoutesEnv + " and the TMM sidecars reach the service network", nil
	}
	return false, "CNEInstance " + cne.GetName() + " has no " + render.TMMK8sRoutesEnv + " (expected " + expectedCIDR + "): once the Infra default route is programmed TMM loses dSSM and DNS and iRule requests reset", nil
}

func fixTMMK8sRoutes(ctx context.Context, d *HealDeps) (string, error) {
	cne, err := findCNEInstanceHeal(ctx, d.Clients)
	if err != nil {
		return "", err
	}
	if cne == nil {
		return "", fmt.Errorf("no CNEInstance in %s", InstanceNamespace)
	}
	cidr := ""
	if d.Cluster != nil && d.Cluster.Network.Main.SvcsCIDR != "" {
		cidr = d.Cluster.Network.Main.SvcsCIDR
	} else if d.State != nil {
		cidr = d.State.Get(ServiceCIDRStateKey)
	}
	if cidr == "" {
		cidr = "10.96.0.0/16"
		if d.State != nil {
			d.State.Set(ServiceCIDRStateKey, cidr)
			_ = d.State.Save()
		}
	}

	env, _, _ := unstructured.NestedSlice(cne.Object, "spec", "advanced", "tmm", "env")
	found := false
	var newEnv []any
	for _, e := range env {
		m, ok := e.(map[string]any)
		if ok && m["name"] == render.TMMK8sRoutesEnv {
			if !found {
				newEnv = append(newEnv, map[string]any{"name": render.TMMK8sRoutesEnv, "value": cidr})
				found = true
			}
		} else {
			newEnv = append(newEnv, e)
		}
	}
	if !found {
		newEnv = append(newEnv, map[string]any{"name": render.TMMK8sRoutesEnv, "value": cidr})
	}

	patch := fmt.Sprintf(`{"spec":{"advanced":{"tmm":{"env":%s}}}}`, mustJSON(newEnv))
	if _, err := d.Clients.Dynamic.Resource(healCNEInstanceGVR).Namespace(InstanceNamespace).Patch(ctx, cne.GetName(), types.MergePatchType, []byte(patch), metav1.PatchOptions{FieldManager: "gkebnkctl-heal"}); err != nil {
		return "", fmt.Errorf("patch CNEInstance %s: %w", cne.GetName(), err)
	}
	return render.TMMK8sRoutesEnv + "=" + cidr + " set on CNEInstance " + cne.GetName() + "; FLO rolls TMM", nil
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

const tmmSidecarLogTail int64 = 400

func tmmSidecarServiceNetworkBroken(ctx context.Context, clients *Clients) string {
	pods, err := clients.K8s.CoreV1().Pods(InstanceNamespace).List(ctx, metav1.ListOptions{LabelSelector: "app=f5-tmm"})
	if err != nil || len(pods.Items) == 0 {
		return ""
	}
	tail := tmmSidecarLogTail
	stream, err := clients.K8s.CoreV1().Pods(InstanceNamespace).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{Container: "f5-fluentbit", TailLines: &tail}).Stream(ctx)
	if err != nil {
		return ""
	}
	defer stream.Close()
	b, err := io.ReadAll(io.LimitReader(stream, 1<<20))
	if err != nil {
		return ""
	}
	return sidecarServiceNetworkEvidence(string(b))
}

func sidecarServiceNetworkEvidence(log string) string {
	for _, line := range strings.Split(log, "\n") {
		switch {
		case strings.Contains(line, "Timeout while contacting DNS servers"),
			strings.Contains(line, "no upstream connections available"),
			strings.Contains(line, "getaddrinfo(host=") && strings.Contains(line, "err="):
			if i := strings.Index(line, "["); i >= 0 {
				line = line[i:]
			}
			if len(line) > 200 {
				line = line[:200]
			}
			return "f5-fluentbit: " + strings.TrimSpace(line)
		}
	}
	return ""
}

// --- EPP namespaces ------------------------------------------------------

func detectEPPNamespaces(ctx context.Context, clients *Clients) (bool, string, error) {
	cne, err := findCNEInstanceHeal(ctx, clients)
	if err != nil {
		return false, "", err
	}
	if cne == nil {
		return true, "no CNEInstance (BNK not installed yet)", nil
	}
	if mv, _, _ := unstructured.NestedString(cne.Object, "spec", "manifestVersion"); strings.HasPrefix(mv, "2.3") {
		return true, "manifestVersion " + mv + " has no F5 Endpoint Picker", nil
	}
	if ns, _, _ := unstructured.NestedStringSlice(cne.Object, "spec", "eppNamespaces"); len(ns) > 0 {
		return true, "eppNamespaces " + strings.Join(ns, ",") + " on CNEInstance " + cne.GetName(), nil
	}
	return false, "CNEInstance " + cne.GetName() + " has no eppNamespaces: the controller reconciles no F5EPP", nil
}

func fixEPPNamespaces(ctx context.Context, clients *Clients) (string, error) {
	cne, err := findCNEInstanceHeal(ctx, clients)
	if err != nil || cne == nil {
		return "", err
	}
	patch := `{"spec":{"eppNamespaces":["All"]}}`
	if _, err := clients.Dynamic.Resource(healCNEInstanceGVR).Namespace(InstanceNamespace).Patch(ctx, cne.GetName(), types.MergePatchType, []byte(patch), metav1.PatchOptions{FieldManager: "gkebnkctl-heal"}); err != nil {
		return "", fmt.Errorf("patch CNEInstance %s: %w", cne.GetName(), err)
	}
	return "eppNamespaces [All] set on CNEInstance " + cne.GetName(), nil
}

// --- helpers -------------------------------------------------------------

func applyRawYAML(ctx context.Context, clients *Clients, rawYAML []byte) error {
	objs, err := k8s.ParseYAMLStream(rawYAML)
	if err != nil {
		return fmt.Errorf("parse YAML: %w", err)
	}
	for _, obj := range objs {
		switch obj.GetKind() {
		case "ClusterRole":
			var cr rbacv1.ClusterRole
			b, err := json.Marshal(obj.Object)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(b, &cr); err != nil {
				return err
			}
			if _, err := clients.K8s.RbacV1().ClusterRoles().Create(ctx, &cr, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
				if _, err := clients.K8s.RbacV1().ClusterRoles().Update(ctx, &cr, metav1.UpdateOptions{}); err != nil {
					return fmt.Errorf("apply ClusterRole %s: %w", cr.Name, err)
				}
			}
		case "ClusterRoleBinding":
			var crb rbacv1.ClusterRoleBinding
			b, err := json.Marshal(obj.Object)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(b, &crb); err != nil {
				return err
			}
			if _, err := clients.K8s.RbacV1().ClusterRoleBindings().Create(ctx, &crb, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
				if _, err := clients.K8s.RbacV1().ClusterRoleBindings().Update(ctx, &crb, metav1.UpdateOptions{}); err != nil {
					return fmt.Errorf("apply ClusterRoleBinding %s: %w", crb.Name, err)
				}
			}
		}
	}
	return nil
}

func restartDeployment(ctx context.Context, clients *Clients, ns, name string) error {
	patch := fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"gkebnkctl.io/restartedAt":%q}}}}}`,
		time.Now().UTC().Format(time.RFC3339))
	if _, err := clients.K8s.AppsV1().Deployments(ns).Patch(ctx, name, types.StrategicMergePatchType,
		[]byte(patch), metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("restart deploy %s/%s: %w", ns, name, err)
	}
	return nil
}

func ensurePodManagerGrpcCertMount(ctx context.Context, clients *Clients) {
	if clients == nil || clients.K8s == nil {
		return
	}
	sec, err := clients.K8s.CoreV1().Secrets(InstanceNamespace).Get(ctx, "tls-f5ingress-grpc-clt-secret", metav1.GetOptions{})
	if err == nil && sec.Data != nil {
		updated := false
		if _, ok := sec.Data["f5ingress-grpc-clt.pem"]; !ok {
			if crt, hasCrt := sec.Data["tls.crt"]; hasCrt {
				sec.Data["f5ingress-grpc-clt.pem"] = crt
				updated = true
			}
		}
		if _, ok := sec.Data["f5ingress-grpc-clt-key.pem"]; !ok {
			if key, hasKey := sec.Data["tls.key"]; hasKey {
				sec.Data["f5ingress-grpc-clt-key.pem"] = key
				updated = true
			}
		}
		if updated {
			if _, err := clients.K8s.CoreV1().Secrets(InstanceNamespace).Update(ctx, sec, metav1.UpdateOptions{}); err == nil {
				fmt.Fprintln(os.Stderr, "[heal] synced f5ingress-grpc-clt pem keys to secret")
			}
		}
	}

	deploy, err := clients.K8s.AppsV1().Deployments(InstanceNamespace).Get(ctx, h4DeploymentName, metav1.GetOptions{})
	if err == nil {
		hasMount := false
		podMgrIdx := -1
		for i, c := range deploy.Spec.Template.Spec.Containers {
			if c.Name == h4ContainerName {
				podMgrIdx = i
				for _, vm := range c.VolumeMounts {
					if vm.MountPath == "/tls/f5ingress/grpc/clt" {
						hasMount = true
						break
					}
				}
				break
			}
		}
		if podMgrIdx >= 0 && !hasMount {
			deploy.Spec.Template.Spec.Containers[podMgrIdx].VolumeMounts = append(
				deploy.Spec.Template.Spec.Containers[podMgrIdx].VolumeMounts,
				corev1.VolumeMount{
					Name:      "tls-f5ingress-grpc-clt-volume",
					MountPath: "/tls/f5ingress/grpc/clt",
					ReadOnly:  true,
				},
			)
			if _, err := clients.K8s.AppsV1().Deployments(InstanceNamespace).Update(ctx, deploy, metav1.UpdateOptions{}); err == nil {
				fmt.Fprintln(os.Stderr, "[heal] added /tls/f5ingress/grpc/clt volumeMount to f5-tmm-pod-manager")
			}
		}
	}
}

func podManagerStatus(pod *corev1.Pod) (found bool, ready bool, restartCount int32, waitingReason string) {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == h4ContainerName {
			reason := ""
			if cs.State.Waiting != nil {
				reason = cs.State.Waiting.Reason
			}
			return true, cs.Ready, cs.RestartCount, reason
		}
	}
	return false, false, 0, ""
}

func isPodReady(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func ensureTestNamespace(ctx context.Context, cs kubernetes.Interface) (bool, error) {
	_, err := cs.CoreV1().Namespaces().Get(ctx, K8sTestNamespace, metav1.GetOptions{})
	if err == nil {
		return false, nil
	}
	if !apierrors.IsNotFound(err) {
		return false, err
	}
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: K8sTestNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "gkebnkctl",
			},
		},
	}
	_, err = cs.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return false, err
	}
	return true, nil
}
