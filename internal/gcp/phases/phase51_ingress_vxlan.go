package phases

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/manifests"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/render"
	"github.com/JLCode-tech/gkebnkctl/internal/netcalc"
)

const (
	ingressVxlanRoutesTemplatePath = "templates/ingress-vxlan-routes.yaml.tmpl"

	vxlanDiscoverNamespace  = "kube-system"
	vxlanDiscoverPodName    = "gkebnkctl-vxlan-discover"
	vxlanDiscoverPodTimeout = 2 * time.Minute

	vxlanIfaceName = "vxlan100"
)

// vxlanAddrScript is run inside the ephemeral discovery pod's host
// network/PID namespace (nsenter -t1), mirroring phase25_discover.go's
// ifaceDumpScript pattern. It prints vxlan100's IPv4 CIDR
// ("<addr>/<prefixlen>", e.g. "10.0.64.1/17") if the interface exists and
// has an address, or nothing otherwise. `grep`/`awk`/`head` (not `ip -4/-o`
// flags) are used deliberately for portability across busybox's minimal `ip`
// applet, matching agent-route-setter.yaml.tmpl's own `ip route` usage
// style. Silence (not an error) means the pseudo-CNI has not provisioned
// vxlan100 on this node (no egress-namespace pod scheduled there) —
// discoverIngressVxlanRoutes treats that as "skip this node", not a
// failure.
const vxlanAddrScript = `ip addr show dev ` + vxlanIfaceName + ` 2>/dev/null | grep -o 'inet [0-9.]*/[0-9]*' | awk '{print $2}' | head -n1
`

// Phase51IngressVxlanRoutes closes the asymmetric-routing gap the pseudo-CNI
// egress vxlan (Phase50) introduces: an egress-steered pod's default route
// is vxlan100 -> TMM, so its replies leave over the vxlan, but TMM
// *originates* ingress connections to that pod over the standard Cilium
// route (route-cluster-pods — Phase41's cluster-wide pod-CIDR route) — a
// path mismatch TMM's stateful connection tracking drops (the return never
// arrives over the vxlan TMM expects it on). Fix: one F5SPKStaticRoute per
// agent-pool node, more specific than route-cluster-pods, pointing that
// node's pod CIDR back over the SAME vxlan egress uses (symmetric). Proven
// live on cluster bnk-hostdev 2026-07-21: destination 10.66.4.0/24, gateway
// 10.0.64.1 — VIP ingress -> HTTP 200 AND egress still SNATs correctly on
// the same pod.
//
// VTEP decision — discovered, not derived: cluster.yaml/Phase50 carry no
// vxlan *subnet* configuration at all (only vxlanKey/vxlanMtu/vxlanPort —
// see intent.EgressSpec and templates/egress.yaml.tmpl's pseudoCNIConfig),
// so the pseudo-CNI controller is the sole owner of vxlan100's IPAM; there
// is no cluster.yaml-derivable formula to reproduce it from (a fixed
// TMM-side-.1/node-side-offset scheme was considered and rejected — nothing
// in the F5SPKEgress CR or this codebase pins that offset, so hardcoding it
// would be an unverified guess, and per this task's instructions "a wrong
// VTEP silently breaks ingress"). This phase instead discovers each
// agent-pool node's actual vxlan100 address the same way Phase25 discovers
// NIC iface/PCI info: an ephemeral privileged pod, nsenter'd into the
// node's host netns, reading the live interface state — after Phase50 has
// applied F5SPKEgress and waited for egress-client Ready, so the vxlan has
// had a chance to come up. A node without an egress-namespace pod scheduled
// on it has no vxlan100 at all; that node is skipped (no route emitted)
// rather than guessed, since a wrong/absent gateway on a route this
// specific would black-hole ALL ingress to that node's pods outright —
// worse than the asymmetric-routing bug this phase fixes.
//
// The declared routes are authoritative: any F5SPKStaticRoute already
// occupying one of their destinations under a different name (a hand-applied
// fix, or an older release's naming) is deleted first — BNK's uniqueness
// webhook forbids two routes on the same destination+prefixLen, so coexistence
// is not an option and skipping would leave unmanaged drift. See
// reconcileStaticRouteDrift in staticroute_drift.go.
//
// Needs a live cluster; must run after Phase50 (vxlan100 must exist to be
// discoverable) and after Phase41 (route-cluster-pods must already exist —
// this phase's routes are deliberately more specific overrides of it, not a
// replacement).
func Phase51IngressVxlanRoutes(ctx context.Context, cl *intent.Cluster, _ *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 51] ingress-vxlan-routes: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 51] would discover vxlan100 VTEP on each agent-pool node and apply one F5SPKStaticRoute per node with a live VTEP, replacing any route already occupying those destinations (needs live cluster)")
		return nil
	}

	if clients == nil || clients.K8s == nil {
		return fmt.Errorf("phase51: Clients.K8s is nil — AttachK8s must run first (after Phase20)")
	}

	routes, err := discoverIngressVxlanRoutes(ctx, clients.K8s, cl.ClusterSpec.AgentPool.Name)
	if err != nil {
		return fmt.Errorf("phase51: %w", err)
	}
	if len(routes) == 0 {
		fmt.Fprintln(os.Stderr, "[phase 51] no agent-pool node has a live vxlan100 VTEP yet — nothing to apply (expected if no egress-namespace pod is scheduled)")
		return nil
	}

	tmplBytes, err := manifests.FS.ReadFile(ingressVxlanRoutesTemplatePath)
	if err != nil {
		return fmt.Errorf("phase51: reading embedded ingress-vxlan-routes template: %w", err)
	}
	rendered, err := render.RenderIngressVxlanRoutes(tmplBytes, render.IngressVxlanRoutesVars{Routes: routes})
	if err != nil {
		return fmt.Errorf("phase51: rendering ingress-vxlan-routes: %w", err)
	}
	if err := applyStaticRouteManifestBytes(ctx, clients, rendered, false); err != nil {
		return fmt.Errorf("phase51: applying F5SPKStaticRoutes: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[phase 51] ingress-vxlan-routes OK (%d route(s))\n", len(routes))
	return nil
}

// discoverIngressVxlanRoutes lists every node in the agent pool and, for
// each, discovers its vxlan100 VTEP address + pod CIDR, returning one
// render.IngressVxlanRouteVars per node that has a live VTEP (nodes without
// one are logged and skipped — see Phase51IngressVxlanRoutes's doc
// comment). Route names are derived from the node's pod-CIDR network
// address so they are deterministic and unique per node without depending
// on node naming.
func discoverIngressVxlanRoutes(ctx context.Context, k8sClient kubernetes.Interface, agentPoolName string) ([]render.IngressVxlanRouteVars, error) {
	nodes, err := k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: gkeNodepoolLabel + "=" + agentPoolName,
	})
	if err != nil {
		return nil, fmt.Errorf("listing nodes for pool %s: %w", agentPoolName, err)
	}
	if len(nodes.Items) == 0 {
		return nil, fmt.Errorf("no nodes found for pool %s — run phase 20 first", agentPoolName)
	}

	routes := make([]render.IngressVxlanRouteVars, 0, len(nodes.Items))
	for _, node := range nodes.Items {
		if node.Spec.PodCIDR == "" {
			return nil, fmt.Errorf("node %s has no Spec.PodCIDR set", node.Name)
		}

		vtepCIDR, err := discoverVxlanAddr(ctx, k8sClient, node.Name)
		if err != nil {
			return nil, fmt.Errorf("discovering vxlan100 on node %s: %w", node.Name, err)
		}
		if vtepCIDR == "" {
			fmt.Fprintf(os.Stderr, "[phase 51] node %s: no vxlan100 VTEP found — skipping (no egress-namespace pod scheduled there)\n", node.Name)
			continue
		}
		vtepIP, err := hostIPFromCIDR(vtepCIDR)
		if err != nil {
			return nil, fmt.Errorf("node %s: parsing discovered vxlan100 address %q: %w", node.Name, vtepCIDR, err)
		}

		dest, err := netcalc.CIDRAddr(node.Spec.PodCIDR)
		if err != nil {
			return nil, fmt.Errorf("node %s: %w", node.Name, err)
		}
		prefixLen, err := netcalc.CIDRPrefixLen(node.Spec.PodCIDR)
		if err != nil {
			return nil, fmt.Errorf("node %s: %w", node.Name, err)
		}

		fmt.Fprintf(os.Stderr, "[phase 51] node %s: pod CIDR=%s -> vxlan100 VTEP=%s\n", node.Name, node.Spec.PodCIDR, vtepIP)
		routes = append(routes, render.IngressVxlanRouteVars{
			Name:        ingressVxlanRouteName(dest),
			Destination: dest,
			PrefixLen:   prefixLen,
			Gateway:     vtepIP,
		})
	}
	return routes, nil
}

// ingressVxlanRouteName derives a deterministic, unique F5SPKStaticRoute
// name from a pod-CIDR network address, e.g. "10.66.4.0" ->
// "route-ingress-vxlan-10-66-4-0".
func ingressVxlanRouteName(podCIDRAddr string) string {
	return "route-ingress-vxlan-" + strings.ReplaceAll(podCIDRAddr, ".", "-")
}

// hostIPFromCIDR returns just the address portion of a "<ip>/<prefixlen>"
// string (the discovery script's output), validating it parses as an IPv4
// address.
func hostIPFromCIDR(cidr string) (string, error) {
	ip, _, found := strings.Cut(cidr, "/")
	if !found {
		return "", fmt.Errorf("expected <ip>/<prefixlen>, got %q", cidr)
	}
	if parsed := net.ParseIP(ip); parsed == nil || parsed.To4() == nil {
		return "", fmt.Errorf("not an IPv4 address: %q", ip)
	}
	return ip, nil
}

// discoverVxlanAddr applies an ephemeral privileged pod to nodeName
// (mirrors phase25_discover.go's discoverNodeIfaces pattern: nsenter -t1
// into the node's host netns, wait for Succeed, read logs, delete),
// running vxlanAddrScript instead of phase25's iface dump. Reuses
// waitForPodSucceeded/podLogs (phase25_discover.go) — both are already
// generic over namespace/pod name. Returns the raw "<ip>/<prefixlen>"
// line, or "" if vxlan100 does not exist / has no IPv4 address on this
// node (not an error — see Phase51IngressVxlanRoutes's doc comment).
func discoverVxlanAddr(ctx context.Context, k8sClient kubernetes.Interface, nodeName string) (string, error) {
	pods := k8sClient.CoreV1().Pods(vxlanDiscoverNamespace)
	_ = pods.Delete(ctx, vxlanDiscoverPodName, metav1.DeleteOptions{})

	if _, err := pods.Create(ctx, buildVxlanDiscoveryPod(nodeName), metav1.CreateOptions{}); err != nil {
		return "", fmt.Errorf("creating vxlan-discover pod on %s: %w", nodeName, err)
	}
	defer func() {
		_ = pods.Delete(ctx, vxlanDiscoverPodName, metav1.DeleteOptions{})
	}()

	if err := waitForPodSucceeded(ctx, k8sClient, vxlanDiscoverNamespace, vxlanDiscoverPodName, vxlanDiscoverPodTimeout); err != nil {
		return "", err
	}
	logs, err := podLogs(ctx, k8sClient, vxlanDiscoverNamespace, vxlanDiscoverPodName)
	if err != nil {
		return "", fmt.Errorf("reading vxlan-discover pod logs: %w", err)
	}
	return strings.TrimSpace(string(logs)), nil
}

// buildVxlanDiscoveryPod returns the ephemeral privileged pod spec used to
// read vxlan100's IPv4 address on nodeName via nsenter -t1, mirroring
// phase25_discover.go's buildDiscoveryPod shape.
func buildVxlanDiscoveryPod(nodeName string) *corev1.Pod {
	privileged := true
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      vxlanDiscoverPodName,
			Namespace: vxlanDiscoverNamespace,
		},
		Spec: corev1.PodSpec{
			NodeName:      nodeName,
			HostPID:       true,
			RestartPolicy: corev1.RestartPolicyNever,
			Tolerations: []corev1.Toleration{
				{Operator: corev1.TolerationOpExists},
			},
			Containers: []corev1.Container{
				{
					Name:    "vxlan-discover",
					Image:   "busybox:1.36",
					Command: []string{"nsenter", "-t", "1", "-m", "-u", "-n", "-i", "--", "sh", "-c", vxlanAddrScript},
					SecurityContext: &corev1.SecurityContext{
						Privileged: &privileged,
					},
				},
			},
		},
	}
}
