// Package bnkconst holds BNK-wide constants shared across packages that would
// otherwise create import cycles (e.g. phases ↔ render).
package bnkconst

const (
	// InstanceNamespace is the k8s namespace where the CNE instance resources
	// live (cloud-network-mapping CM, SA, CNEInstance CR, NADs).
	InstanceNamespace = "f5-cne-system"
)

// BenchmarkProxyPort is a fixed NodePort Forge pins on a benchmark proxy it
// deploys next to BNK, so the jumphost (which runs aiperf) can reach it.
type BenchmarkProxyPort struct {
	Proxy    string
	NodePort int32
}

// BenchmarkProxyNodePorts lists those NodePorts (clear of BNK's own fixed
// NodePorts, e.g. f5-spk-cwc on 30881). gkebnkctl opens exactly these
// on the cluster nodes / firewall to the jumphost; keep in sync with Forge's
// PROXY_NODE_PORTS (backend/services/proxy_deploy_service.py).
var BenchmarkProxyNodePorts = []BenchmarkProxyPort{
	{Proxy: "haproxy", NodePort: 30890},
	{Proxy: "nginx", NodePort: 30891},
	{Proxy: "envoy", NodePort: 30892},
	{Proxy: "envoy-ai-gateway", NodePort: 30893},
	{Proxy: "llm-d-router", NodePort: 30894},
}
