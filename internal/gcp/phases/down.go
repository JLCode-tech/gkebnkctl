package phases

import (
	"context"
	"fmt"
	"os"
	"time"

	computepb "cloud.google.com/go/compute/apiv1/computepb"
	containerpb "cloud.google.com/go/container/apiv1/containerpb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
)

// PhaseDownCluster deletes the GKE cluster (node pools go with it) and waits
// until it is gone. Tolerates "already gone" (a 404 on GetCluster / a 404 on
// DeleteCluster).
func PhaseDownCluster(ctx context.Context, cl *intent.Cluster, st *state.State, clients *gcp.Clients) error {
	project := cl.Metadata.Project
	zone := cl.Metadata.Zone
	name := cl.Metadata.Name

	fmt.Fprintf(os.Stderr, "[down] cluster: deleting %s (node pools go with it)\n", name)

	clusterName := fmt.Sprintf("projects/%s/locations/%s/clusters/%s", project, zone, name)
	if _, err := clients.GKE.GetCluster(ctx, &containerpb.GetClusterRequest{Name: clusterName}); err != nil {
		if isNotFound(err) {
			fmt.Fprintf(os.Stderr, "[down] cluster %s not found — skipping\n", name)
			return nil
		}
		return fmt.Errorf("down: cluster: GetCluster: %w", err)
	}

	// Best-effort strip of F5 BNK CR finalizers before deleting the GKE cluster.
	if clients.Dynamic != nil {
		stripStuckFinalizersWithClient(ctx, clients.Dynamic)
	} else if st != nil {
		if kcPath := st.KubeconfigPath(); kcPath != "" {
			if _, err := os.Stat(kcPath); err == nil {
				stripStuckFinalizers(ctx, kcPath)
			}
		}
	}

	op, err := clients.GKE.DeleteCluster(ctx, &containerpb.DeleteClusterRequest{Name: clusterName})
	if err != nil {
		if isNotFound(err) {
			fmt.Fprintf(os.Stderr, "[down] cluster %s already gone\n", name)
			return nil
		}
		return fmt.Errorf("down: cluster: DeleteCluster: %w", err)
	}
	if err := waitForGKEOp(ctx, clients.GKE, project, zone, op.GetName()); err != nil {
		return fmt.Errorf("down: cluster: waiting for delete: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[down] cluster %s deleted\n", name)
	return nil
}

// PhaseDownNetworks deletes ONLY the main VPC's firewall, subnet, and VPC
// (in that order — firewall/subnet before VPC). It deliberately never
// references network.external / network.internal — the shared dataplane
// VPCs may be used by other clusters and must never be touched by teardown,
// per docs/SPEC-go-tracer.md §3 "Down phases".
func PhaseDownNetworks(ctx context.Context, cl *intent.Cluster, _ *state.State, clients *gcp.Clients, keepDataplane bool) error {
	project := cl.Metadata.Project
	region := cl.Region
	main := cl.Network.Main

	fwName := main.VPC + "-allow-internal"
	fmt.Fprintf(os.Stderr, "[down] main-vpc: deleting firewall %s\n", fwName)
	if _, err := clients.Firewalls.Get(ctx, &computepb.GetFirewallRequest{Project: project, Firewall: fwName}); err != nil {
		if isNotFound(err) {
			fmt.Fprintf(os.Stderr, "[down] firewall %s not found — skipping\n", fwName)
		} else {
			return fmt.Errorf("down: main-vpc: checking firewall %s: %w", fwName, err)
		}
	} else {
		op, err := clients.Firewalls.Delete(ctx, &computepb.DeleteFirewallRequest{Project: project, Firewall: fwName})
		if err != nil {
			if !isNotFound(err) {
				return fmt.Errorf("down: main-vpc: deleting firewall %s: %w", fwName, err)
			}
		} else if err := insertAndWait(ctx, op); err != nil {
			return fmt.Errorf("down: main-vpc: waiting for firewall delete: %w", err)
		}
	}

	fmt.Fprintf(os.Stderr, "[down] main-vpc: deleting subnet %s\n", main.Subnet)
	if _, err := clients.Subnetworks.Get(ctx, &computepb.GetSubnetworkRequest{Project: project, Region: region, Subnetwork: main.Subnet}); err != nil {
		if isNotFound(err) {
			fmt.Fprintf(os.Stderr, "[down] subnet %s not found — skipping\n", main.Subnet)
		} else {
			return fmt.Errorf("down: main-vpc: checking subnet %s: %w", main.Subnet, err)
		}
	} else {
		op, err := clients.Subnetworks.Delete(ctx, &computepb.DeleteSubnetworkRequest{Project: project, Region: region, Subnetwork: main.Subnet})
		if err != nil {
			if !isNotFound(err) {
				return fmt.Errorf("down: main-vpc: deleting subnet %s: %w", main.Subnet, err)
			}
		} else if err := insertAndWait(ctx, op); err != nil {
			return fmt.Errorf("down: main-vpc: waiting for subnet delete: %w", err)
		}
	}

	fmt.Fprintf(os.Stderr, "[down] main-vpc: deleting VPC %s\n", main.VPC)
	if _, err := clients.Networks.Get(ctx, &computepb.GetNetworkRequest{Project: project, Network: main.VPC}); err != nil {
		if isNotFound(err) {
			fmt.Fprintf(os.Stderr, "[down] VPC %s not found — skipping\n", main.VPC)
		} else {
			return fmt.Errorf("down: main-vpc: checking VPC %s: %w", main.VPC, err)
		}
	} else {
		op, err := clients.Networks.Delete(ctx, &computepb.DeleteNetworkRequest{Project: project, Network: main.VPC})
		if err != nil {
			if !isNotFound(err) {
				return fmt.Errorf("down: main-vpc: deleting VPC %s: %w", main.VPC, err)
			}
		} else if err := insertAndWait(ctx, op); err != nil {
			return fmt.Errorf("down: main-vpc: waiting for VPC delete: %w", err)
		}
	}

	if !keepDataplane {
		if err := teardownDataplaneResources(ctx, project, region, cl.Network.External.VPC, cl.Metadata.Name, clients); err != nil {
			return fmt.Errorf("down: external-vpc: %w", err)
		}
		if err := teardownDataplaneResources(ctx, project, region, cl.Network.Internal.VPC, cl.Metadata.Name, clients); err != nil {
			return fmt.Errorf("down: internal-vpc: %w", err)
		}
	}

	fmt.Fprintf(os.Stderr, "[down] main-vpc done — shared dataplane VPCs %s/%s left untouched\n",
		cl.Network.External.VPC, cl.Network.Internal.VPC)
	return nil
}

func teardownDataplaneResources(ctx context.Context, project, region, vpc, cluster string, clients *gcp.Clients) error {
	byCluster, err := DiscoverDataplaneResources(ctx, clients, project, region, vpc)
	if err != nil {
		return err
	}

	clusterResources := byCluster[cluster]
	if clusterResources == nil {
		clusterResources = &VPCResources{}
	}

	// Delete subnets
	for _, sn := range clusterResources.Subnets {
		name := sn.GetName()
		fmt.Fprintf(os.Stderr, "[down] shared-vpc %s: deleting subnet %s\n", vpc, name)
		op, err := clients.Subnetworks.Delete(ctx, &computepb.DeleteSubnetworkRequest{Project: project, Region: region, Subnetwork: name})
		if err != nil && !isNotFound(err) {
			return fmt.Errorf("deleting subnet %s: %w", name, err)
		}
		if op != nil {
			if err := insertAndWait(ctx, op); err != nil {
				return fmt.Errorf("waiting for subnet delete: %w", err)
			}
		}
	}

	// Determine if other clusters are still using this shared VPC.
	// Subnets are per-cluster, so their presence indicates an active cluster.
	otherClustersExist := false
	for c, res := range byCluster {
		if c != cluster && c != "unmarked" && len(res.Subnets) > 0 {
			otherClustersExist = true
			break
		}
	}

	// Delete firewalls (last-one-out shared resource)
	var sweepFirewalls []*computepb.Firewall
	if otherClustersExist {
		sweepFirewalls = clusterResources.Firewalls
	} else {
		for c, res := range byCluster {
			if c != "unmarked" {
				sweepFirewalls = append(sweepFirewalls, res.Firewalls...)
			}
		}
	}

	for _, fw := range sweepFirewalls {
		name := fw.GetName()
		if otherClustersExist {
			// We knowingly accept the stale marker naming this departed cluster. The firewall
			// remains functional for the surviving clusters, and the last one out (which sweeps ALL marked
			// resources regardless of their specific cluster label) will delete it.
			fmt.Fprintf(os.Stderr, "[down] shared-vpc %s: skipping firewall %s because other clusters still have subnets in this VPC (leaving stale ownership marker)\n", vpc, name)
		} else {
			fmt.Fprintf(os.Stderr, "[down] shared-vpc %s: deleting firewall %s\n", vpc, name)
			op, err := clients.Firewalls.Delete(ctx, &computepb.DeleteFirewallRequest{Project: project, Firewall: name})
			if err != nil && !isNotFound(err) {
				return fmt.Errorf("deleting firewall %s: %w", name, err)
			}
			if op != nil {
				if err := insertAndWait(ctx, op); err != nil {
					return fmt.Errorf("waiting for firewall delete: %w", err)
				}
			}
		}
	}

	// Skip and log unmarked resources
	if unmarked, ok := byCluster["unmarked"]; ok {
		for _, sn := range unmarked.Subnets {
			fmt.Fprintf(os.Stderr, "[down] shared-vpc %s: skipping subnet %s (no gkebnkctl ownership marker). Run `gcloud compute networks subnets delete %s --region=%s --project=%s` to delete manually.\n", vpc, sn.GetName(), sn.GetName(), region, project)
		}
		for _, fw := range unmarked.Firewalls {
			fmt.Fprintf(os.Stderr, "[down] shared-vpc %s: skipping firewall %s (no gkebnkctl ownership marker). Run `gcloud compute firewalls delete %s --project=%s` to delete manually.\n", vpc, fw.GetName(), fw.GetName(), project)
		}
		for _, rt := range unmarked.Routers {
			fmt.Fprintf(os.Stderr, "[down] shared-vpc %s: skipping router %s (no gkebnkctl ownership marker). Run `gcloud compute routers delete %s --region=%s --project=%s` to delete manually.\n", vpc, rt.GetName(), rt.GetName(), region, project)
		}
	}

	// Delete routers and NATs (last-one-out shared resource)
	var sweepRouters []*computepb.Router
	if otherClustersExist {
		sweepRouters = clusterResources.Routers
	} else {
		for c, res := range byCluster {
			if c != "unmarked" {
				sweepRouters = append(sweepRouters, res.Routers...)
			}
		}
	}

	for _, rt := range sweepRouters {
		name := rt.GetName()
		if otherClustersExist {
			// As with the firewall, we knowingly accept the stale marker. The last one out sweeps all.
			fmt.Fprintf(os.Stderr, "[down] shared-vpc %s: skipping router %s because other clusters still have subnets in this VPC (leaving stale ownership marker)\n", vpc, name)
		} else {
			fmt.Fprintf(os.Stderr, "[down] shared-vpc %s: deleting router %s (and its NATs)\n", vpc, name)
			op, err := clients.Routers.Delete(ctx, &computepb.DeleteRouterRequest{Project: project, Region: region, Router: name})
			if err != nil && !isNotFound(err) {
				return fmt.Errorf("deleting router %s: %w", name, err)
			}
			if op != nil {
				if err := insertAndWait(ctx, op); err != nil {
					return fmt.Errorf("waiting for router delete: %w", err)
				}
			}
		}
	}

	return nil
}

// stripStuckFinalizers best-effort strips finalizers from F5 BNK CRs that
// otherwise block namespace and cluster deletion when controller pods are gone.
func stripStuckFinalizers(ctx context.Context, kubeconfigPath string) {
	cfg, err := k8s.BuildRESTConfigFromKubeconfig(kubeconfigPath, "")
	if err != nil {
		return
	}
	dyn, err := k8s.NewDynamicFromConfig(cfg)
	if err != nil {
		return
	}
	stripStuckFinalizersWithClient(ctx, dyn)
}

func stripStuckFinalizersWithClient(ctx context.Context, dyn dynamic.Interface) {
	if dyn == nil {
		return
	}
	tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	crds := []string{
		"f5-spk-vlans.k8s.f5net.com",
		"f5-spk-staticroutes.k8s.f5net.com",
		"f5-spk-egresses.k8s.f5net.com",
		"f5-spk-snatpools.k8s.f5net.com",
		"infras.gateway.k8s.f5.com",
		"gatewaysettings.gateway.k8s.f5.com",
		"egressgateways.gateway.k8s.f5.com",
		"analyzers.k8s.f5.com",
		"cnecontrollers.k8s.f5.com",
		"cneinstances.k8s.f5.com",
		"dssms.k8s.f5.com",
		"f5tmms.k8s.f5.com",
		"coremonds.k8s.f5.com",
		"crdconversions.k8s.f5.com",
		"crdinstallers.k8s.f5.com",
		"csrcs.k8s.f5.com",
		"cwcs.k8s.f5.com",
		"ipamcontrollers.k8s.f5.com",
		"rabbitmqs.k8s.f5.com",
	}
	gvrs := crdsToGVRs(crds)
	namespaces := []string{
		"f5-cne-core",
		"f5-cne-system",
		"f5-bnk-instance",
		"f5-utils",
		"f5-bnk",
		"bnk-demo",
		"egress-test",
	}

	for _, ns := range namespaces {
		for _, gvr := range gvrs {
			list, listErr := dyn.Resource(gvr).Namespace(ns).List(tctx, metav1.ListOptions{})
			if listErr != nil || list == nil {
				continue
			}
			for i := range list.Items {
				item := list.Items[i]
				if len(item.GetFinalizers()) == 0 {
					continue
				}
				unstructured.RemoveNestedField(item.Object, "metadata", "finalizers")
				if _, updErr := dyn.Resource(gvr).Namespace(ns).Update(tctx, &item, metav1.UpdateOptions{}); updErr == nil {
					fmt.Fprintf(os.Stderr, "[down] stripped finalizers from %s %s/%s\n", gvr.Resource, ns, item.GetName())
				}
			}
		}
	}
}

func crdsToGVRs(crds []string) []schema.GroupVersionResource {
	var gvrs []schema.GroupVersionResource
	for _, crd := range crds {
		var resource, group string
		for i := 0; i < len(crd); i++ {
			if crd[i] == '.' {
				resource = crd[:i]
				group = crd[i+1:]
				break
			}
		}
		if resource == "" || group == "" {
			continue
		}
		version := "v1"
		if group == "gateway.k8s.f5.com" {
			version = "v1alpha1"
		}
		gvrs = append(gvrs, schema.GroupVersionResource{Group: group, Version: version, Resource: resource})
		if resource == "f5-spk-egresses" {
			gvrs = append(gvrs, schema.GroupVersionResource{Group: group, Version: "v3", Resource: resource})
		}
	}
	return gvrs
}
