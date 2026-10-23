// Package topology builds and renders the whole-cluster data-path topology
// from a cluster.yaml (intent), the cluster's state.env, and optional live
// Kubernetes enrichment.
//
// Supported render formats:
//   - ASCII box-drawing (envdiagram aesthetic, ~70 cols)
//   - Mermaid graph TD (for docs / forge embedding)
//   - Graphviz DOT (digraph G)
//   - JSON (structured Model serialization)
package topology

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
)

// BNKNamespaces lists the canonical BNK namespaces queried during live K8s enrichment.
var BNKNamespaces = []string{
	"f5-cne-core",
	"f5-cne-system",
	"f5-bnk-instance",
	"f5-utils",
}

// VPCInfo describes one of the three GKE VPC networks (main, external, internal).
type VPCInfo struct {
	Name          string `json:"name"`
	Subnet        string `json:"subnet"`
	CIDR          string `json:"cidr"`
	MTU           int    `json:"mtu,omitempty"`
	PodsRangeName string `json:"podsRangeName,omitempty"`
	PodsCIDR      string `json:"podsCidr,omitempty"`
	SvcsRangeName string `json:"svcsRangeName,omitempty"`
	SvcsCIDR      string `json:"svcsCidr,omitempty"`
	UnderlayCIDR  string `json:"underlayCidr,omitempty"`
	GatewayIP     string `json:"gatewayIp,omitempty"`
}

// NodePoolInfo captures a GKE node pool's sizing and discovered node name.
type NodePoolInfo struct {
	Name           string `json:"name"`
	MachineType    string `json:"machineType"`
	NodeCount      int    `json:"nodeCount"`
	MaxPodsPerNode int    `json:"maxPodsPerNode,omitempty"`
	NodeName       string `json:"nodeName,omitempty"`
}

// TMMInfo holds the TMM pod's data-plane identity: external/internal VLAN
// self-IPs, interface names, PCI addresses, and pod routing state.
type TMMInfo struct {
	ExtSelfIP       string `json:"extSelfIp,omitempty"`
	ExtIfname       string `json:"extIfname,omitempty"`
	ExtPCI          string `json:"extPci,omitempty"`
	ExtAliasCIDR    string `json:"extAliasCidr,omitempty"`
	IntSelfIP       string `json:"intSelfIp,omitempty"`
	IntIfname       string `json:"intIfname,omitempty"`
	IntPCI          string `json:"intPci,omitempty"`
	IntAliasCIDR    string `json:"intAliasCidr,omitempty"`
	PodCIDR         string `json:"podCidr,omitempty"`
	PodGW           string `json:"podGw,omitempty"`
	TmmMtu          string `json:"tmmMtu,omitempty"`
	ManifestVersion string `json:"manifestVersion,omitempty"`
	LivePodName     string `json:"livePodName,omitempty"`
	LivePodIP       string `json:"livePodIp,omitempty"`
}

// EgressInfo holds the Phase 50 VXLAN egress / agent-pool state.
type EgressInfo struct {
	TestNamespace string `json:"testNamespace,omitempty"`
	VxlanPort     int    `json:"vxlanPort,omitempty"`
	VxlanKey      int    `json:"vxlanKey,omitempty"`
	VxlanMtu      int    `json:"vxlanMtu,omitempty"`
	AgentNode     string `json:"agentNode,omitempty"`
	AgentExtIface string `json:"agentExtIface,omitempty"`
	AgentExtIP    string `json:"agentExtIp,omitempty"`
}

// JumphostInfo carries the jumphost VM name, firewall rule, and NIC IPs.
type JumphostInfo struct {
	Enabled      bool   `json:"enabled"`
	MachineType  string `json:"machineType,omitempty"`
	InstanceName string `json:"instanceName,omitempty"`
	FirewallName string `json:"firewallName,omitempty"`
	MgmtIP       string `json:"mgmtIp,omitempty"`
	BnkExtIP     string `json:"bnkExtIp,omitempty"`
}

// Model is the GKE 3-VPC topology data model. String fields are "" when the
// corresponding resource has not yet been provisioned. Callers render "" as
// "(not provisioned)" so the diagram is useful before `gkebnkctl up`.
type Model struct {
	// Identity
	ClusterName       string `json:"clusterName"`
	Project           string `json:"project"`
	Zone              string `json:"zone"`
	Region            string `json:"region"`
	Pattern           string `json:"pattern"`
	KubernetesVersion string `json:"kubernetesVersion,omitempty"`

	// 3-VPC Architecture
	MainVPC           VPCInfo `json:"mainVpc"`
	ExternalVPC       VPCInfo `json:"externalVpc"`
	InternalVPC       VPCInfo `json:"internalVpc"`
	AllowInternalCIDR string  `json:"allowInternalCidr,omitempty"`
	NatGateways       bool    `json:"natGateways"`

	// GKE Node Pools
	DefaultPool NodePoolInfo `json:"defaultPool"`
	TmmPool     NodePoolInfo `json:"tmmPool"`
	AgentPool   NodePoolInfo `json:"agentPool"`

	// TMM data-plane identity
	TMM TMMInfo `json:"tmm"`

	// Egress / VXLAN overlay
	Egress EgressInfo `json:"egress"`

	// Jumphost
	Jumphost JumphostInfo `json:"jumphost"`

	// Gateway API
	GatewayClassName string `json:"gatewayClassName,omitempty"`
	VIPRange         string `json:"vipRange,omitempty"`

	// Live Kubernetes pod counts by namespace (populated by EnrichFromK8s).
	NamespacePods map[string]int `json:"namespacePods,omitempty"`
}

// np returns v when non-empty, otherwise "(not provisioned)".
func np(v string) string {
	if v == "" {
		return "(not provisioned)"
	}
	return v
}

// Build populates a Model from intent first, then overlays non-empty values
// from state. State wins whenever it has a non-empty value for a field; intent
// fills in everything that is deterministic before provisioning (VPCs, CIDRs,
// node pool sizing). Build is pure and deterministic — no I/O.
func Build(cl *intent.Cluster, st *state.State) Model {
	if cl == nil {
		return Model{}
	}

	region := cl.Region
	if region == "" && cl.Metadata.Zone != "" {
		if idx := strings.LastIndex(cl.Metadata.Zone, "-"); idx > 0 {
			region = cl.Metadata.Zone[:idx]
		} else {
			region = cl.Metadata.Zone
		}
	}

	defaultPoolName := cl.ClusterSpec.DefaultPool.Name
	if defaultPoolName == "" {
		defaultPoolName = "default-pool"
	}

	m := Model{
		ClusterName:       cl.Metadata.Name,
		Project:           cl.Metadata.Project,
		Zone:              cl.Metadata.Zone,
		Region:            region,
		Pattern:           cl.Pattern,
		KubernetesVersion: cl.ClusterSpec.KubernetesVersion,
		MainVPC: VPCInfo{
			Name:          cl.Network.Main.VPC,
			Subnet:        cl.Network.Main.Subnet,
			CIDR:          cl.Network.Main.CIDR,
			PodsRangeName: cl.Network.Main.PodsRangeName,
			PodsCIDR:      cl.Network.Main.PodsCIDR,
			SvcsRangeName: cl.Network.Main.SvcsRangeName,
			SvcsCIDR:      cl.Network.Main.SvcsCIDR,
		},
		ExternalVPC: VPCInfo{
			Name:          cl.Network.External.VPC,
			Subnet:        cl.Network.External.Subnet,
			CIDR:          cl.Network.External.CIDR,
			MTU:           cl.Network.External.MTU,
			PodsRangeName: cl.Network.External.PodsRangeName,
			PodsCIDR:      cl.Network.External.PodsCIDR,
			UnderlayCIDR:  cl.Network.External.UnderlayCIDR,
			GatewayIP:     cl.ExtGwIP,
		},
		InternalVPC: VPCInfo{
			Name:          cl.Network.Internal.VPC,
			Subnet:        cl.Network.Internal.Subnet,
			CIDR:          cl.Network.Internal.CIDR,
			MTU:           cl.Network.Internal.MTU,
			PodsRangeName: cl.Network.Internal.PodsRangeName,
			PodsCIDR:      cl.Network.Internal.PodsCIDR,
			GatewayIP:     cl.IntGwIP,
		},
		AllowInternalCIDR: cl.Network.AllowInternalCidr,
		NatGateways:       cl.Network.NatGatewaysEnabled(),
		DefaultPool: NodePoolInfo{
			Name:           defaultPoolName,
			MachineType:    cl.ClusterSpec.DefaultPool.MachineType,
			NodeCount:      cl.ClusterSpec.DefaultPool.NodeCount,
			MaxPodsPerNode: cl.ClusterSpec.DefaultPool.MaxPodsPerNode,
		},
		TmmPool: NodePoolInfo{
			Name:           cl.ClusterSpec.TmmPool.Name,
			MachineType:    cl.ClusterSpec.TmmPool.MachineType,
			NodeCount:      cl.ClusterSpec.TmmPool.NodeCount,
			MaxPodsPerNode: cl.ClusterSpec.TmmPool.MaxPodsPerNode,
		},
		AgentPool: NodePoolInfo{
			Name:           cl.ClusterSpec.AgentPool.Name,
			MachineType:    cl.ClusterSpec.AgentPool.MachineType,
			NodeCount:      cl.ClusterSpec.AgentPool.NodeCount,
			MaxPodsPerNode: cl.ClusterSpec.AgentPool.MaxPodsPerNode,
		},
		TMM: TMMInfo{
			TmmMtu:          cl.Bnk.TmmMtu,
			ManifestVersion: cl.Bnk.ManifestVersion,
		},
		Egress: EgressInfo{
			TestNamespace: cl.Egress.TestNamespace,
			VxlanPort:     cl.Egress.VxlanPort,
			VxlanKey:      cl.Egress.VxlanKey,
			VxlanMtu:      cl.Egress.VxlanMtu,
		},
		Jumphost: JumphostInfo{
			Enabled:     cl.Testing.Jumphost.Enabled,
			MachineType: cl.Testing.Jumphost.MachineType,
		},
	}

	if vip, err := cl.DefaultVIP(); err == nil && vip != "" {
		m.VIPRange = vip + " (default VIP)"
	}

	if st == nil {
		return m
	}

	overlay := func(field *string, keys ...string) {
		for _, k := range keys {
			if v := st.Get(k); v != "" {
				*field = v
				return
			}
		}
	}

	overlay(&m.Project, "PROJECT_ID")
	overlay(&m.Zone, "ZONE")
	overlay(&m.Region, "REGION")

	// Node pool discovered nodes.
	overlay(&m.TmmPool.NodeName, "TMM_NODE_NAME")
	overlay(&m.AgentPool.NodeName, "AGENT_NODE_NAME")

	// TMM data-plane state.
	overlay(&m.TMM.ExtSelfIP, "TMM_EXT_SELFIP")
	overlay(&m.TMM.IntSelfIP, "TMM_INT_SELFIP")
	overlay(&m.TMM.ExtIfname, "TMM_EXT_IFACE", "EXTERNAL_IFNAME")
	overlay(&m.TMM.IntIfname, "TMM_INT_IFACE", "INTERNAL_IFNAME")
	overlay(&m.TMM.ExtPCI, "TMM_EXT_PCI")
	overlay(&m.TMM.IntPCI, "TMM_INT_PCI")
	overlay(&m.TMM.ExtAliasCIDR, "TMM_EXT_ALIAS_CIDR")
	overlay(&m.TMM.IntAliasCIDR, "TMM_INT_ALIAS_CIDR")
	overlay(&m.TMM.PodCIDR, "TMM_POD_CIDR")
	overlay(&m.TMM.PodGW, "TMM_POD_GW")

	// Egress / Agent state.
	overlay(&m.Egress.AgentNode, "AGENT_NODE_NAME")
	overlay(&m.Egress.AgentExtIface, "AGENT_EXT_IFACE")
	overlay(&m.Egress.AgentExtIP, "AGENT_EXT_IP")

	// Jumphost state.
	overlay(&m.Jumphost.InstanceName, "JUMPHOST_INSTANCE_NAME", "JUMPHOST_INSTANCE_ID")
	overlay(&m.Jumphost.FirewallName, "JUMPHOST_FIREWALL_NAME")
	overlay(&m.Jumphost.MgmtIP, "JUMPHOST_MGMT_ENI_IP", "JUMPHOST_IP")
	overlay(&m.Jumphost.BnkExtIP, "JUMPHOST_BNK_EXT_NIC_IP", "JUMPHOST_BNK_EXT_ENI_IP", "JUMPHOST_EXT_IP")

	// Gateway API.
	overlay(&m.GatewayClassName, "GATEWAYCLASS_NAME")

	return m
}

// EnrichFromK8s queries live Kubernetes state across all BNK namespaces
// (f5-cne-core, f5-cne-system, f5-bnk-instance, f5-utils) plus the
// egress test namespace. Errors are ignored so topology degrades gracefully when
// the cluster is unreachable or partially provisioned.
func EnrichFromK8s(ctx context.Context, m *Model, cs kubernetes.Interface, dyn dynamic.Interface) {
	if m == nil || cs == nil {
		return
	}
	if m.NamespacePods == nil {
		m.NamespacePods = make(map[string]int)
	}

	namespaces := append([]string{}, BNKNamespaces...)
	if m.Egress.TestNamespace != "" {
		namespaces = append(namespaces, m.Egress.TestNamespace)
	}

	for _, ns := range namespaces {
		pods, err := cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			continue
		}
		if len(pods.Items) > 0 {
			m.NamespacePods[ns] = len(pods.Items)
		}
		for i := range pods.Items {
			p := &pods.Items[i]
			if strings.Contains(p.Name, "f5-tmm") || p.Labels["app"] == "f5-tmm" {
				if m.TMM.LivePodName == "" {
					m.TMM.LivePodName = p.Name
				}
				if m.TMM.LivePodIP == "" && p.Status.PodIP != "" {
					m.TMM.LivePodIP = p.Status.PodIP
				}
				if m.TmmPool.NodeName == "" && p.Spec.NodeName != "" {
					m.TmmPool.NodeName = p.Spec.NodeName
				}
			}
		}
	}

	if dyn != nil && m.GatewayClassName == "" {
		gvr := schema.GroupVersionResource{
			Group:    "gateway.networking.k8s.io",
			Version:  "v1",
			Resource: "gatewayclasses",
		}
		if list, err := dyn.Resource(gvr).List(ctx, metav1.ListOptions{Limit: 1}); err == nil && len(list.Items) > 0 {
			m.GatewayClassName = list.Items[0].GetName()
		}
	}
}

// RenderASCII renders a single box-drawing diagram of the GKE 3-VPC cluster
// data path in the envdiagram aesthetic (~70 cols). Fields that are not yet
// provisioned are shown as "(not provisioned)".
func RenderASCII(m Model) string {
	const innerW = 70

	top := "┌" + strings.Repeat("─", innerW) + "┐"
	bot := "└" + strings.Repeat("─", innerW) + "┘"

	boxLine := func(s string) string {
		inner := " " + s
		w := utf8.RuneCountInString(inner)
		switch {
		case w < innerW:
			inner += strings.Repeat(" ", innerW-w)
		case w > innerW:
			runes := []rune(inner)
			inner = string(runes[:innerW-1]) + "…"
		}
		return "│" + inner + "│"
	}
	blank := boxLine("")
	sep := "├" + strings.Repeat("─", innerW) + "┤"

	var lines []string
	lines = append(lines, top)

	// Header: identity
	lines = append(lines, boxLine(fmt.Sprintf("cluster: %-20s  region: %s (%s)", m.ClusterName, np(m.Region), np(m.Zone))))
	lines = append(lines, boxLine(fmt.Sprintf("project: %-20s  pattern: %s", np(m.Project), np(m.Pattern))))
	if m.KubernetesVersion != "" {
		lines = append(lines, boxLine(fmt.Sprintf("k8s-version: %s", m.KubernetesVersion)))
	}

	// 3-VPC Architecture
	lines = append(lines, blank)
	lines = append(lines, sep)
	lines = append(lines, boxLine("GKE 3-VPC NETWORK ARCHITECTURE"))
	lines = append(lines, boxLine(fmt.Sprintf("  main-vpc: %-16s  subnet: %-14s (%s)",
		np(m.MainVPC.Name), np(m.MainVPC.Subnet), np(m.MainVPC.CIDR))))
	if m.MainVPC.PodsCIDR != "" || m.MainVPC.SvcsCIDR != "" {
		lines = append(lines, boxLine(fmt.Sprintf("    pods: %-18s  svcs: %s",
			np(m.MainVPC.PodsCIDR), np(m.MainVPC.SvcsCIDR))))
	}
	lines = append(lines, boxLine(fmt.Sprintf("  ext-vpc:  %-16s  subnet: %-14s (%s)",
		np(m.ExternalVPC.Name), np(m.ExternalVPC.Subnet), np(m.ExternalVPC.CIDR))))
	if m.ExternalVPC.PodsCIDR != "" || m.ExternalVPC.MTU > 0 {
		lines = append(lines, boxLine(fmt.Sprintf("    ext-pods: %-14s  mtu: %d  underlay: %s",
			np(m.ExternalVPC.PodsCIDR), m.ExternalVPC.MTU, np(m.ExternalVPC.UnderlayCIDR))))
	}
	lines = append(lines, boxLine(fmt.Sprintf("  int-vpc:  %-16s  subnet: %-14s (%s)",
		np(m.InternalVPC.Name), np(m.InternalVPC.Subnet), np(m.InternalVPC.CIDR))))
	if m.InternalVPC.PodsCIDR != "" || m.InternalVPC.MTU > 0 {
		lines = append(lines, boxLine(fmt.Sprintf("    int-pods: %-14s  mtu: %d",
			np(m.InternalVPC.PodsCIDR), m.InternalVPC.MTU)))
	}

	// Jumphost / Test Client
	lines = append(lines, blank)
	lines = append(lines, sep)
	lines = append(lines, boxLine("JUMPHOST / EXTERNAL TRAFFIC SOURCE"))
	lines = append(lines, boxLine(fmt.Sprintf("  instance: %-22s  firewall: %s",
		np(m.Jumphost.InstanceName), np(m.Jumphost.FirewallName))))
	lines = append(lines, boxLine(fmt.Sprintf("  bnk-ext-ip: %-20s  mgmt-ip: %s",
		np(m.Jumphost.BnkExtIP), np(m.Jumphost.MgmtIP))))
	lines = append(lines, boxLine("  └─IAP SSH──► send test traffic to TMM SelfIP / VIP"))

	// GKE Node Pools
	lines = append(lines, blank)
	lines = append(lines, sep)
	lines = append(lines, boxLine("GKE NODE POOLS"))
	lines = append(lines, boxLine(fmt.Sprintf("  default-pool: %-14s  %s × %d",
		np(m.DefaultPool.Name), np(m.DefaultPool.MachineType), m.DefaultPool.NodeCount)))
	lines = append(lines, boxLine(fmt.Sprintf("  tmm-pool:     %-14s  %s × %d  node: %s",
		np(m.TmmPool.Name), np(m.TmmPool.MachineType), m.TmmPool.NodeCount, np(m.TmmPool.NodeName))))
	lines = append(lines, boxLine(fmt.Sprintf("  agent-pool:   %-14s  %s × %d  node: %s",
		np(m.AgentPool.Name), np(m.AgentPool.MachineType), m.AgentPool.NodeCount, np(m.AgentPool.NodeName))))

	// TMM VLANs & PCI passthrough
	lines = append(lines, blank)
	lines = append(lines, sep)
	lines = append(lines, boxLine("TMM DATA-PLANE VLANS (HOST-DEVICE / PCI)"))
	lines = append(lines, boxLine(fmt.Sprintf("  ext-vlan: selfip=%-16s  ifname=%-8s  pci=%s",
		np(m.TMM.ExtSelfIP), np(m.TMM.ExtIfname), np(m.TMM.ExtPCI))))
	lines = append(lines, boxLine(fmt.Sprintf("  int-vlan: selfip=%-16s  ifname=%-8s  pci=%s",
		np(m.TMM.IntSelfIP), np(m.TMM.IntIfname), np(m.TMM.IntPCI))))
	if m.TMM.PodCIDR != "" || m.TMM.PodGW != "" {
		lines = append(lines, boxLine(fmt.Sprintf("  pod-cidr: %-22s  pod-gw: %s",
			np(m.TMM.PodCIDR), np(m.TMM.PodGW))))
	}
	if m.TMM.LivePodName != "" {
		lines = append(lines, boxLine(fmt.Sprintf("  live-tmm: %-22s  pod-ip: %s",
			m.TMM.LivePodName, np(m.TMM.LivePodIP))))
	}

	// Egress / VXLAN overlay
	lines = append(lines, blank)
	lines = append(lines, sep)
	lines = append(lines, boxLine("EGRESS VXLAN OVERLAY"))
	lines = append(lines, boxLine(fmt.Sprintf("  test-ns: %-16s  vxlan: port=%d key=%d mtu=%d",
		np(m.Egress.TestNamespace), m.Egress.VxlanPort, m.Egress.VxlanKey, m.Egress.VxlanMtu)))
	lines = append(lines, boxLine(fmt.Sprintf("  agent-iface: %-12s  agent-ext-ip: %s",
		np(m.Egress.AgentExtIface), np(m.Egress.AgentExtIP))))

	// GatewayClass + VIP range
	lines = append(lines, blank)
	lines = append(lines, sep)
	lines = append(lines, boxLine("GATEWAY API"))
	lines = append(lines, boxLine(fmt.Sprintf("  gatewayclass: %s", np(m.GatewayClassName))))
	lines = append(lines, boxLine(fmt.Sprintf("  vip-range:    %s", np(m.VIPRange))))

	lines = append(lines, blank)
	lines = append(lines, bot)

	return strings.Join(lines, "\n")
}

// RenderMermaid renders a Mermaid graph TD diagram of the GKE 3-VPC cluster data path.
func RenderMermaid(m Model) string {
	var sb strings.Builder

	label := func(id, text string) string {
		return fmt.Sprintf(`%s["%s"]`, id, text)
	}
	edge := func(from, to string) string {
		return fmt.Sprintf("  %s --> %s", from, to)
	}

	sb.WriteString("graph TD\n\n")
	sb.WriteString(fmt.Sprintf("  %%%% Cluster: %s  Project: %s  Zone: %s\n\n", m.ClusterName, m.Project, m.Zone))

	// Internet
	sb.WriteString("  " + label("INET", "Internet / Cloud NAT") + "\n\n")

	// Three VPCs
	mainLbl := fmt.Sprintf("Main VPC\\n%s\\n%s", np(m.MainVPC.Name), np(m.MainVPC.CIDR))
	if m.MainVPC.PodsCIDR != "" {
		mainLbl += "\\npods: " + m.MainVPC.PodsCIDR
	}
	sb.WriteString("  " + label("MAINVPC", mainLbl) + "\n")
	sb.WriteString(edge("INET", "MAINVPC") + "\n")

	extLbl := fmt.Sprintf("External Dataplane VPC\\n%s\\n%s", np(m.ExternalVPC.Name), np(m.ExternalVPC.CIDR))
	if m.ExternalVPC.PodsCIDR != "" {
		extLbl += "\\npods: " + m.ExternalVPC.PodsCIDR
	}
	sb.WriteString("  " + label("EXTVPC", extLbl) + "\n")
	sb.WriteString(edge("INET", "EXTVPC") + "\n")

	intLbl := fmt.Sprintf("Internal Dataplane VPC\\n%s\\n%s", np(m.InternalVPC.Name), np(m.InternalVPC.CIDR))
	if m.InternalVPC.PodsCIDR != "" {
		intLbl += "\\npods: " + m.InternalVPC.PodsCIDR
	}
	sb.WriteString("  " + label("INTVPC", intLbl) + "\n")
	sb.WriteString(edge("INET", "INTVPC") + "\n\n")

	// Node pools
	defPoolLbl := fmt.Sprintf("Default Pool\\n%s\\n%s × %d", np(m.DefaultPool.Name), np(m.DefaultPool.MachineType), m.DefaultPool.NodeCount)
	sb.WriteString("  " + label("DEFPOOL", defPoolLbl) + "\n")
	sb.WriteString(edge("MAINVPC", "DEFPOOL") + "\n")

	tmmPoolLbl := fmt.Sprintf("TMM Pool\\n%s\\n%s × %d\\nnode: %s",
		np(m.TmmPool.Name), np(m.TmmPool.MachineType), m.TmmPool.NodeCount, np(m.TmmPool.NodeName))
	sb.WriteString("  " + label("TMMPOOL", tmmPoolLbl) + "\n")
	sb.WriteString(edge("MAINVPC", "TMMPOOL") + "\n")

	agentPoolLbl := fmt.Sprintf("Agent Pool\\n%s\\n%s × %d\\nnode: %s",
		np(m.AgentPool.Name), np(m.AgentPool.MachineType), m.AgentPool.NodeCount, np(m.AgentPool.NodeName))
	sb.WriteString("  " + label("AGENTPOOL", agentPoolLbl) + "\n")
	sb.WriteString(edge("MAINVPC", "AGENTPOOL") + "\n")
	sb.WriteString(edge("EXTVPC", "AGENTPOOL") + "\n\n")

	// TMM VLANs
	tmmExtLbl := fmt.Sprintf("TMM Ext VLAN\\nselfip: %s\\nifname: %s", np(m.TMM.ExtSelfIP), np(m.TMM.ExtIfname))
	sb.WriteString("  " + label("TMMEXT", tmmExtLbl) + "\n")
	sb.WriteString(edge("TMMPOOL", "TMMEXT") + "\n")
	sb.WriteString(edge("EXTVPC", "TMMEXT") + "\n")

	tmmIntLbl := fmt.Sprintf("TMM Int VLAN\\nselfip: %s\\nifname: %s", np(m.TMM.IntSelfIP), np(m.TMM.IntIfname))
	sb.WriteString("  " + label("TMMINT", tmmIntLbl) + "\n")
	sb.WriteString(edge("TMMPOOL", "TMMINT") + "\n")
	sb.WriteString(edge("INTVPC", "TMMINT") + "\n\n")

	// Jumphost
	jhLbl := fmt.Sprintf("Jumphost\\n%s\\nbnk-ext: %s", np(m.Jumphost.InstanceName), np(m.Jumphost.BnkExtIP))
	sb.WriteString("  " + label("JH", jhLbl) + "\n")
	sb.WriteString(edge("EXTVPC", "JH") + "\n")
	sb.WriteString(edge("JH", "TMMEXT") + "\n\n")

	// GatewayClass
	gcLbl := fmt.Sprintf("GatewayClass\\n%s\\nvip: %s", np(m.GatewayClassName), np(m.VIPRange))
	sb.WriteString("  " + label("GWC", gcLbl) + "\n")
	sb.WriteString(edge("TMMEXT", "GWC") + "\n")

	return sb.String()
}

// RenderDOT renders a Graphviz DOT digraph of the GKE 3-VPC cluster data path.
func RenderDOT(m Model) string {
	var sb strings.Builder
	sb.WriteString("digraph G {\n")
	sb.WriteString("  rankdir=TB;\n")
	sb.WriteString(fmt.Sprintf("  label=\"Cluster: %s (%s)\";\n", m.ClusterName, m.Zone))
	sb.WriteString(fmt.Sprintf("  MAINVPC [label=\"Main VPC\\n%s (%s)\"];\n", np(m.MainVPC.Name), np(m.MainVPC.CIDR)))
	sb.WriteString(fmt.Sprintf("  EXTVPC [label=\"External VPC\\n%s (%s)\"];\n", np(m.ExternalVPC.Name), np(m.ExternalVPC.CIDR)))
	sb.WriteString(fmt.Sprintf("  INTVPC [label=\"Internal VPC\\n%s (%s)\"];\n", np(m.InternalVPC.Name), np(m.InternalVPC.CIDR)))
	sb.WriteString(fmt.Sprintf("  TMMPOOL [label=\"TMM Pool\\n%s\\nnode: %s\"];\n", np(m.TmmPool.Name), np(m.TmmPool.NodeName)))
	sb.WriteString(fmt.Sprintf("  AGENTPOOL [label=\"Agent Pool\\n%s\\nnode: %s\"];\n", np(m.AgentPool.Name), np(m.AgentPool.NodeName)))
	sb.WriteString(fmt.Sprintf("  TMMEXT [label=\"TMM Ext VLAN\\n%s (%s)\"];\n", np(m.TMM.ExtSelfIP), np(m.TMM.ExtIfname)))
	sb.WriteString(fmt.Sprintf("  TMMINT [label=\"TMM Int VLAN\\n%s (%s)\"];\n", np(m.TMM.IntSelfIP), np(m.TMM.IntIfname)))
	sb.WriteString(fmt.Sprintf("  JH [label=\"Jumphost\\n%s (%s)\"];\n", np(m.Jumphost.InstanceName), np(m.Jumphost.BnkExtIP)))
	sb.WriteString(fmt.Sprintf("  GWC [label=\"GatewayClass\\n%s\\nvip: %s\"];\n", np(m.GatewayClassName), np(m.VIPRange)))
	sb.WriteString("  MAINVPC -> TMMPOOL;\n")
	sb.WriteString("  MAINVPC -> AGENTPOOL;\n")
	sb.WriteString("  EXTVPC -> TMMEXT;\n")
	sb.WriteString("  INTVPC -> TMMINT;\n")
	sb.WriteString("  TMMPOOL -> TMMEXT;\n")
	sb.WriteString("  TMMPOOL -> TMMINT;\n")
	sb.WriteString("  EXTVPC -> JH;\n")
	sb.WriteString("  JH -> TMMEXT;\n")
	sb.WriteString("  TMMEXT -> GWC;\n")
	sb.WriteString("}\n")
	return sb.String()
}

// RenderJSON serializes the topology Model as indented JSON.
func RenderJSON(m Model) (string, error) {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshalling topology model: %w", err)
	}
	return string(b), nil
}
