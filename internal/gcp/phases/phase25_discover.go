package phases

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	computepb "cloud.google.com/go/compute/apiv1/computepb"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/netcalc"
)

const (
	discoverNamespace   = "kube-system"
	discoverPodName     = "gkebnkctl-discover"
	discoverPodTimeout  = 3 * time.Minute
	discoverPodPollFreq = 3 * time.Second
)

// DiscoverStateKeys are every state.env key Phase25Discover writes (or, in
// dry-run, sets to a placeholder) — the single source of truth so this phase
// and any other package that needs the full key set (lifecycle.go's down
// teardown, clearing discovered.* state per docs/SPEC-go-tracer.md §3) can
// never drift into two independently-maintained copies of the same literal.
var DiscoverStateKeys = []string{
	"TMM_NODE_NAME", "AGENT_NODE_NAME",
	"TMM_EXT_IFACE", "TMM_EXT_PCI", "TMM_INT_IFACE", "TMM_INT_PCI",
	"TMM_EXT_ALIAS_CIDR", "TMM_INT_ALIAS_CIDR", "TMM_EXT_SELFIP", "TMM_INT_SELFIP",
	"TMM_POD_CIDR", "TMM_POD_GW", "AGENT_EXT_IFACE",
}

// ifaceDumpScript is run inside the discovery pod's host network/PID
// namespace (via nsenter -t1) and emits one "IFACE=x MAC=y PCI=z" line per
// non-loopback interface.
//
// Derived from reference/bash-seed/bin/25-discover.sh's IFACE_DUMP_SCRIPT, with
// one fix the bash seed lacks: a bare `basename $(readlink -f .../device)` is
// WRONG for virtio-net NICs. Their device symlink points at the virtio node,
// not the PCI function —
//
//	/sys/class/net/eth1/device -> /sys/devices/pci0000:00/0000:00:05.0/virtio2
//
// so basename yields "virtio2" instead of the PCI address "0000:00:05.0". That
// bogus value flows into CNEInstance advanced.tmm.env PCIDEVICE_INTEL_COM_*,
// mapres cannot resolve it, so TMM never maps net1/net2, its tmm_init.tcl gets
// `route ... vlan (null)`, and TMM crashes with `tcl_init_proxy failed`
// (live-hit 2026-07-22, cluster bnk-scratch4 — GCE virtio nodes; bnk-hostdev's
// NICs bind PCI directly so the seed's basename happened to work there).
//
// Fix: if the device leaf is already a PCI BDF (has the "0000:00:05.0" shape)
// keep it; otherwise it is a virtio/child node, so take its parent directory,
// which IS the PCI function (verified live: parent of virtio3 = 0000:00:06.0,
// matching bnk-hostdev's discovered value).
const ifaceDumpScript = `for i in /sys/class/net/*; do
  n=$(basename "$i")
  [ "$n" = "lo" ] && continue
  mac=$(cat "$i/address" 2>/dev/null || echo "")
  pci=""
  if [ -e "$i/device" ]; then
    dev=$(readlink -f "$i/device")
    leaf=$(basename "$dev")
    case "$leaf" in
      *:*:*.*) pci=$leaf ;;
      *) pci=$(basename "$(dirname "$dev")") ;;
    esac
  fi
  echo "IFACE=$n MAC=$mac PCI=$pci"
done
`

// Phase25Discover discovers, per docs/SPEC-go-tracer.md §3/§9.6:
//   - tmm-pool node: external + internal NIC IP (compute Instances.Get,
//     matched by Network self-link suffix) -> derived MAC -> Linux
//     iface/PCI (ephemeral privileged pod, MAC-matched) -> GKE-allocated
//     alias /28 slice -> self-IP (Nth(cidr, 2)) -> pod-CIDR gateway.
//   - agent-pool node: external NIC iface only (for the egress route-setter).
//
// Read-only against the cloud/cluster (the ephemeral discovery pod is
// deleted immediately after use) and safely re-runnable.
func Phase25Discover(ctx context.Context, cl *intent.Cluster, st *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 25] discover: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 25] would discover tmm/agent NIC iface/MAC/PCI + alias /28 slices (needs live cluster)")
		for _, k := range DiscoverStateKeys {
			st.Set(k, "dry-run-"+strings.ToLower(k))
		}
		return nil
	}

	if clients.K8s == nil {
		return fmt.Errorf("phase25: Clients.K8s is nil — Phase20 must attach the k8s client first")
	}

	tmmNode, err := firstNodeInPool(ctx, clients.K8s, cl.ClusterSpec.TmmPool.Name)
	if err != nil {
		return fmt.Errorf("phase25: %w", err)
	}
	agentNode, err := firstNodeInPool(ctx, clients.K8s, cl.ClusterSpec.AgentPool.Name)
	if err != nil {
		return fmt.Errorf("phase25: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[phase 25] tmm node: %s, agent node: %s\n", tmmNode, agentNode)

	tmmInst, err := clients.Instances.Get(ctx, &computepb.GetInstanceRequest{
		Project: cl.Metadata.Project, Zone: cl.Metadata.Zone, Instance: tmmNode,
	})
	if err != nil {
		return fmt.Errorf("phase25: describing tmm node %s: %w", tmmNode, err)
	}
	extNIC, err := nicForVPC(tmmInst, cl.Network.External.VPC)
	if err != nil {
		return fmt.Errorf("phase25: tmm node external NIC: %w", err)
	}
	intNIC, err := nicForVPC(tmmInst, cl.Network.Internal.VPC)
	if err != nil {
		return fmt.Errorf("phase25: tmm node internal NIC: %w", err)
	}
	extIP := extNIC.GetNetworkIP()
	intIP := intNIC.GetNetworkIP()
	extMAC, err := netcalc.MACForIP(extIP)
	if err != nil {
		return fmt.Errorf("phase25: %w", err)
	}
	intMAC, err := netcalc.MACForIP(intIP)
	if err != nil {
		return fmt.Errorf("phase25: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[phase 25] discovering NIC names/PCI addresses on %s (node debug pod pattern)\n", tmmNode)
	dump, err := discoverNodeIfaces(ctx, clients.K8s, tmmNode)
	if err != nil {
		return fmt.Errorf("phase25: %w", err)
	}
	extIface := fieldForMAC(dump, extMAC, "IFACE")
	extPCI := fieldForMAC(dump, extMAC, "PCI")
	intIface := fieldForMAC(dump, intMAC, "IFACE")
	intPCI := fieldForMAC(dump, intMAC, "PCI")
	if extIface == "" || intIface == "" {
		// If TMM is already running, Multus host-device CNI has moved eth1/eth2 out
		// of the root host network namespace into the TMM pod. If we already
		// discovered them on this node in an earlier run, preserve them.
		if st.Get("TMM_NODE_NAME") == tmmNode && st.Get("TMM_EXT_IFACE") != "" && st.Get("TMM_INT_IFACE") != "" {
			fmt.Fprintf(os.Stderr, "[phase 25] note: NICs already bound into TMM pod on %s; reusing previously discovered ifaces/PCIs\n", tmmNode)
			extIface = st.Get("TMM_EXT_IFACE")
			extPCI = st.Get("TMM_EXT_PCI")
			intIface = st.Get("TMM_INT_IFACE")
			intPCI = st.Get("TMM_INT_PCI")
		} else {
			return fmt.Errorf("phase25: MAC match failed on tmm node %s — expected ext MAC=%s (ip=%s), int MAC=%s (ip=%s); raw dump:\n%s",
				tmmNode, extMAC, extIP, intMAC, intIP, dump)
		}
	}
	fmt.Fprintf(os.Stderr, "[phase 25] tmm ext: iface=%s pci=%s  |  tmm int: iface=%s pci=%s\n", extIface, extPCI, intIface, intPCI)

	extAliasCIDR, err := aliasCIDRForRange(extNIC, cl.Network.External.PodsRangeName)
	if err != nil {
		return fmt.Errorf("phase25: %w", err)
	}
	intAliasCIDR, err := aliasCIDRForRange(intNIC, cl.Network.Internal.PodsRangeName)
	if err != nil {
		return fmt.Errorf("phase25: %w", err)
	}
	extSelfIP, err := netcalc.Nth(extAliasCIDR, 2)
	if err != nil {
		return fmt.Errorf("phase25: %w", err)
	}
	intSelfIP, err := netcalc.Nth(intAliasCIDR, 2)
	if err != nil {
		return fmt.Errorf("phase25: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[phase 25] tmm ext alias slice: %s -> self-IP %s\n", extAliasCIDR, extSelfIP)
	fmt.Fprintf(os.Stderr, "[phase 25] tmm int alias slice: %s -> self-IP %s\n", intAliasCIDR, intSelfIP)

	tmmNodeObj, err := clients.K8s.CoreV1().Nodes().Get(ctx, tmmNode, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("phase25: getting tmm node %s: %w", tmmNode, err)
	}
	podCIDR := tmmNodeObj.Spec.PodCIDR
	podGW, err := netcalc.Nth(podCIDR, 1)
	if err != nil {
		return fmt.Errorf("phase25: tmm pod CIDR %q: %w", podCIDR, err)
	}
	fmt.Fprintf(os.Stderr, "[phase 25] tmm pod CIDR: %s -> gateway %s\n", podCIDR, podGW)

	agentInst, err := clients.Instances.Get(ctx, &computepb.GetInstanceRequest{
		Project: cl.Metadata.Project, Zone: cl.Metadata.Zone, Instance: agentNode,
	})
	if err != nil {
		return fmt.Errorf("phase25: describing agent node %s: %w", agentNode, err)
	}
	agentExtNIC, err := nicForVPC(agentInst, cl.Network.External.VPC)
	if err != nil {
		return fmt.Errorf("phase25: agent node external NIC: %w", err)
	}
	agentExtMAC, err := netcalc.MACForIP(agentExtNIC.GetNetworkIP())
	if err != nil {
		return fmt.Errorf("phase25: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[phase 25] discovering NIC names on %s\n", agentNode)
	agentDump, err := discoverNodeIfaces(ctx, clients.K8s, agentNode)
	if err != nil {
		return fmt.Errorf("phase25: %w", err)
	}
	agentExtIface := fieldForMAC(agentDump, agentExtMAC, "IFACE")
	if agentExtIface == "" {
		if st.Get("AGENT_NODE_NAME") == agentNode && st.Get("AGENT_EXT_IFACE") != "" {
			fmt.Fprintf(os.Stderr, "[phase 25] note: agent NIC on %s already discovered; reusing %s\n", agentNode, st.Get("AGENT_EXT_IFACE"))
			agentExtIface = st.Get("AGENT_EXT_IFACE")
		} else {
			return fmt.Errorf("phase25: MAC match failed on agent node %s — expected MAC=%s; raw dump:\n%s", agentNode, agentExtMAC, agentDump)
		}
	}
	fmt.Fprintf(os.Stderr, "[phase 25] agent ext: iface=%s\n", agentExtIface)

	st.Set("TMM_NODE_NAME", tmmNode)
	st.Set("AGENT_NODE_NAME", agentNode)
	st.Set("TMM_EXT_IFACE", extIface)
	st.Set("TMM_EXT_PCI", extPCI)
	st.Set("TMM_INT_IFACE", intIface)
	st.Set("TMM_INT_PCI", intPCI)
	st.Set("TMM_EXT_ALIAS_CIDR", extAliasCIDR)
	st.Set("TMM_INT_ALIAS_CIDR", intAliasCIDR)
	st.Set("TMM_EXT_SELFIP", extSelfIP)
	st.Set("TMM_INT_SELFIP", intSelfIP)
	st.Set("TMM_POD_CIDR", podCIDR)
	st.Set("TMM_POD_GW", podGW)
	st.Set("AGENT_EXT_IFACE", agentExtIface)
	st.Set("AGENT_EXT_IP", agentExtNIC.GetNetworkIP())
	st.Set("JUMPHOST_INSTANCE_NAME", agentNode)
	st.Set("JUMPHOST_INSTANCE_ID", agentNode)
	st.Set("JUMPHOST_EXT_IP", agentExtNIC.GetNetworkIP())
	st.Set("JUMPHOST_BNK_EXT_ENI_IP", agentExtNIC.GetNetworkIP())

	if err := st.Save(); err != nil {
		return fmt.Errorf("phase25: saving state: %w", err)
	}
	fmt.Fprintln(os.Stderr, "[phase 25] discover OK")
	return nil
}

// firstNodeInPool returns the name of the first Node with the
// cloud.google.com/gke-nodepool=poolName label.
func firstNodeInPool(ctx context.Context, k8sClient kubernetes.Interface, poolName string) (string, error) {
	nodes, err := k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: gkeNodepoolLabel + "=" + poolName,
	})
	if err != nil {
		return "", fmt.Errorf("listing nodes for pool %s: %w", poolName, err)
	}
	if len(nodes.Items) == 0 {
		return "", fmt.Errorf("no nodes found for pool %s — run phase 20 first", poolName)
	}
	return nodes.Items[0].Name, nil
}

// nicForVPC returns the NetworkInterface on inst whose Network self-link
// ends in "/networks/<vpc>".
func nicForVPC(inst *computepb.Instance, vpc string) (*computepb.NetworkInterface, error) {
	suffix := "/networks/" + vpc
	for _, nic := range inst.GetNetworkInterfaces() {
		if strings.HasSuffix(nic.GetNetwork(), suffix) {
			return nic, nil
		}
	}
	return nil, fmt.Errorf("no network interface on %s matches vpc %s", inst.GetName(), vpc)
}

// aliasCIDRForRange returns the alias IP CIDR range on nic named rangeName.
func aliasCIDRForRange(nic *computepb.NetworkInterface, rangeName string) (string, error) {
	for _, a := range nic.GetAliasIpRanges() {
		if a.GetSubnetworkRangeName() == rangeName {
			return a.GetIpCidrRange(), nil
		}
	}
	return "", fmt.Errorf("no alias IP range named %s on interface %s", rangeName, nic.GetName())
}

// fieldForMAC extracts the named field ("IFACE" or "PCI") from the dump line
// whose MAC field matches mac (case-insensitive). Mirrors bash
// discover::field_for_mac. Returns "" if no line matches.
func fieldForMAC(dump, mac, field string) string {
	target := strings.ToLower(mac)
	for _, line := range strings.Split(dump, "\n") {
		vals := make(map[string]string)
		for _, tok := range strings.Fields(line) {
			if idx := strings.Index(tok, "="); idx > 0 {
				vals[tok[:idx]] = tok[idx+1:]
			}
		}
		if strings.ToLower(vals["MAC"]) == target {
			return vals[field]
		}
	}
	return ""
}

// buildNsenterPod returns the ephemeral privileged pod spec used to run script
// on nodeName via nsenter -t 1 (host PID/net/mount/IPC namespaces).
func buildNsenterPod(ns, podName, nodeName, script string) *corev1.Pod {
	privileged := true
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: ns,
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
					Name:    "discover",
					Image:   "busybox:1.36",
					Command: []string{"nsenter", "-t", "1", "-m", "-u", "-n", "-i", "--", "sh", "-c", script},
					SecurityContext: &corev1.SecurityContext{
						Privileged: &privileged,
					},
				},
			},
		},
	}
}

// buildDiscoveryPod returns the ephemeral privileged pod spec used to dump
// /sys/class/net on nodeName.
func buildDiscoveryPod(nodeName string) *corev1.Pod {
	return buildNsenterPod(discoverNamespace, discoverPodName, nodeName, ifaceDumpScript)
}

// runHostNsenterScript applies an ephemeral privileged pod to nodeName running
// script via nsenter -t 1, waits for it to Succeeded, reads its stdout logs, and
// deletes the pod (best-effort cleanup).
func runHostNsenterScript(ctx context.Context, k8sClient kubernetes.Interface, ns, podName, nodeName, script string, timeout time.Duration) (string, error) {
	pods := k8sClient.CoreV1().Pods(ns)
	_ = pods.Delete(ctx, podName, metav1.DeleteOptions{})
	_ = gcp.PollUntil(ctx, 500*time.Millisecond, 30*time.Second, func(ctx context.Context) (bool, error) {
		_, err := pods.Get(ctx, podName, metav1.GetOptions{})
		return err != nil, nil
	})

	if _, err := pods.Create(ctx, buildNsenterPod(ns, podName, nodeName, script), metav1.CreateOptions{}); err != nil {
		return "", fmt.Errorf("creating discovery pod %s/%s on %s: %w", ns, podName, nodeName, err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		_ = pods.Delete(cleanupCtx, podName, metav1.DeleteOptions{})
	}()

	if err := waitForPodSucceeded(ctx, k8sClient, ns, podName, timeout); err != nil {
		return "", err
	}
	logs, err := podLogs(ctx, k8sClient, ns, podName)
	if err != nil {
		return "", fmt.Errorf("reading discovery pod %s/%s logs: %w", ns, podName, err)
	}
	return string(logs), nil
}

// discoverNodeIfaces applies the ephemeral discovery pod to nodeName, waits
// for it to Succeed, reads its logs (the IFACE/MAC/PCI dump), and deletes
// it. Delegates to runHostNsenterScript.
func discoverNodeIfaces(ctx context.Context, k8sClient kubernetes.Interface, nodeName string) (string, error) {
	return runHostNsenterScript(ctx, k8sClient, discoverNamespace, discoverPodName, nodeName, ifaceDumpScript, discoverPodTimeout)
}

// waitForPodSucceeded polls ns/name until it reaches PodSucceeded, erroring
// immediately on PodFailed.
func waitForPodSucceeded(ctx context.Context, k8sClient kubernetes.Interface, ns, name string, timeout time.Duration) error {
	return gcp.PollUntil(ctx, discoverPodPollFreq, timeout, func(ctx context.Context) (bool, error) {
		pod, err := k8sClient.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, nil //nolint:nilerr
		}
		switch pod.Status.Phase {
		case corev1.PodSucceeded:
			return true, nil
		case corev1.PodFailed:
			return false, fmt.Errorf("discovery pod %s/%s failed", ns, name)
		}
		return false, nil
	})
}

// podLogs retrieves the stdout logs from ns/name.
func podLogs(ctx context.Context, k8sClient kubernetes.Interface, ns, name string) ([]byte, error) {
	req := k8sClient.CoreV1().Pods(ns).GetLogs(name, &corev1.PodLogOptions{})
	stream, err := req.Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("GetLogs %s/%s: %w", ns, name, err)
	}
	defer stream.Close()
	return io.ReadAll(stream)
}
