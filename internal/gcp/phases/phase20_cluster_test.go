package phases

import (
	"context"
	"testing"

	containerpb "cloud.google.com/go/container/apiv1/containerpb"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakek8s "k8s.io/client-go/kubernetes/fake"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
)

func TestPhase20Cluster_DryRun_MakesZeroMutations(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	gke := &fakeGKE{}
	clients := &gcp.Clients{GKE: gke}

	if err := Phase20Cluster(context.Background(), cl, st, clients, true); err != nil {
		t.Fatalf("Phase20Cluster(dryRun=true): %v", err)
	}
	if len(gke.createClusterCalls) != 0 || len(gke.createNodePoolCalls) != 0 {
		t.Error("dry-run must make zero GKE mutations")
	}
}

func TestPhase20Cluster_CreatesWhenAbsent(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	gke := &fakeGKE{}
	k8sClient := fakek8s.NewClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "tmm-node-1", Labels: map[string]string{gkeNodepoolLabel: "tmm-pool"}}},
	)
	clients := &gcp.Clients{GKE: gke, K8s: k8sClient}

	if err := Phase20Cluster(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("Phase20Cluster: %v", err)
	}
	if len(gke.createClusterCalls) != 1 || gke.createClusterCalls[0] != "bnk-hostdev" {
		t.Errorf("expected 1 CreateCluster call for bnk-hostdev, got %v", gke.createClusterCalls)
	}
	wantPools := map[string]bool{"tmm-pool": false, "agent-pool": false}
	for _, p := range gke.createNodePoolCalls {
		if _, ok := wantPools[p]; ok {
			wantPools[p] = true
		}
	}
	for pool, called := range wantPools {
		if !called {
			t.Errorf("expected CreateNodePool call for %s, got calls=%v", pool, gke.createNodePoolCalls)
		}
	}

	node, err := k8sClient.CoreV1().Nodes().Get(context.Background(), "tmm-node-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting labeled node: %v", err)
	}
	if node.Labels[tmmNodeLabelKey] != tmmNodeLabelValue {
		t.Errorf("expected tmm-node-1 to be labeled %s=%s, got labels=%v", tmmNodeLabelKey, tmmNodeLabelValue, node.Labels)
	}
}

// TestPhase20Cluster_SkipsWhenClusterAndPoolsRunning proves Get-returns-
// RUNNING skips Create for both the cluster and both node pools.
func TestPhase20Cluster_SkipsWhenClusterAndPoolsRunning(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	gke := &fakeGKE{
		clusters: map[string]*containerpb.Cluster{"bnk-hostdev": {Name: "bnk-hostdev", Status: containerpb.Cluster_RUNNING}},
		nodePools: map[string]*containerpb.NodePool{
			"tmm-pool":   {Name: "tmm-pool", Status: containerpb.NodePool_RUNNING},
			"agent-pool": {Name: "agent-pool", Status: containerpb.NodePool_RUNNING},
		},
	}
	k8sClient := fakek8s.NewClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "tmm-node-1", Labels: map[string]string{gkeNodepoolLabel: "tmm-pool"}}},
	)
	clients := &gcp.Clients{GKE: gke, K8s: k8sClient}

	if err := Phase20Cluster(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("Phase20Cluster: %v", err)
	}
	if len(gke.createClusterCalls) != 0 {
		t.Errorf("expected 0 CreateCluster calls when cluster already RUNNING, got %v", gke.createClusterCalls)
	}
	if len(gke.createNodePoolCalls) != 0 {
		t.Errorf("expected 0 CreateNodePool calls when pools already RUNNING, got %v", gke.createNodePoolCalls)
	}
}

func TestBuildTMMPoolConfig_MapsMultiNetworking(t *testing.T) {
	cl := testCluster()
	pool := buildTMMPoolConfig(cl)

	if pool.GetName() != "tmm-pool" {
		t.Errorf("Name = %q, want tmm-pool", pool.GetName())
	}
	if pool.GetConfig().GetLabels()["f5-dataplane"] != "true" {
		t.Error("expected f5-dataplane=true node label")
	}
	nodeNets := pool.GetNetworkConfig().GetAdditionalNodeNetworkConfigs()
	if len(nodeNets) != 2 {
		t.Fatalf("expected 2 AdditionalNodeNetworkConfigs (ext+int), got %d", len(nodeNets))
	}
	if nodeNets[0].GetNetwork() != "bnkhd-ext" || nodeNets[1].GetNetwork() != "bnkhd-int" {
		t.Errorf("unexpected node network order/values: %+v", nodeNets)
	}
	podNets := pool.GetNetworkConfig().GetAdditionalPodNetworkConfigs()
	if len(podNets) != 2 {
		t.Fatalf("expected 2 AdditionalPodNetworkConfigs (ext+int), got %d", len(podNets))
	}
	if podNets[0].GetMaxPodsPerNode().GetMaxPodsPerNode() != 8 {
		t.Errorf("expected max-pods-per-node=8, got %d", podNets[0].GetMaxPodsPerNode().GetMaxPodsPerNode())
	}
}

func TestBuildAgentPoolConfig_ExternalOnly(t *testing.T) {
	cl := testCluster()
	pool := buildAgentPoolConfig(cl)

	nodeNets := pool.GetNetworkConfig().GetAdditionalNodeNetworkConfigs()
	if len(nodeNets) != 1 {
		t.Fatalf("expected exactly 1 AdditionalNodeNetworkConfig (external only), got %d", len(nodeNets))
	}
	if nodeNets[0].GetNetwork() != "bnkhd-ext" {
		t.Errorf("expected external VPC bnkhd-ext, got %s", nodeNets[0].GetNetwork())
	}
	podNets := pool.GetNetworkConfig().GetAdditionalPodNetworkConfigs()
	if len(podNets) != 1 {
		t.Fatalf("expected exactly 1 AdditionalPodNetworkConfig (external only), got %d", len(podNets))
	}
}

func TestBuildClusterConfig_EnablesMultiNetworkingAndDPv2(t *testing.T) {
	cl := testCluster()
	c := buildClusterConfig(cl)

	if !c.GetNetworkConfig().GetEnableMultiNetworking() {
		t.Error("expected EnableMultiNetworking=true")
	}
	if c.GetNetworkConfig().GetDatapathProvider() != containerpb.DatapathProvider_ADVANCED_DATAPATH {
		t.Errorf("expected DatapathProvider=ADVANCED_DATAPATH, got %v", c.GetNetworkConfig().GetDatapathProvider())
	}
	if !c.GetIpAllocationPolicy().GetUseIpAliases() {
		t.Error("expected UseIpAliases=true")
	}
	if c.GetIpAllocationPolicy().GetClusterSecondaryRangeName() != "bnkhd-pods" {
		t.Errorf("ClusterSecondaryRangeName = %q, want bnkhd-pods", c.GetIpAllocationPolicy().GetClusterSecondaryRangeName())
	}
	if c.GetIpAllocationPolicy().GetServicesSecondaryRangeName() != "bnkhd-svcs" {
		t.Errorf("ServicesSecondaryRangeName = %q, want bnkhd-svcs", c.GetIpAllocationPolicy().GetServicesSecondaryRangeName())
	}
}

func TestLabelTMMNodes_NoNodesErrors(t *testing.T) {
	k8sClient := fakek8s.NewClientset()
	if err := labelTMMNodes(context.Background(), k8sClient, "tmm-pool"); err == nil {
		t.Fatal("expected an error when no nodes match the tmm-pool label selector")
	}
}

func TestLabelTMMNodes_LabelsMatchingNodesOnly(t *testing.T) {
	k8sClient := fakek8s.NewClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "tmm-1", Labels: map[string]string{gkeNodepoolLabel: "tmm-pool"}}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "agent-1", Labels: map[string]string{gkeNodepoolLabel: "agent-pool"}}},
	)
	if err := labelTMMNodes(context.Background(), k8sClient, "tmm-pool"); err != nil {
		t.Fatalf("labelTMMNodes: %v", err)
	}
	tmm, _ := k8sClient.CoreV1().Nodes().Get(context.Background(), "tmm-1", metav1.GetOptions{})
	if tmm.Labels[tmmNodeLabelKey] != tmmNodeLabelValue {
		t.Error("expected tmm-1 to be labeled app=f5-tmm")
	}
	agent, _ := k8sClient.CoreV1().Nodes().Get(context.Background(), "agent-1", metav1.GetOptions{})
	if agent.Labels[tmmNodeLabelKey] == tmmNodeLabelValue {
		t.Error("agent-1 must NOT be labeled app=f5-tmm — it is not in the tmm-pool")
	}
}
