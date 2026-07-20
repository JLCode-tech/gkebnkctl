package phases

import (
	"context"
	"fmt"
	"os"
	"time"

	containerpb "cloud.google.com/go/container/apiv1/containerpb"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ktypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
)

const (
	tmmNodeLabelKey   = "app"
	tmmNodeLabelValue = "f5-tmm"
	gkeNodepoolLabel  = "cloud.google.com/gke-nodepool"

	gkeOpPollInterval   = 10 * time.Second
	gkeOpTimeout        = 30 * time.Minute
	clusterPollInterval = 15 * time.Second
	clusterTimeout      = 30 * time.Minute

	// tmmNodeListPollInterval/tmmNodeListMaxAttempts back off the node-label
	// step after the tmm-pool node pool reports RUNNING: on real GKE, node
	// objects can lag a few seconds to tens of seconds behind the node pool's
	// own RUNNING status, so an immediate empty List is not necessarily an
	// error yet. 6 attempts * 10s = up to ~50s of polling before giving up,
	// matching the "few retries over ~30-60s" window.
	tmmNodeListPollInterval = 10 * time.Second
	tmmNodeListMaxAttempts  = 6
)

// Phase20Cluster creates the GKE cluster (DPv2 + multi-networking + ip-alias,
// default pool) then the tmm-pool (f5-dataplane=true, 2 additional node/pod
// networks) and agent-pool (1 additional node/pod network), then labels the
// tmm-pool's nodes app=f5-tmm. Idempotent: GetCluster/GetNodePool gate every
// create call, matching bash bin/20-cluster.sh.
func Phase20Cluster(ctx context.Context, cl *intent.Cluster, _ *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 20] cluster: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		printPhase20Plan(cl)
		return nil
	}

	project := cl.Metadata.Project
	zone := cl.Metadata.Zone
	name := cl.Metadata.Name

	if _, err := ensureCluster(ctx, clients.GKE, project, zone, name, cl); err != nil {
		return fmt.Errorf("phase20: cluster: %w", err)
	}

	tmm := cl.ClusterSpec.TmmPool
	if err := ensureNodePool(ctx, clients.GKE, project, zone, name, buildTMMPoolConfig(cl)); err != nil {
		return fmt.Errorf("phase20: tmm-pool: %w", err)
	}
	agent := cl.ClusterSpec.AgentPool
	if err := ensureNodePool(ctx, clients.GKE, project, zone, name, buildAgentPoolConfig(cl)); err != nil {
		return fmt.Errorf("phase20: agent-pool: %w", err)
	}

	// AttachK8s is only called if not already attached — lets tests pre-seed
	// clients.K8s with a fake clientset, since AttachK8s's real path
	// (GetCluster -> BuildRESTConfig) requires live Application Default
	// Credentials (see docs/SPEC-go-tracer.md §5/§9, and the test-boundary
	// note in this package's clients_test.go equivalent).
	if clients.K8s == nil {
		if err := clients.AttachK8s(ctx, name, zone); err != nil {
			return fmt.Errorf("phase20: attaching k8s client: %w", err)
		}
	}

	fmt.Fprintf(os.Stderr, "[phase 20] labeling %s nodes app=f5-tmm\n", tmm.Name)
	if err := labelTMMNodes(ctx, clients.K8s, tmm.Name, tmmNodeListPollInterval); err != nil {
		return fmt.Errorf("phase20: labeling tmm nodes: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[phase 20] cluster OK: %s + %s + %s\n", name, tmm.Name, agent.Name)
	return nil
}

func printPhase20Plan(cl *intent.Cluster) {
	cs := cl.ClusterSpec
	fmt.Fprintf(os.Stderr,
		"[phase 20] would create GKE cluster %s (DPv2+multinet, default %s x%d) + %s (%s x%d, +2 node-nets +2 pod-nets) + %s (%s x%d, +1 node-net +1 pod-net)\n",
		cl.Metadata.Name, cs.DefaultPool.MachineType, cs.DefaultPool.NodeCount,
		cs.TmmPool.Name, cs.TmmPool.MachineType, cs.TmmPool.NodeCount,
		cs.AgentPool.Name, cs.AgentPool.MachineType, cs.AgentPool.NodeCount,
	)
}

// ensureCluster creates the GKE cluster if absent, or waits for RUNNING if
// it already exists in a transitional state. Returns the RUNNING cluster.
func ensureCluster(ctx context.Context, gke gcp.GKEAPI, project, zone, name string, cl *intent.Cluster) (*containerpb.Cluster, error) {
	existing, err := gke.GetCluster(ctx, &containerpb.GetClusterRequest{ProjectId: project, Zone: zone, ClusterId: name})
	if err == nil {
		switch existing.GetStatus() {
		case containerpb.Cluster_RUNNING:
			fmt.Fprintf(os.Stderr, "[phase 20] cluster %s already exists, status=RUNNING, skipping create\n", name)
			return existing, nil
		case containerpb.Cluster_ERROR:
			//lint:ignore SA1019 GKE SDK uses deprecated ID-based request fields; Name-based migration is a follow-up
			return nil, fmt.Errorf("cluster %s is in ERROR state: %s", name, existing.GetStatusMessage())
		default:
			fmt.Fprintf(os.Stderr, "[phase 20] cluster %s already exists, status=%s, waiting for RUNNING\n", name, existing.GetStatus())
			return waitForClusterRunning(ctx, gke, project, zone, name)
		}
	}
	if !isNotFound(err) {
		return nil, fmt.Errorf("GetCluster: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[phase 20] creating GKE cluster %s (DPv2 + multi-networking + ip-alias)\n", name)
	op, err := gke.CreateCluster(ctx, &containerpb.CreateClusterRequest{
		ProjectId: project,
		Zone:      zone,
		Cluster:   buildClusterConfig(cl),
	})
	if err != nil {
		return nil, fmt.Errorf("CreateCluster: %w", err)
	}
	if err := waitForGKEOp(ctx, gke, project, zone, op.GetName()); err != nil {
		return nil, fmt.Errorf("waiting for cluster create: %w", err)
	}
	return waitForClusterRunning(ctx, gke, project, zone, name)
}

// buildClusterConfig maps cluster.yaml + intent derived fields 1:1 onto the
// gcloud flags in reference/bash-seed/bin/20-cluster.sh (see
// docs/SPEC-go-tracer.md §9.1): --enable-ip-alias, --enable-dataplane-v2,
// --enable-multi-networking, --cluster-secondary-range-name,
// --services-secondary-range-name.
func buildClusterConfig(cl *intent.Cluster) *containerpb.Cluster {
	main := cl.Network.Main
	return &containerpb.Cluster{
		Name:                  cl.Metadata.Name,
		InitialClusterVersion: cl.ClusterSpec.KubernetesVersion,
		Network:               main.VPC,
		Subnetwork:            main.Subnet,
		ResourceLabels:        cl.Metadata.Labels,
		IpAllocationPolicy: &containerpb.IPAllocationPolicy{
			UseIpAliases:               true,
			ClusterSecondaryRangeName:  main.PodsRangeName,
			ServicesSecondaryRangeName: main.SvcsRangeName,
		},
		NetworkConfig: &containerpb.NetworkConfig{
			DatapathProvider:      containerpb.DatapathProvider_ADVANCED_DATAPATH,
			EnableMultiNetworking: true,
		},
		NodePools: []*containerpb.NodePool{
			{
				Name:             "default-pool",
				InitialNodeCount: int32(cl.ClusterSpec.DefaultPool.NodeCount), //nolint:gosec -- node counts are small operator-set values
				Config: &containerpb.NodeConfig{
					MachineType: cl.ClusterSpec.DefaultPool.MachineType,
				},
			},
		},
	}
}

// buildTMMPoolConfig maps cluster.yaml cluster.tmmPool onto
// --additional-node-network (external+internal) + --additional-pod-network
// (external+internal, max-pods-per-node) + --node-labels=f5-dataplane=true,
// per reference/bash-seed/bin/20-cluster.sh.
func buildTMMPoolConfig(cl *intent.Cluster) *containerpb.NodePool {
	pool := cl.ClusterSpec.TmmPool
	ext := cl.Network.External
	in := cl.Network.Internal
	maxPods := &containerpb.MaxPodsConstraint{MaxPodsPerNode: int64(pool.MaxPodsPerNode)}

	return &containerpb.NodePool{
		Name:             pool.Name,
		InitialNodeCount: int32(pool.NodeCount), //nolint:gosec -- node counts are small operator-set values
		Config: &containerpb.NodeConfig{
			MachineType: pool.MachineType,
			Labels:      map[string]string{"f5-dataplane": "true"},
		},
		NetworkConfig: &containerpb.NodeNetworkConfig{
			AdditionalNodeNetworkConfigs: []*containerpb.AdditionalNodeNetworkConfig{
				{Network: ext.VPC, Subnetwork: ext.Subnet},
				{Network: in.VPC, Subnetwork: in.Subnet},
			},
			AdditionalPodNetworkConfigs: []*containerpb.AdditionalPodNetworkConfig{
				{Subnetwork: ext.Subnet, SecondaryPodRange: ext.PodsRangeName, MaxPodsPerNode: maxPods},
				{Subnetwork: in.Subnet, SecondaryPodRange: in.PodsRangeName, MaxPodsPerNode: maxPods},
			},
		},
	}
}

// buildAgentPoolConfig maps cluster.yaml cluster.agentPool onto a single
// --additional-node-network + --additional-pod-network on the external VPC
// only, per reference/bash-seed/bin/20-cluster.sh.
func buildAgentPoolConfig(cl *intent.Cluster) *containerpb.NodePool {
	pool := cl.ClusterSpec.AgentPool
	ext := cl.Network.External
	maxPods := &containerpb.MaxPodsConstraint{MaxPodsPerNode: int64(pool.MaxPodsPerNode)}

	return &containerpb.NodePool{
		Name:             pool.Name,
		InitialNodeCount: int32(pool.NodeCount), //nolint:gosec -- node counts are small operator-set values
		Config: &containerpb.NodeConfig{
			MachineType: pool.MachineType,
			Labels:      map[string]string{"f5-dataplane": "true"},
		},
		NetworkConfig: &containerpb.NodeNetworkConfig{
			AdditionalNodeNetworkConfigs: []*containerpb.AdditionalNodeNetworkConfig{
				{Network: ext.VPC, Subnetwork: ext.Subnet},
			},
			AdditionalPodNetworkConfigs: []*containerpb.AdditionalPodNetworkConfig{
				{Subnetwork: ext.Subnet, SecondaryPodRange: ext.PodsRangeName, MaxPodsPerNode: maxPods},
			},
		},
	}
}

// ensureNodePool creates pool on cluster clusterName if it doesn't already
// exist (or waits for RUNNING if it's mid-provisioning).
func ensureNodePool(ctx context.Context, gke gcp.GKEAPI, project, zone, clusterName string, pool *containerpb.NodePool) error {
	existing, err := gke.GetNodePool(ctx, &containerpb.GetNodePoolRequest{
		ProjectId: project, Zone: zone, ClusterId: clusterName, NodePoolId: pool.GetName(),
	})
	if err == nil {
		switch existing.GetStatus() {
		case containerpb.NodePool_RUNNING:
			fmt.Fprintf(os.Stderr, "[phase 20] node pool %s already exists, skipping create\n", pool.GetName())
			return nil
		case containerpb.NodePool_ERROR:
			//lint:ignore SA1019 GKE SDK uses deprecated ID-based request fields; Name-based migration is a follow-up
			return fmt.Errorf("node pool %s is in ERROR state: %s", pool.GetName(), existing.GetStatusMessage())
		default:
			fmt.Fprintf(os.Stderr, "[phase 20] node pool %s already exists, status=%s, waiting for RUNNING\n", pool.GetName(), existing.GetStatus())
			return waitForNodePoolRunning(ctx, gke, project, zone, clusterName, pool.GetName())
		}
	}
	if !isNotFound(err) {
		return fmt.Errorf("GetNodePool %s: %w", pool.GetName(), err)
	}

	fmt.Fprintf(os.Stderr, "[phase 20] creating node pool %s (%s x%d)\n", pool.GetName(), pool.GetConfig().GetMachineType(), pool.GetInitialNodeCount())
	op, err := gke.CreateNodePool(ctx, &containerpb.CreateNodePoolRequest{
		ProjectId: project, Zone: zone, ClusterId: clusterName, NodePool: pool,
	})
	if err != nil {
		return fmt.Errorf("CreateNodePool %s: %w", pool.GetName(), err)
	}
	if err := waitForGKEOp(ctx, gke, project, zone, op.GetName()); err != nil {
		return fmt.Errorf("waiting for node pool %s create: %w", pool.GetName(), err)
	}
	return waitForNodePoolRunning(ctx, gke, project, zone, clusterName, pool.GetName())
}

// waitForGKEOp polls GetOperation until the operation reaches DONE (per
// docs/SPEC-go-tracer.md §3: GKE LROs are polled, unlike compute's
// op.Wait()-capable *compute.Operation).
func waitForGKEOp(ctx context.Context, gke gcp.GKEAPI, project, zone, opName string) error {
	deadline := time.Now().Add(gkeOpTimeout)
	for {
		op, err := gke.GetOperation(ctx, &containerpb.GetOperationRequest{ProjectId: project, Zone: zone, OperationId: opName})
		if err != nil {
			return fmt.Errorf("GetOperation %s: %w", opName, err)
		}
		switch op.GetStatus() {
		case containerpb.Operation_DONE:
			if op.GetError() != nil {
				return fmt.Errorf("operation %s finished with error: %v", opName, op.GetError())
			}
			return nil
		case containerpb.Operation_ABORTING:
			//lint:ignore SA1019 GKE SDK uses deprecated ID-based request fields; Name-based migration is a follow-up
			return fmt.Errorf("operation %s is aborting: %s", opName, op.GetStatusMessage())
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for operation %s", opName)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(gkeOpPollInterval):
		}
	}
}

// waitForClusterRunning polls GetCluster until the cluster reaches RUNNING.
func waitForClusterRunning(ctx context.Context, gke gcp.GKEAPI, project, zone, name string) (*containerpb.Cluster, error) {
	deadline := time.Now().Add(clusterTimeout)
	for {
		c, err := gke.GetCluster(ctx, &containerpb.GetClusterRequest{ProjectId: project, Zone: zone, ClusterId: name})
		if err != nil {
			return nil, fmt.Errorf("GetCluster %s: %w", name, err)
		}
		switch c.GetStatus() {
		case containerpb.Cluster_RUNNING:
			return c, nil
		case containerpb.Cluster_ERROR, containerpb.Cluster_STOPPING:
			return nil, fmt.Errorf("cluster %s in unexpected status %s", name, c.GetStatus())
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for cluster %s RUNNING", name)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(clusterPollInterval):
		}
	}
}

// waitForNodePoolRunning polls GetNodePool until the pool reaches RUNNING.
func waitForNodePoolRunning(ctx context.Context, gke gcp.GKEAPI, project, zone, clusterName, poolName string) error {
	deadline := time.Now().Add(clusterTimeout)
	for {
		p, err := gke.GetNodePool(ctx, &containerpb.GetNodePoolRequest{ProjectId: project, Zone: zone, ClusterId: clusterName, NodePoolId: poolName})
		if err != nil {
			return fmt.Errorf("GetNodePool %s: %w", poolName, err)
		}
		switch p.GetStatus() {
		case containerpb.NodePool_RUNNING:
			return nil
		case containerpb.NodePool_ERROR:
			//lint:ignore SA1019 GKE SDK uses deprecated ID-based request fields; Name-based migration is a follow-up
			return fmt.Errorf("node pool %s in ERROR state: %s", poolName, p.GetStatusMessage())
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for node pool %s RUNNING", poolName)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(clusterPollInterval):
		}
	}
}

// labelTMMNodes labels every node in poolName's GKE node pool app=f5-tmm —
// the bnk-node-sysctls DS (Phase37) and CNEInstance scheduling (Phase38)
// depend on this label, per reference/bash-seed/bin/20-cluster.sh.
//
// The node pool reporting RUNNING does not guarantee its node objects have
// registered with the API server yet on real GKE, so an empty first List is
// polled a few times (pollInterval, up to tmmNodeListMaxAttempts) rather than
// erroring immediately. pollInterval is injectable so tests stay fast.
func labelTMMNodes(ctx context.Context, k8sClient kubernetes.Interface, poolName string, pollInterval time.Duration) error {
	var nodes *corev1.NodeList
	for attempt := 1; attempt <= tmmNodeListMaxAttempts; attempt++ {
		var err error
		nodes, err = k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{
			LabelSelector: gkeNodepoolLabel + "=" + poolName,
		})
		if err != nil {
			return fmt.Errorf("listing nodes for pool %s: %w", poolName, err)
		}
		if len(nodes.Items) > 0 {
			break
		}
		if attempt == tmmNodeListMaxAttempts {
			return fmt.Errorf("no nodes found for pool %s after %d attempts — node pool may still be provisioning", poolName, tmmNodeListMaxAttempts)
		}
		fmt.Fprintf(os.Stderr, "[phase 20] no nodes registered yet for pool %s (attempt %d/%d) — node objects can lag pool RUNNING status, retrying in %s\n",
			poolName, attempt, tmmNodeListMaxAttempts, pollInterval)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}

	patch := []byte(fmt.Sprintf(`{"metadata":{"labels":{%q:%q}}}`, tmmNodeLabelKey, tmmNodeLabelValue))
	for i := range nodes.Items {
		n := &nodes.Items[i]
		if n.Labels[tmmNodeLabelKey] == tmmNodeLabelValue {
			continue
		}
		if _, err := k8sClient.CoreV1().Nodes().Patch(ctx, n.Name, ktypes.StrategicMergePatchType, patch, metav1.PatchOptions{}); err != nil {
			return fmt.Errorf("labeling node %s: %w", n.Name, err)
		}
	}
	return nil
}
